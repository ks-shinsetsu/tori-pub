// Package simulate synthesizes triplets and judgments without using the API (plan §6.5 / P1).
//
// The judges' "true costs" θ* are set off from the current values, and 3-way judgments are produced from GED differences at those costs.
// The goal is to check whether estimation recovers θ* (identifiability and parameter recovery) and whether the edit
// catalogue (internal/perturb, dataset-spec §2) yields the per-cell informative counts of decisions-stats §15.3.
package simulate

import (
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
)

// Judge is a synthetic judge.
type Judge struct {
	Beta, Tau, Pi float64
}

// Source shapes.
const (
	ShapeSpec = "spec" // dataset-spec §1.2 numeric targets (QT30-like size, density, isolated floor, relation mix, locality)
	ShapePoC  = "poc"  // the PoC generator (random tree, 15% of propositions without an outgoing relation)
)

// Config configures the simulation.
type Config struct {
	Sources int // number of source arguments (PerSrc triplets each)
	PerSrc  int
	Seed    uint64
	// World names the true-cost world (informational; True and TrueNeg are what the simulator uses).
	World string
	// True is θ*, TrueNeg the polarity-flip weight.
	True    ged.Theta
	TrueNeg float64
	Judges  []Judge
	Sigma   float64 // standard deviation of the per-triplet offset ε_t shared by all judges
	Random  float64 // probability of answering at random
	// Shape is the source generator (ShapeSpec or ShapePoC). MinProps / MaxProps apply to ShapePoC only.
	Shape    string
	MinProps int
	MaxProps int
	// Catalogue are the catalogue options (strata shares, t3_ident / t3_diag shares, T1 settings); the per-source utterance map
	// and source-proposition cos are filled in by the simulator.
	Catalogue perturb.BuildOptions
	// Hetero is the per-source (per-context) cost heterogeneity: each source draws θ_s,p = θ*_p · exp(Hetero·z − Hetero²/2)
	// independently per parameter (misspecified scenario; 0 = homogeneous).
	Hetero float64
	// LabelNoise is the probability that a non-random answer is replaced by one of the other two labels (misspecified scenario).
	LabelNoise float64
}

// Default is the main scenario of plan §6.5 (β ≈ 2, σ = 1, 10% random) with the revised catalogue and §1.2 sources,
// in the alternative world alt′ (WorldAlt, decisions-stats Appendix A preamble).
func Default() Config {
	w, _ := WorldByName(WorldAlt)
	return Config{
		Sources: 250, PerSrc: 4, Seed: 1,
		World: w.Name, True: w.True, TrueNeg: w.TrueNeg,
		Judges: []Judge{{2.0, 0.6, 0.3}, {1.5, 0.9, -0.2}, {2.5, 0.4, 0.0}},
		Sigma:  1, Random: 0.1, Shape: ShapeSpec, MinProps: 6, MaxProps: 18,
	}
}

// World names (decisions-stats Appendix A.5).
const (
	WorldNull   = "null"
	WorldMild   = "mild"
	WorldSingle = "single"
	WorldAlt    = "alt"
	WorldEdgeN  = "edgeN"
)

// World is a true cost vector θ* with its polarity-flip weight.
type World struct {
	Name    string
	True    ged.Theta
	TrueNeg float64
	// Doc describes the world as defined in decisions-stats Appendix A.5 (and the choices made where it is silent).
	Doc string
}

// TrueNegDefault is the polarity-flip weight of every world (as in the A.5 batch of 2026-10-28; θ_neg has no
// table value and no margin decision, so the worlds do not move it).
const TrueNegDefault = 1.5

