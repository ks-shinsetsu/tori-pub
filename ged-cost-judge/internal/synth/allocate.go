package synth

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
)

// FormatWeight is a source format and its share (dataset-spec §1.1).
type FormatWeight struct {
	Format string
	Weight float64
}

// Formats are the six formats; the broadcast debate (QT30-like) carries 50%.
var Formats = []FormatWeight{
	{"TV debate with a moderator", 0.5},
	{"meeting", 0.1},
	{"one-on-one argument", 0.1},
	{"online forum thread", 0.1},
	{"opinion essay by a single author", 0.1},
	{"interview", 0.1},
}

// Domains are the twelve topic domains.
var Domains = []string{
	"politics", "economy", "healthcare", "education", "environment", "technology",
	"law and justice", "sports", "local government", "business management", "science", "daily life",
}

// sizeBins are the n_P bins with their shares (dataset-spec §1.2).
var sizeBins = []struct {
	name   string
	lo, hi int
	w      float64
}{{"8-10", 8, 10, 0.25}, {"11-15", 11, 15, 0.45}, {"16-20", 16, 20, 0.30}}

// AllocOptions configures Allocate.
type AllocOptions struct {
	N          int
	Seed       uint64
	Generators []string // model ids, crossed 1:1 with every stratum
	Exemplars  []Exemplar
	// Names is the name allocator (nil: no names drawn).
	Names *NameAllocator
}

