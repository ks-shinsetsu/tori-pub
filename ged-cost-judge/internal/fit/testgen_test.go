package fit

import (
	"fmt"
	"math/rand/v2"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
)

// A tiny synthetic world for the tests (independent of internal/simulate).

type simJudge struct{ beta, tau, pi float64 }

type simWorld struct {
	rng    *rand.Rand
	cos    map[[2]string]float64 // substitution cos between a reference prop and its replacement
	theta  ged.Theta
	neg    float64
	judges []simJudge
	sigma  float64
}

func newWorld(seed uint64, theta ged.Theta, sigma float64) *simWorld {
	return &simWorld{
		rng: rand.New(rand.NewPCG(seed, 99)), cos: map[[2]string]float64{}, theta: theta, sigma: sigma,
		judges: []simJudge{{2.0, 0.6, 0.3}, {1.5, 0.9, -0.2}, {2.5, 0.4, 0}},
	}
}

func (w *simWorld) sim(ref, v string) float64 {
	if ref == v {
		return 1
	}
	if c, ok := w.cos[[2]string{ref, v}]; ok {
		return c
	}
	return 0.15
}

// source builds a reference graph with n propositions and a connected set of edges.
func (w *simWorld) source(n int) ged.Graph {
	var g ged.Graph
	for i := range n {
		g.Props = append(g.Props, ged.Prop{ID: fmt.Sprintf("p%d", i)})
	}
	for i := 1; i < n; i++ {
		to := w.rng.IntN(i)
		g.Edges = append(g.Edges, ged.Edge{From: g.Props[i].ID, To: g.Props[to].ID, Kind: ged.Kinds[w.rng.IntN(3)]})
	}
	return g
}

// edit kinds
const (
	eDelProp = iota
	eAddProp
	eDelEdge
	eAddEdge
	eRelabel
	eReverse
	eSubst
	eNeg
	nEdits
)

// apply applies one random edit of kind k to g (in place) and returns the polarity-flip count.
func (w *simWorld) apply(g *ged.Graph, k int, tag string) int {
	switch k {
	case eDelProp:
		if len(g.Props) <= 2 {
			return 0
		}
		i := 1 + w.rng.IntN(len(g.Props)-1)
		id := g.Props[i].ID
		g.Props = append(g.Props[:i], g.Props[i+1:]...)
		var es []ged.Edge
		for _, e := range g.Edges {
			if e.From != id && e.To != id {
				es = append(es, e)
			}
		}
		g.Edges = es
	case eAddProp:
		id := "q" + tag
		to := g.Props[w.rng.IntN(len(g.Props))].ID
		g.Props = append(g.Props, ged.Prop{ID: id})
		g.Edges = append(g.Edges, ged.Edge{From: id, To: to, Kind: ged.RA})
	case eDelEdge:
		if len(g.Edges) == 0 {
			return 0
		}
		i := w.rng.IntN(len(g.Edges))
		g.Edges = append(g.Edges[:i], g.Edges[i+1:]...)
	case eAddEdge:
		for try := 0; try < 10; try++ {
			a, b := w.rng.IntN(len(g.Props)), w.rng.IntN(len(g.Props))
			if a == b {
				continue
			}
			dup := false
			for _, e := range g.Edges {
				if (e.From == g.Props[a].ID && e.To == g.Props[b].ID) || (e.From == g.Props[b].ID && e.To == g.Props[a].ID) {
					dup = true
				}
			}
			if !dup {
				g.Edges = append(g.Edges, ged.Edge{From: g.Props[a].ID, To: g.Props[b].ID, Kind: ged.Kinds[w.rng.IntN(3)]})
				break
			}
		}
	case eRelabel:
		if len(g.Edges) == 0 {
			return 0
		}
		i := w.rng.IntN(len(g.Edges))
		for {
			k := ged.Kinds[w.rng.IntN(3)]
			if k != g.Edges[i].Kind {
				g.Edges[i].Kind = k
				break
			}
		}
	case eReverse:
		if len(g.Edges) == 0 {
			return 0
		}
		i := w.rng.IntN(len(g.Edges))
		g.Edges[i].From, g.Edges[i].To = g.Edges[i].To, g.Edges[i].From
	case eSubst:
		i := w.rng.IntN(len(g.Props))
		old := g.Props[i].ID
		id := "s" + tag
		c := 0.3 + 0.7*w.rng.Float64()
		w.cos[[2]string{old, id}] = c
		g.Props[i].ID = id
		for j := range g.Edges {
			if g.Edges[j].From == old {
				g.Edges[j].From = id
			}
			if g.Edges[j].To == old {
				g.Edges[j].To = id
			}
		}
	case eNeg:
		return 1
	}
	return 0
}

// generate makes sources×4 triplets and 3 judges × 2 orders of judgments from the world's truth.
func (w *simWorld) generate(sources int, withNeg bool) ([]Triplet, []Judgment) {
	var trips []Triplet
	var js []Judgment
	gens := []string{"g1", "g2"}
	for s := range sources {
		ref := w.source(5 + w.rng.IntN(4))
		for k := range 4 {
			a, b := ref.Clone(), ref.Clone()
			negA, negB := 0, 0
			tag := fmt.Sprintf("%d_%d", s, k)
			na, nb := 1+w.rng.IntN(2), 1+w.rng.IntN(2)
			for i := range na {
				e := w.rng.IntN(nEdits)
				if e == eNeg && !withNeg {
					e = eDelEdge
				}
				negA += w.apply(&a, e, tag+"a"+fmt.Sprint(i))
			}
			for i := range nb {
				e := w.rng.IntN(nEdits)
				if e == eNeg && !withNeg {
					e = eAddEdge
				}
				negB += w.apply(&b, e, tag+"b"+fmt.Sprint(i))
			}
			trips = append(trips, Triplet{Source: s, Ref: ref, A: a, B: b, SimA: w.sim, SimB: w.sim,
				NegDiff: float64(negB - negA), Generator: gens[s%2], Type: "T3"})
		}
	}
	d, err := Delta(trips, w.theta)
	if err != nil {
		panic(err)
	}
	for t := range trips {
		u := w.sigma * w.rng.NormFloat64()
		for j, jd := range w.judges {
			for _, o := range []float64{1, -1} {
				D := d[t] + w.neg*trips[t].NegDiff
				z := jd.beta*D + jd.pi*o + u
				pa, pb, _ := Probs(z, jd.tau)
				r := w.rng.Float64()
				ans := AnswerTie
				switch {
				case r < pa:
					ans = AnswerA
				case r < pa+pb:
					ans = AnswerB
				}
				js = append(js, Judgment{Triplet: t, Judge: j, Order: o, Answer: ans})
			}
		}
	}
	return trips, js
}

func altTheta() ged.Theta {
	th := ged.Current()
	th[ged.PNodeMissing] = 1.3
	th[ged.PNodeExtra] = 0.7
	th[ged.PEdgeMissing] = 1.2
	th[ged.PTypeRACA], th[ged.PTypeRAMA], th[ged.PTypeCAMA] = 1.0, 1.0, 1.0
	th[ged.PDirection] = 1.2
	return th
}

func baseOpts(judges int) Options {
	return Options{Judges: judges, Theta0: ged.Current(), Lambda: 0, FitNeg: true, MaxOuter: 20, IncludeAll: true}
}