// Worlds are the five A.5 worlds in batch order.
//
//   - null: θ* = θ₀.
//   - edgeN: θ* = θ₀ × 4/3 with I-missing at 5/3: on the sum-normalized scale I-missing sits at θ₀ + m and edge-extra
//     at θ₀ − m (m = 0.25), the other four margin-judged costs at θ₀ (ŝ_true = 4/3). The knots and the CA pairs are
//     scaled with the rest (so their normalized values equal θ₀); θ_neg is not scaled.
//   - alt′: I-missing 1.3, I-extra 0.7, edge-missing 1.2, RA↔MA 1.05 (θ₀ + 0.30), direction 1.2, RA↔CA 1.5,
//     CA↔MA 0.75, knots at θ₀. The CA pairs are seen only by the untied diagnostic fit (§12b).
//   - single: I-missing 1.3, everything else at θ₀.
//   - mild: the seven pre-reduction margin-judged costs (I-missing, I-extra, edge-missing, the three type costs,
//     direction) at θ₀ + 0.15, knots at θ₀.
func Worlds() []World {
	th0 := ged.Current()
	null := th0

	edge := th0
	for p := range edge {
		edge[p] *= 4.0 / 3
	}
	edge[ged.PNodeMissing] = 5.0 / 3

	alt := th0
	alt[ged.PNodeMissing] = 1.3
	alt[ged.PNodeExtra] = 0.7
	alt[ged.PEdgeMissing] = 1.2
	alt[ged.PTypeRAMA] = 1.05
	alt[ged.PTypeRACA] = 1.5
	alt[ged.PTypeCAMA] = 0.75
	alt[ged.PDirection] = 1.2

	single := th0
	single[ged.PNodeMissing] = 1.3

	mild := th0
	for _, p := range []int{ged.PNodeMissing, ged.PNodeExtra, ged.PEdgeMissing, ged.PTypeRACA, ged.PTypeRAMA, ged.PTypeCAMA, ged.PDirection} {
		mild[p] += 0.15
	}
	return []World{
		{WorldNull, null, TrueNegDefault, "θ* = θ₀"},
		{WorldEdgeN, edge, TrueNegDefault, "θ* = θ₀ × 4/3, I-missing 5/3 (normalized: I-missing θ₀ + 0.25, edge-extra θ₀ − 0.25, rest θ₀; knots and CA pairs scaled, θ_neg not)"},
		{WorldAlt, alt, TrueNegDefault, "alt′: I-missing 1.3, I-extra 0.7, edge-missing 1.2, RA↔MA 1.05, direction 1.2, RA↔CA 1.5, CA↔MA 0.75 (CA pairs seen only by the untied diagnostic fit)"},
		{WorldSingle, single, TrueNegDefault, "I-missing 1.3, everything else at θ₀"},
		{WorldMild, mild, TrueNegDefault, "I-missing, I-extra, edge-missing, the three type costs and direction at θ₀ + 0.15; knots at θ₀"},
	}
}

// WorldByName returns the named world.
func WorldByName(name string) (World, error) {
	for _, w := range Worlds() {
		if w.Name == name {
			return w, nil
		}
	}
	return World{}, fmt.Errorf("unknown world %q (null | edgeN | alt | single | mild)", name)
}

// WithWorld returns c with the true costs of w.
func (c Config) WithWorld(w World) Config {
	c.World, c.True, c.TrueNeg = w.Name, w.True, w.TrueNeg
	return c
}

// Misspecified is Default with per-context cost heterogeneity (SD 0.25 on the log scale, per source and parameter)
// and 5% label noise (decisions-stats §16 item 14).
func Misspecified() Config {
	c := Default()
	c.Hetero = 0.25
	c.LabelNoise = 0.05
	return c
}

// Meta is auxiliary information about a triplet.
type Meta struct {
	Type   perturb.TripletType
	EditsA []perturb.Edit
	EditsB []perturb.Edit
	// Info is the plan metadata (flags, strata, per-side counts) from perturb.DrawTriplet.
	Info perturb.PlanInfo
	// Target is the T3 Δ targeting of the slot ("near", "clear"; "" for t3_ident and other types).
	Target string
	// Delta0 is Δ at θ₀ with the simulated (realized) cos.
	Delta0 float64
	// T1Stratum is the T1 stratum re-assigned from Delta0 (dataset-spec §3.1 item 3); "" for other types.
	T1Stratum string
	// TrueDelta is Δ at θ* (including the polarity-flip weight; at the source's θ_s when Hetero > 0).
	TrueDelta float64
	// Generator is the synthetic generator label of the source (GeneratorOf).
	Generator string
}