// Allocate draws n source specs (dataset-spec §1.2, §1.5.2):
//
//   - formats by largest remainder over the §1.1 weights; within a format, domain cycles through a
//     shuffled order (balanced) and size bin, CA share, MA share, multi-premise and chain are drawn as a
//     Latin hypercube (one stratum per slot);
//   - the numeric targets follow §1.2 (n_E = round(n_P·U(0.55, 0.85)), CA ≥ 1 at U(5%, 15%), MA at
//     U(20%, 35%), the rest RA, isolated ≥ max(2, round(0.12·n_P)));
//   - slots are grouped by their same-format exemplar (for the debate: the exemplar whose size is
//     closer to n_P), sorted by (domain, size bin), and generators alternate along that order, so every
//     (format, domain, size bin) stratum is balanced 1:1 within ±1;
//   - consecutive slot pairs (one per generator) share the other-format exemplar, which rotates through
//     the other-format exemplars as a cyclic Latin square shifted by the format index, so exemplar pair ×
//     generator is a full cross balanced within ±1 (odd leftovers are placed greedily to keep that bound);
//   - the final order is shuffled with no two consecutive sources on the same pair.
func Allocate(o AllocOptions) ([]Spec, error) {
	if o.N <= 0 {
		return nil, nil
	}
	if len(o.Generators) == 0 {
		return nil, fmt.Errorf("synth: allocate: no generators")
	}
	rng := rand.New(rand.NewPCG(o.Seed, 0xa110c))
	ids := ExemplarIDs(o.Exemplars)
	counts := largestRemainder(o.N, Formats)

	type slot struct {
		spec  Spec
		same  string
		order float64
	}
	var all []Spec
	// leftover[pair][generator] counts slots without a partner of the other generator.
	leftover := map[int]map[string]int{}
	leftGen := 0
	for fi, fw := range Formats {
		m := counts[fi]
		if m == 0 {
			continue
		}
		var sameEx []Exemplar
		for _, ex := range o.Exemplars {
			if ex.Format == fw.Format {
				sameEx = append(sameEx, ex)
			}
		}
		domOrder := slices.Clone(Domains)
		rng.Shuffle(len(domOrder), func(i, j int) { domOrder[i], domOrder[j] = domOrder[j], domOrder[i] })
		lhs := func() []float64 {
			p := rng.Perm(m)
			out := make([]float64, m)
			for k := range m {
				out[k] = (float64(p[k]) + rng.Float64()) / float64(m)
			}
			return out
		}
		uSize, uCA, uMA, uMP, uCh := lhs(), lhs(), lhs(), lhs(), lhs()
		var slots []slot
		for k := range m {
			sp := Spec{Format: fw.Format, Domain: domOrder[k%len(domOrder)], Seed: rng.Uint64()}
			bin := sizeBins[len(sizeBins)-1]
			acc := 0.0
			for _, b := range sizeBins {
				acc += b.w
				if uSize[k] < acc {
					bin = b
					break
				}
			}
			sp.SizeBin = bin.name
			sp.Props = bin.lo + rng.IntN(bin.hi-bin.lo+1)
			edges := int(math.Round(float64(sp.Props) * (0.55 + 0.30*rng.Float64())))
			sp.Isolated = max(2, int(math.Round(0.12*float64(sp.Props))))
			// every non-isolated proposition needs an edge: n_P − isolated ≤ 2·n_E
			edges = max(edges, (sp.Props-sp.Isolated+1)/2)
			sp.CA = max(1, int(math.Round((0.05+0.10*uCA[k])*float64(edges))))
			sp.MA = int(math.Round((0.20 + 0.15*uMA[k]) * float64(edges)))
			sp.RA = edges - sp.CA - sp.MA
			if sp.RA < 1 {
				sp.MA -= 1 - sp.RA
				sp.RA = 1
			}
			sp.MultiPremise = uMP[k] < 0.5
			sp.Chain = uCh[k] < 0.5
			names, roles := speakersFor(fw.Format, rng)
			if o.Names != nil {
				sp.Names = o.Names.Draw(names)
			}
			sp.Speakers = names + roles
			same := ""
			best := math.Inf(1)
			for _, ex := range sameEx {
				if d := math.Abs(float64(ex.Props - sp.Props)); d < best {
					best, same = d, ex.ID
				}
			}
			slots = append(slots, slot{spec: sp, same: same, order: rng.Float64()})
		}
		// group by same-format exemplar
		groups := map[string][]slot{}
		var keys []string
		for _, s := range slots {
			if _, ok := groups[s.same]; !ok {
				keys = append(keys, s.same)
			}
			groups[s.same] = append(groups[s.same], s)
		}
		slices.Sort(keys)
		for _, key := range keys {
			g := groups[key]
			slices.SortFunc(g, func(a, b slot) int {
				if c := strings.Compare(a.spec.Domain, b.spec.Domain); c != 0 {
					return c
				}
				if c := strings.Compare(a.spec.SizeBin, b.spec.SizeBin); c != 0 {
					return c
				}
				return cmpFloat(a.order, b.order)
			})
			var others []string
			for _, id := range ids {
				ex, _ := FindExemplar(o.Exemplars, id)
				if ex.Format != fw.Format {
					others = append(others, id)
				}
			}
			for i := range g {
				sp := &g[i].spec
				unit := i / 2
				full := i/2*2+1 < len(g) // the slot has a partner of the other generator
				var other string
				switch {
				case len(others) == 0 || key == "":
				case full || len(o.Generators) == 1:
					sp.Generator = o.Generators[i%len(o.Generators)]
					other = others[(unit+fi)%len(others)]
				default:
					sp.Generator = o.Generators[leftGen%len(o.Generators)]
					leftGen++
					// keep pair × generator within ±1: avoid a pair this generator already got as a leftover
					// more often than the other generator(s)
					bestScore := math.MaxInt
					for r := range others {
						cand := others[(unit+fi+r)%len(others)]
						pid := PairID(ids, key, cand)
						sum := 0
						for _, v := range leftover[pid] {
							sum += v
						}
						score := leftover[pid][sp.Generator]*len(o.Generators) - sum
						if score < bestScore {
							bestScore, other = score, cand
						}
					}
				}
				if sp.Generator == "" {
					sp.Generator = o.Generators[i%len(o.Generators)]
				}
				if key != "" && other != "" {
					order := []string{key, other}
					if rng.IntN(2) == 1 {
						order[0], order[1] = order[1], order[0]
					}
					sp.ExemplarPair = ExemplarPair{ID: PairID(ids, key, other), Order: order}
					if !full && len(o.Generators) > 1 {
						if leftover[sp.ExemplarPair.ID] == nil {
							leftover[sp.ExemplarPair.ID] = map[string]int{}
						}
						leftover[sp.ExemplarPair.ID][sp.Generator]++
					}
				}
				all = append(all, *sp)
			}
		}
	}
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	separatePairs(all)
	return all, nil
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// separatePairs reorders specs so that no two consecutive specs share an exemplar pair (best effort).
func separatePairs(s []Spec) {
	for pass := 0; pass < 3; pass++ {
		clean := true
		for i := 1; i < len(s); i++ {
			if s[i].ExemplarPair.ID == 0 || s[i].ExemplarPair.ID != s[i-1].ExemplarPair.ID {
				continue
			}
			clean = false
			for j := i + 1; j < len(s); j++ {
				okHere := s[j].ExemplarPair.ID != s[i-1].ExemplarPair.ID &&
					(i+1 >= len(s) || s[j].ExemplarPair.ID != s[i+1].ExemplarPair.ID)
				okThere := s[i].ExemplarPair.ID != s[j-1].ExemplarPair.ID &&
					(j+1 >= len(s) || s[i].ExemplarPair.ID != s[j+1].ExemplarPair.ID)
				if okHere && okThere {
					s[i], s[j] = s[j], s[i]
					break
				}
			}
		}
		if clean {
			return
		}
	}
}

// speakersFor returns the number of named speakers and of role speakers for a format
// (debate: 3–5 panellists + host; others 1–3, dataset-spec §1.2).
func speakersFor(format string, rng *rand.Rand) (names, roles int) {
	switch format {
	case "TV debate with a moderator":
		return 3 + rng.IntN(3), 1
	case "meeting":
		return 3, 0
	case "one-on-one argument":
		return 2, 0
	case "online forum thread":
		return 3, 0
	case "interview":
		return 1, 1
	case "opinion essay by a single author":
		return 0, 1
	}
	return 2, 0
}

// largestRemainder splits n over the weights.
func largestRemainder(n int, ws []FormatWeight) []int {
	out := make([]int, len(ws))
	type rem struct {
		i int
		r float64
	}
	var rs []rem
	used := 0
	for i, w := range ws {
		x := float64(n) * w.Weight
		out[i] = int(math.Floor(x))
		used += out[i]
		rs = append(rs, rem{i, x - math.Floor(x)})
	}
	slices.SortStableFunc(rs, func(a, b rem) int { return cmpFloat(b.r, a.r) })
	for k := 0; used < n; k++ {
		out[rs[k%len(rs)].i]++
		used++
	}
	return out
}

// Topic is one entry of the stage-0 topic bank.
type Topic struct {
	ID        string `json:"id"`
	Domain    string `json:"domain"`
	Question  string `json:"question"`
	Generator string `json:"generator,omitempty"`
}

// TopicQueue hands out topics per domain. A topic is consumed only by an accepted source: a rejected
// source's topic is handed back with Return and goes to the front (dataset-spec §1.3).
type TopicQueue struct {
	byDomain map[string][]Topic
}

// NewTopicQueue builds the queue from the bank (bank order within a domain).
func NewTopicQueue(bank []Topic) *TopicQueue {
	q := &TopicQueue{byDomain: map[string][]Topic{}}
	for _, t := range bank {
		q.byDomain[t.Domain] = append(q.byDomain[t.Domain], t)
	}
	return q
}

// Next takes the next topic of domain.
func (q *TopicQueue) Next(domain string) (Topic, bool) {
	ts := q.byDomain[domain]
	if len(ts) == 0 {
		return Topic{}, false
	}
	q.byDomain[domain] = ts[1:]
	return ts[0], true
}

// Return puts a topic back at the front of its domain.
func (q *TopicQueue) Return(t Topic) {
	q.byDomain[t.Domain] = append([]Topic{t}, q.byDomain[t.Domain]...)
}

// AssignTopics gives every spec the next topic of its domain.
func AssignTopics(specs []Spec, q *TopicQueue) error {
	for i := range specs {
		t, ok := q.Next(specs[i].Domain)
		if !ok {
			return fmt.Errorf("synth: topic bank has no topic left for domain %q", specs[i].Domain)
		}
		specs[i].TopicID, specs[i].Topic = t.ID, t.Question
	}
	return nil
}