// Flags are the fit.Triplet flags of the triplet (decisions-stats §16 #16): x_spk, diag_ca_type, del_add_cooccur,
// t3_ident, t3_diag, and t1_gate for a T1 triplet in the Δ(θ₀) ≥ 2 stratum (§9.1; realized Δ(θ₀) > 0).
func (m Meta) Flags() []string {
	var f []string
	add := func(name string, ok bool) {
		if ok {
			f = append(f, name)
		}
	}
	add("x_spk", m.Info.XSpk)
	add("diag_ca_type", m.Info.DiagCAType)
	add("del_add_cooccur", m.Info.DelAddCooccur)
	add("t3_ident", m.Info.T3Ident)
	add("t3_diag", m.Info.T3Diag)
	add("t1_gate", m.Type == perturb.T1Monotone && m.Delta0 > 1e-9 && m.T1Stratum == perturb.T1High)
	return f
}

// Generators are the synthetic generator labels; sources alternate between them (s mod 2), so the generator-stratified
// bootstrap and CV of decisions-stats §6–§7 are exercised with the 1:1 balance of §15 item 2.
var Generators = []string{"gen-a", "gen-b"}

// GeneratorOf is the generator label of source s.
func GeneratorOf(s int) string { return Generators[s%len(Generators)] }

// JudgeFamily is the synthetic model-family label of judge j (fit.Judgment.Family).
func JudgeFamily(j int) string { return fmt.Sprintf("judge-%d", j+1) }

// InFit reports whether the triplet enters the fit by default (X-spk and diagnostic CA relabels are excluded,
// decisions-stats §16 item 16; P-add/P-del co-occurrence is never produced but would be excluded too). On simulated
// data it equals fit.Filter with the default options (TestFitSetMatchesFilter).
func (m Meta) InFit() bool { return !m.Info.XSpk && !m.Info.DiagCAType && !m.Info.DelAddCooccur }

type world struct {
	rng  *rand.Rand
	base map[[2]string]float64
}

func (w *world) baseCos(a, b string) float64 {
	if a > b {
		a, b = b, a
	}
	k := [2]string{a, b}
	if c, ok := w.base[k]; ok {
		return c
	}
	c := 0.1 + 0.35*w.rng.Float64()
	w.base[k] = c
	return c
}

// sourceInfo is a source graph with its utterance map.
type sourceInfo struct {
	g   ged.Graph
	utt map[string]int
}

func (w *world) source(src int, cfg Config) sourceInfo {
	var si sourceInfo
	if cfg.Shape == ShapePoC {
		si = w.sourcePoC(src, cfg)
	} else {
		si = w.sourceSpec(src)
	}
	// Fix the source-pair cos up front in a fixed order (the R-add filter reads it during the catalogue draws).
	for i, a := range si.g.Props {
		for _, b := range si.g.Props[i+1:] {
			w.baseCos(a.ID, b.ID)
		}
	}
	return si
}

func (w *world) sourcePoC(src int, cfg Config) sourceInfo {
	n := cfg.MinProps + w.rng.IntN(cfg.MaxProps-cfg.MinProps+1)
	var g ged.Graph
	utt := map[string]int{}
	for i := range n {
		id := fmt.Sprintf("s%d.p%d", src, i)
		g.Props = append(g.Props, ged.Prop{ID: id})
		utt[id] = i
	}
	for i := 1; i < n; i++ {
		if w.rng.Float64() < 0.15 {
			continue // propositions without relations
		}
		parent := w.rng.IntN(i)
		kind := ged.RA
		switch r := w.rng.Float64(); {
		case r < 0.25:
			kind = ged.CA
		case r < 0.4:
			kind = ged.MA
		}
		g.Edges = append(g.Edges, ged.Edge{From: g.Props[i].ID, To: g.Props[parent].ID, Kind: kind})
	}
	return sourceInfo{g, utt}
}

// sourceSpec draws a source with the dataset-spec §1.2 numeric targets: n_P in 8–10 / 11–15 / 16–20 (25 / 45 / 30%),
// n_E = round(n_P·U(0.55, 0.85)), ≥ max(2, round(0.12·n_P)) isolated propositions, CA share U(5, 15)% with ≥ 1 CA,
// MA share U(20, 35)%, rest RA, ≤ 2 propositions per utterance, and relations at utterance distance ≤1 / 2–4 / 5+
// in 68 / 21 / 11% (QT30, dataset-spec §0). Relations run from the later to the earlier proposition (a forest).
func (w *world) sourceSpec(src int) sourceInfo {
	var n int
	switch r := w.rng.Float64(); {
	case r < 0.25:
		n = 8 + w.rng.IntN(3)
	case r < 0.70:
		n = 11 + w.rng.IntN(5)
	default:
		n = 16 + w.rng.IntN(5)
	}
	var g ged.Graph
	utt := map[string]int{}
	u, inU := 0, 0 // current utterance and its number of propositions
	for i := range n {
		id := fmt.Sprintf("s%d.p%d", src, i)
		g.Props = append(g.Props, ged.Prop{ID: id})
		// a second proposition joins the current utterance with probability 0.3 (≤ 2 propositions per utterance)
		if i > 0 && !(inU == 1 && w.rng.Float64() < 0.3) {
			u, inU = u+1, 0
		}
		utt[id] = u
		inU++
	}
	nE := int(math.Round(float64(n) * (0.55 + 0.30*w.rng.Float64())))
	nIso := max(2, int(math.Round(0.12*float64(n))))
	// isolated propositions: a random subset; the rest form a forest with nE edges.
	perm := w.rng.Perm(n)
	iso := map[int]bool{}
	for _, i := range perm[:nIso] {
		iso[i] = true
	}
	var conn []int
	for i := range n {
		if !iso[i] {
			conn = append(conn, i)
		}
	}
	nE = min(nE, len(conn)-1)
	// choose which connected propositions get a parent: nE of conn[1:].
	child := map[int]bool{}
	cp := w.rng.Perm(len(conn) - 1)
	for _, k := range cp[:nE] {
		child[conn[k+1]] = true
	}
	pCA := 0.05 + 0.10*w.rng.Float64()
	pMA := 0.20 + 0.15*w.rng.Float64()
	stratumW := []float64{0.68, 0.21, 0.11}
	for ci, i := range conn {
		if !child[i] {
			continue
		}
		ui := utt[g.Props[i].ID]
		var strata [3][]int
		for _, j := range conn[:ci] {
			d := ui - utt[g.Props[j].ID]
			switch {
			case d <= 1:
				strata[0] = append(strata[0], j)
			case d <= 4:
				strata[1] = append(strata[1], j)
			default:
				strata[2] = append(strata[2], j)
			}
		}
		ws := make([]float64, 3)
		for k := range 3 {
			if len(strata[k]) > 0 {
				ws[k] = stratumW[k]
			}
		}
		k := pickW(w.rng, ws)
		j := strata[k][w.rng.IntN(len(strata[k]))]
		kind := ged.RA
		switch r := w.rng.Float64(); {
		case r < pCA:
			kind = ged.CA
		case r < pCA+pMA:
			kind = ged.MA
		}
		g.Edges = append(g.Edges, ged.Edge{From: g.Props[i].ID, To: g.Props[j].ID, Kind: kind})
	}
	if len(g.Edges) > 0 && !slices.ContainsFunc(g.Edges, func(e ged.Edge) bool { return e.Kind == ged.CA }) {
		g.Edges[w.rng.IntN(len(g.Edges))].Kind = ged.CA
	}
	return sourceInfo{g, utt}
}

func pickW(rng *rand.Rand, ws []float64) int {
	tot := 0.0
	for _, x := range ws {
		tot += x
	}
	r := rng.Float64() * tot
	last := 0
	for i, x := range ws {
		if x <= 0 {
			continue
		}
		last = i
		if r < x {
			return i
		}
		r -= x
	}
	return last
}

func (w *world) cosFor(e perturb.Edit) float64 {
	u := w.rng.Float64()
	switch e.Kind {
	case perturb.PPara:
		switch e.Strength {
		case "weak":
			return 0.9 + 0.07*u
		case "medium":
			return 0.83 + 0.07*u
		default:
			return 0.75 + 0.08*u
		}
	case perturb.PShift:
		switch e.Strength {
		case "small":
			return 0.6 + 0.15*u
		case "medium":
			return 0.45 + 0.15*u
		default:
			return 0.25 + 0.2*u
		}
	case perturb.PNeg:
		return 0.85 + 0.1*u
	}
	return 1
}

// sim builds a table of cos between variant and source propositions and returns a function that looks it up.
// Estimation processes triplets in parallel, so the table is fully populated up front (the function only reads).
func (w *world) sim(src ged.Graph, v perturb.Variant, tag string) ged.Sim {
	own := map[string]float64{}
	for _, id := range slices.Sorted(maps.Keys(v.Changed)) { // fix the order of random draws
		own[id] = w.cosFor(v.Changed[id])
	}
	table := map[[2]string]float64{}
	for _, r := range src.Props {
		for _, p := range v.Graph.Props {
			var c float64
			switch o, ok := v.Origin[p.ID]; {
			case !ok:
				c = w.baseCos(r.ID, tag+"/"+p.ID) // an added proposition is unrelated to every proposition
			case o == r.ID:
				c = 1
				if x, ok := own[p.ID]; ok {
					c = x
				}
			default:
				c = w.baseCos(r.ID, o)
			}
			table[[2]string{r.ID, p.ID}] = c
		}
	}
	return func(ref, vid string) float64 { return table[[2]string{ref, vid}] }
}

// nearThreshold is |Δ(θ₀)| below which a T3 pair counts as "near 0" (plan §4.3).
const nearThreshold = 0.3

// Generate synthesizes triplets and judgments.
func Generate(cfg Config) ([]fit.Triplet, []fit.Judgment, []Meta, error) {
	w := &world{rng: rand.New(rand.NewPCG(cfg.Seed, 0x5eed)), base: map[[2]string]float64{}}
	th0 := ged.Current()
	var trips []fit.Triplet
	var metas []Meta
	var thetas []ged.Theta // per-source θ_s (Hetero > 0)
	for s := range cfg.Sources {
		si := w.source(s, cfg)
		src := si.g
		th := cfg.True
		if cfg.Hetero > 0 {
			for p := range ged.NumParams {
				th[p] *= math.Exp(cfg.Hetero*w.rng.NormFloat64() - cfg.Hetero*cfg.Hetero/2)
			}
		}
		opts := cfg.Catalogue
		opts.Utterance = si.utt
		opts.Cos = w.baseCos // every source pair is already in the table, so this only reads
		for k := range cfg.PerSrc {
			typ := perturb.DrawType(w.rng)
			var tp perturb.TripletPlan
			var sa, sb ged.Sim
			var d0 float64
			target := ""
			slot := opts
			if typ == perturb.T3Contrast {
				sub := opts.DrawSub(w.rng) // once per slot, so the Δ-targeting redraws keep the sub-strata's shares
				slot.Sub = &sub
			}
			for attempt := 0; ; attempt++ {
				tp = perturb.DrawTriplet(w.rng, src, typ, slot)
				tag := fmt.Sprintf("t%d.%d", s, k)
				sa, sb = w.sim(src, tp.A, tag+"a"), w.sim(src, tp.B, tag+"b")
				var err error
				if d0, err = delta(src, tp.A, tp.B, sa, sb, th0); err != nil {
					return nil, nil, nil, err
				}
				if typ != perturb.T3Contrast || tp.T3Ident || tp.T3Diag {
					target = ""
					break
				}
				// The ordinary T3 slots: half have a difference near 0 under current costs and half a clear difference (plan §4.3).
				wantNear := k%2 == 0
				target = map[bool]string{true: "near", false: "clear"}[wantNear]
				if (math.Abs(d0) < nearThreshold) == wantNear || attempt >= 50 {
					break
				}
			}
			m := Meta{Type: typ, EditsA: tp.A.Edits, EditsB: tp.B.Edits, Info: tp.PlanInfo, Target: target, Delta0: d0,
				Generator: GeneratorOf(s)}
			tr := fit.Triplet{
				Source: s, Ref: src, A: tp.A.Graph, B: tp.B.Graph, SimA: sa, SimB: sb,
				NegDiff: float64(tp.B.Neg - tp.A.Neg),
				Type:    string(typ), Generator: m.Generator,
			}
			if typ == perturb.T1Monotone {
				m.T1Stratum = perturb.T1StratumOf(d0)
				tr.T1Delta0 = d0
			}
			tr.Flags = m.Flags()
			trips = append(trips, tr)
			metas = append(metas, m)
			thetas = append(thetas, th)
		}
	}

	var trueDelta []float64
	if cfg.Hetero > 0 {
		trueDelta = make([]float64, len(trips))
		for t, tr := range trips {
			d, err := deltaG(tr.Ref, tr.A, tr.B, tr.SimA, tr.SimB, thetas[t])
			if err != nil {
				return nil, nil, nil, err
			}
			trueDelta[t] = d
		}
	} else {
		var err error
		if trueDelta, err = fit.Delta(trips, cfg.True); err != nil {
			return nil, nil, nil, err
		}
	}
	var js []fit.Judgment
	for t := range trips {
		d := trueDelta[t] + cfg.TrueNeg*trips[t].NegDiff
		metas[t].TrueDelta = d
		eps := w.rng.NormFloat64() * cfg.Sigma
		for j, jd := range cfg.Judges {
			for _, o := range []float64{1, -1} {
				var y fit.Answer
				if w.rng.Float64() < cfg.Random {
					y = fit.Answer(w.rng.IntN(3))
				} else {
					z := jd.Beta*d + eps + jd.Pi*o
					pa := 1 / (1 + math.Exp(-(z - jd.Tau)))
					pb := 1 / (1 + math.Exp(-(-z - jd.Tau)))
					switch u := w.rng.Float64(); {
					case u < pa:
						y = fit.AnswerA
					case u < pa+pb:
						y = fit.AnswerB
					default:
						y = fit.AnswerTie
					}
					if cfg.LabelNoise > 0 && w.rng.Float64() < cfg.LabelNoise {
						y = fit.Answer((int(y) + 1 + w.rng.IntN(2)) % 3)
					}
				}
				js = append(js, fit.Judgment{Triplet: t, Judge: j, Order: o, Answer: y, Family: JudgeFamily(j)})
			}
		}
	}
	return trips, js, metas, nil
}

// Subset keeps the triplets with keep(meta) and re-indexes the judgments.
func Subset(trips []fit.Triplet, js []fit.Judgment, metas []Meta, keep func(Meta) bool) ([]fit.Triplet, []fit.Judgment, []Meta) {
	idx := make([]int, len(trips))
	var ts []fit.Triplet
	var ms []Meta
	for i := range trips {
		idx[i] = -1
		if keep(metas[i]) {
			idx[i] = len(ts)
			ts = append(ts, trips[i])
			ms = append(ms, metas[i])
		}
	}
	var out []fit.Judgment
	for _, j := range js {
		if k := idx[j.Triplet]; k >= 0 {
			j.Triplet = k
			out = append(out, j)
		}
	}
	return ts, out, ms
}

func delta(src ged.Graph, va, vb perturb.Variant, sa, sb ged.Sim, th ged.Theta) (float64, error) {
	return deltaG(src, va.Graph, vb.Graph, sa, sb, th)
}

func deltaG(src, a, b ged.Graph, sa, sb ged.Sim, th ged.Theta) (float64, error) {
	fa, err := ged.Compute(src, a, sa, th)
	if err != nil {
		return 0, err
	}
	fb, err := ged.Compute(src, b, sb, th)
	if err != nil {
		return 0, err
	}
	return fb.Cost(th) - fa.Cost(th), nil
}
