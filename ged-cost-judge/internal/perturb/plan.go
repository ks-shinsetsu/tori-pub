package perturb

import (
	"math"
	"math/rand/v2"
	"slices"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
)

// TripletType is the kind of triplet (plan §4.3, dataset-spec §3).
type TripletType string

const (
	T1Monotone  TripletType = "T1" // A's edits ⊂ B's edits (B = A plus extra edits)
	T2SameKind  TripletType = "T2" // same kind, differing only in amount or strength
	T3Contrast  TripletType = "T3" // edits of different kinds
	T4Invariant TripletType = "T4" // pairs expected at Δ = 0
)

// DrawType picks a triplet type with proportions 10 / 10 / 70 / 10%.
func DrawType(rng *rand.Rand) TripletType {
	switch r := rng.Float64(); {
	case r < 0.1:
		return T1Monotone
	case r < 0.2:
		return T2SameKind
	case r < 0.9:
		return T3Contrast
	default:
		return T4Invariant
	}
}

// KindWeight is an edit kind with its draw weight.
type KindWeight struct {
	Kind Kind
	W    float64
}

// graphKinds are the estimation edit kinds and weights used in the ordinary T3 draws (dataset-spec §2.2).
// X-spk is T4 only; the diagnostic CA relabel (RTypeCA) is drawn only in the t3_diag sub-stratum, never here.
var graphKinds = []KindWeight{
	{RDel, 0.12}, {RAdd, 0.11}, {RType, 0.10}, {RRev, 0.10},
	{PPara, 0.08}, {PShift, 0.13}, {PNeg, 0.05}, {PAdd, 0.13}, {PDel, 0.06}, {PDelR, 0.10},
	{XSurf, 0.02},
}

// KindWeights returns a copy of the T3 kind weights (dataset-spec §2.2).
func KindWeights() []KindWeight { return slices.Clone(graphKinds) }

// T3Sub is the sub-stratum of a T3 slot (dataset-spec §2.2): ordinary, t3_ident or t3_diag.
type T3Sub string

const (
	T3Plain T3Sub = ""      // ordinary T3 (near / clear Δ targeting applies)
	T3Ident T3Sub = "ident" // separate-identifiability cells (P-del-only / R-del-only)
	T3Diag  T3Sub = "diag"  // diagnostic CA relabel vs one estimation edit (excluded from the fit)
)

// Ident cells of the t3_ident sub-stratum (PlanInfo.T3IdentCell).
const (
	IdentNodeCell = "P-del" // node-only side with P-del vs edge side without R-del: ΔI-missing ≠ 0, Δedge-missing = 0
	IdentEdgeCell = "R-del" // edge-only side with R-del vs node side without P-del: Δedge-missing ≠ 0, ΔI-missing = 0
)

func restrict(ks ...Kind) []KindWeight {
	var out []KindWeight
	for _, x := range graphKinds {
		if slices.Contains(ks, x.Kind) {
			out = append(out, x)
		}
	}
	return out
}

var (
	// t1Kinds are the kinds allowed as T1 extra edits (dataset-spec §3.1 item 2), with the §2.2 weights.
	t1Kinds = restrict(RDel, RAdd, RRev, RType, PDel, PAdd, PDelR, PShift)
	// t1ShiftWeights restricts a T1 extra P-shift to medium / large.
	t1ShiftWeights = []float64{0, 0.3, 0.5}
	// identNode / identEdge are the node-only and edge-only sides of the t3_ident sub-stratum (dataset-spec §2.2);
	// identNodeKeep / identEdgeKeep are the same pools without the deleting kind, used on the side that must leave
	// the other cost's φ unchanged (P-del-only: node side vs identEdgeKeep; R-del-only: edge side vs identNodeKeep).
	identNode     = restrict(PDel, PAdd, PPara, XSurf)
	identEdge     = restrict(RDel, RAdd, RRev, RType)
	identNodeKeep = restrict(PAdd, PPara, XSurf)
	identEdgeKeep = restrict(RAdd, RRev, RType)
	// t4SameKinds are the kinds of the T4 "same kind elsewhere" cell (every graph kind except X-surf and the diagnostic relabel).
	t4SameKinds = restrict(RDel, RAdd, RType, RRev, PPara, PShift, PNeg, PAdd, PDel, PDelR)
	// t2Kinds are the T2 kinds (dataset-spec §3), drawn uniformly.
	t2Kinds = []Kind{PPara, PShift, PDelR, RDel, RType}
	// aKinds are the kinds of the shared edits A in T1 (the T3 estimation weights; the diagnostic relabel is never among them).
	aKinds = graphKinds
)

// drawFrom draws a kind from pool, skipping excluded kinds (the weights are renormalized over the rest).
func drawFrom(rng *rand.Rand, pool []KindWeight, excl func(Kind) bool) (Kind, bool) {
	ws := make([]float64, len(pool))
	for i, x := range pool {
		if excl == nil || !excl(x.Kind) {
			ws[i] = x.W
		}
	}
	i := pickWeighted(rng, ws)
	if i < 0 {
		return "", false
	}
	return pool[i].Kind, true
}

// banFor is the m7 co-occurrence ban (decisions-stats §15 item 5): P-add never in the same variant as P-del / P-del+R.
func banFor(kinds []Kind) func(Kind) bool {
	hasAdd := slices.Contains(kinds, PAdd)
	hasDel := slices.Contains(kinds, PDel) || slices.Contains(kinds, PDelR)
	return func(k Kind) bool {
		return (hasAdd && (k == PDel || k == PDelR)) || (hasDel && k == PAdd)
	}
}

// DelAddCooccur reports whether edits contain P-add together with P-del or P-del+R.
func DelAddCooccur(edits []Edit) bool {
	var add, del bool
	for _, e := range edits {
		switch e.Kind {
		case PAdd:
			add = true
		case PDel, PDelR:
			del = true
		}
	}
	return add && del
}

// drawKind draws a T3 kind.
func drawKind(rng *rand.Rand) Kind {
	k, _ := drawFrom(rng, graphKinds, nil)
	return k
}

// T4 cells (dataset-spec §3).
type T4Cell string

const (
	T4SurfSurf T4Cell = "surf/surf" // X-surf vs X-surf
	T4SameKind T4Cell = "same-kind" // same kind at different locations
	T4SurfSpk  T4Cell = "surf/spk"  // X-surf vs X-spk (excluded from the fit, RQ4)
	T4SpkSpk   T4Cell = "spk/spk"   // X-spk vs X-spk (excluded from the fit, RQ4)
)

var (
	t4Cells   = []T4Cell{T4SurfSurf, T4SameKind, T4SurfSpk, T4SpkSpk}
	t4Weights = []float64{0.4, 0.4, 0.1, 0.1}
)

// Plan draws the sequences of edit kinds for one triplet (for A and for B) without looking at a graph.
// In T1, A's sequence is the prefix of B's (calling Build with the same seed gives A ⊂ B).
// It is the kind-level view used by cmd/gcj/pilot*.go; DrawTriplet is the full catalogue (feasibility, strata, flags).
func Plan(rng *rand.Rand, typ TripletType) (a, b []Kind) {
	switch typ {
	case T1Monotone:
		n := 1 + rng.IntN(2)
		for range n {
			k, _ := drawFrom(rng, aKinds, banFor(a))
			a = append(a, k)
		}
		k, _ := drawFrom(rng, t1Kinds, banFor(a))
		b = append(slices.Clone(a), k)
		return a, b
	case T2SameKind:
		k := t2Kinds[rng.IntN(len(t2Kinds))]
		return []Kind{k}, []Kind{k, k}
	case T4Invariant:
		switch t4Cells[pickWeighted(rng, t4Weights)] {
		case T4SurfSurf:
			a, b = []Kind{XSurf}, []Kind{XSurf}
		case T4SameKind:
			k, _ := drawFrom(rng, t4SameKinds, nil)
			a, b = []Kind{k}, []Kind{k}
		case T4SurfSpk:
			a, b = []Kind{XSurf}, []Kind{XSpk}
		default:
			a, b = []Kind{XSpk}, []Kind{XSpk}
		}
		if rng.IntN(2) == 1 {
			a, b = b, a
		}
		return a, b
	default:
		na, nb := 1+rng.IntN(4), 1+rng.IntN(4)
		for range na {
			k, _ := drawFrom(rng, graphKinds, banFor(a))
			a = append(a, k)
		}
		for range nb {
			k, _ := drawFrom(rng, graphKinds, banFor(b))
			b = append(b, k)
		}
		return a, b
	}
}

// T1 Δ(θ₀) strata (dataset-spec §3.1 item 3).
const (
	T1Below = "<0.5"
	T1Low   = "[0.5,1)"
	T1Mid   = "[1,2)"
	T1High  = ">=2"
)

// T1Strata lists the three planned strata in order.
var T1Strata = []string{T1Low, T1Mid, T1High}

// T1StratumOf maps Δ(θ₀) to its stratum label.
func T1StratumOf(d float64) string {
	switch {
	case math.IsNaN(d) || d < 0.5:
		return T1Below
	case d < 1:
		return T1Low
	case d < 2:
		return T1Mid
	}
	return T1High
}

// t1ExtraCounts are the distributions of |B \ A| per planned stratum (dataset-spec §3.1 item 3: "one unit edit;
// one heavier edit or two; P-del+R with 2 edges or three edits").
var t1ExtraCounts = [3][]float64{{1}, {0.7, 0.3}, {0.3, 0.4, 0.3}}

// PlanInfo is the per-triplet plan metadata recorded with the triplet (dataset-spec §6, decisions-stats §15 item 7).
type PlanInfo struct {
	Type TripletType `json:"type"`
	// T3Ident marks the T3 identifiability sub-stratum; T3IdentCell is its cell (IdentNodeCell: node-only side with
	// P-del vs edge side without R-del; IdentEdgeCell: edge-only side with R-del vs node side without P-del).
	T3Ident     bool   `json:"t3_ident,omitempty"`
	T3IdentCell string `json:"t3_ident_cell,omitempty"`
	// T3Diag marks the T3 diagnostic sub-stratum (one CA-involving relabel vs one estimation edit; implies DiagCAType).
	T3Diag bool `json:"t3_diag,omitempty"`
	// XSpk marks a triplet with an X-spk edit (T4 X-spk cells; excluded from the fit).
	XSpk bool `json:"x_spk,omitempty"`
	// DiagCAType marks a triplet with a diagnostic CA-involving relabel (excluded from the fit by default).
	DiagCAType bool `json:"diag_ca_type,omitempty"`
	// DelAddCooccur marks a side with P-add together with P-del / P-del+R (never produced by DrawTriplet; kept for the record).
	DelAddCooccur bool `json:"del_add_cooccur,omitempty"`
	// T4Cell is the T4 cell.
	T4Cell T4Cell `json:"t4_cell,omitempty"`
	// T1Target is the planned T1 stratum; T1Stratum is the stratum of T1Delta0 (re-stratified if it fell elsewhere).
	T1Target  string `json:"t1_target,omitempty"`
	T1Stratum string `json:"t1_stratum,omitempty"`
	// T1Delta0 is Δ(θ₀) of the extra edits on the frozen graphs with the predicted cos (T1 only).
	T1Delta0 float64 `json:"t1_delta0,omitempty"`
	// T1Extra is |B \ A| (T1 only).
	T1Extra int `json:"t1_extra,omitempty"`
	// PredDelta0 is the predicted Δ = GED(S, B) − GED(S, A) under θ₀ (predicted cos), every type.
	PredDelta0 float64 `json:"pred_delta0"`
	// EditsA / EditsB are the per-side edit counts.
	EditsA int `json:"n_edits_a"`
	EditsB int `json:"n_edits_b"`
	// Swapped reports that the sides were randomized and the constructed first side became B.
	Swapped bool `json:"swapped,omitempty"`
	// Redraws counts T1 stratum redraws and T4 same-target redraws.
	Redraws int `json:"redraws,omitempty"`
	// Infeasible lists kinds that were infeasible on the graph at their step and were redrawn (Kind + reason).
	Infeasible []Edit `json:"infeasible,omitempty"`
}

// TripletPlan is a drawn triplet: the two frozen variants and the plan metadata.
type TripletPlan struct {
	PlanInfo
	A, B Variant
}

// side builds one variant edit by edit.
type side struct {
	s          *state
	kinds      []Kind
	edits      []Edit
	infeasible []Edit
}

func newSide(src ged.Graph, o *BuildOptions) *side { return &side{s: newState(src, o)} }

func (b *side) clone() *side {
	return &side{s: b.s.clone(), kinds: slices.Clone(b.kinds), edits: slices.Clone(b.edits), infeasible: slices.Clone(b.infeasible)}
}

func (b *side) add(rng *rand.Rand, k Kind, w want) bool {
	e, ok, why := b.s.draw(rng, k, w)
	if !ok {
		b.infeasible = append(b.infeasible, Edit{Kind: k, Infeasible: why})
		return false
	}
	b.kinds = append(b.kinds, k)
	b.edits = append(b.edits, e)
	return true
}

// addFrom draws a kind from pool under the m7 ban; a kind infeasible on the current graph is excluded and the kind redrawn.
func (b *side) addFrom(rng *rand.Rand, pool []KindWeight, w want) bool {
	ban := banFor(b.kinds)
	tried := map[Kind]bool{}
	for {
		k, ok := drawFrom(rng, pool, func(k Kind) bool { return tried[k] || ban(k) })
		if !ok {
			return false
		}
		if b.add(rng, k, w) {
			return true
		}
		tried[k] = true
	}
}

func (b *side) variant() Variant { return snapshot(b.s.src, b.s.g, b.edits) }

// DrawTriplet draws a full plan of type typ on src (dataset-spec §2–§3):
//
//   - T1: A = 1–2 edits; B = A plus 1–T1MaxExtra extra edits of the §3.1 kinds, drawn into the Δ(θ₀) strata
//     [0.5,1) / [1,2) / ≥2 (redrawn until the planned stratum is hit; re-stratified if 200 draws miss it).
//   - T2: one edit vs two edits of the same kind (R-type: same direction).
//   - T3: 1–4 edits per side with the §2.2 weights and the m7 ban; IdentShare of T3 is the t3_ident sub-stratum
//     (half P-del-only, half R-del-only cells) and DiagShare the t3_diag sub-stratum (one CA relabel vs one estimation edit).
//   - T4: the four cells of §3, side randomized; "same kind" uses the same strength / direction / degree at another target.
func DrawTriplet(rng *rand.Rand, src ged.Graph, typ TripletType, opts BuildOptions) TripletPlan {
	o := opts.norm()
	var tp TripletPlan
	tp.Type = typ
	var a, b *side
	switch typ {
	case T1Monotone:
		a, b = drawT1(rng, src, &o, &tp.PlanInfo)
	case T2SameKind:
		a, b = drawT2(rng, src, &o)
	case T4Invariant:
		a, b = drawT4(rng, src, &o, &tp.PlanInfo)
	default:
		a, b = drawT3(rng, src, &o, &tp.PlanInfo)
	}
	if tp.Swapped {
		a, b = b, a
	}
	tp.A, tp.B = a.variant(), b.variant()
	tp.EditsA, tp.EditsB = len(tp.A.Edits), len(tp.B.Edits)
	tp.Infeasible = append(slices.Clone(a.infeasible), b.infeasible...)
	for _, e := range append(slices.Clone(tp.A.Edits), tp.B.Edits...) {
		switch e.Kind {
		case XSpk:
			tp.XSpk = true
		case RTypeCA:
			tp.DiagCAType = true
		}
	}
	tp.DelAddCooccur = DelAddCooccur(tp.A.Edits) || DelAddCooccur(tp.B.Edits)
	if typ == T1Monotone {
		tp.PredDelta0 = tp.T1Delta0
	} else if d, err := Delta0(src, tp.A, tp.B, *o.Theta0, o.CosPred); err == nil {
		tp.PredDelta0 = d
	} else {
		tp.PredDelta0 = math.NaN()
	}
	return tp
}

func drawT3(rng *rand.Rand, src ged.Graph, o *BuildOptions, info *PlanInfo) (a, b *side) {
	sub := T3Plain
	if o.Sub != nil {
		sub = *o.Sub
	} else {
		sub = o.DrawSub(rng)
	}
	switch sub {
	case T3Ident:
		// Half the slots are the P-del-only cell, half the R-del-only cell (dataset-spec §2.2, decisions-stats §15 item 4).
		del, keep, cell, first := identNode, identEdgeKeep, IdentNodeCell, PDel
		if rng.IntN(2) == 1 {
			del, keep, cell, first = identEdge, identNodeKeep, IdentEdgeCell, RDel
		}
		x, y := newSide(src, o), newSide(src, o)
		if x.add(rng, first, want{}) {
			nx, ny := 1+rng.IntN(4), 1+rng.IntN(4)
			for len(x.edits) < nx && x.addFrom(rng, del, want{}) {
			}
			for len(y.edits) < ny && y.addFrom(rng, keep, want{}) {
			}
			if len(y.edits) > 0 {
				info.T3Ident, info.T3IdentCell = true, cell
				info.Swapped = rng.IntN(2) == 1
				return x, y
			}
		}
		// infeasible on this source (no isolated proposition / no edge): an ordinary T3 slot instead.
	case T3Diag:
		// One CA-involving relabel against one estimation edit (sides randomized); flagged diag_ca_type via the edit.
		ca, ord := newSide(src, o), newSide(src, o)
		if ca.add(rng, RTypeCA, want{}) && ord.addFrom(rng, graphKinds, want{}) {
			info.T3Diag = true
			info.Swapped = rng.IntN(2) == 1
			return ca, ord
		}
		// no relabelable edge (or nothing else feasible): an ordinary T3 slot instead.
	}
	a, b = newSide(src, o), newSide(src, o)
	na, nb := 1+rng.IntN(4), 1+rng.IntN(4)
	for len(a.edits) < na && a.addFrom(rng, graphKinds, want{}) {
	}
	for len(b.edits) < nb && b.addFrom(rng, graphKinds, want{}) {
	}
	return a, b
}

func drawT2(rng *rand.Rand, src ged.Graph, o *BuildOptions) (a, b *side) {
	ks := slices.Clone(t2Kinds)
	for len(ks) > 0 {
		i := rng.IntN(len(ks))
		k := ks[i]
		a, b = newSide(src, o), newSide(src, o)
		if a.add(rng, k, want{}) {
			w := want{}
			if k == RType {
				w.direction = a.edits[0].Direction
			}
			b.add(rng, k, w)
			b.add(rng, k, w)
			if len(b.edits) > 0 {
				return a, b
			}
		}
		ks = slices.Delete(ks, i, i+1)
	}
	return newSide(src, o), newSide(src, o)
}

func drawT4(rng *rand.Rand, src ged.Graph, o *BuildOptions, info *PlanInfo) (a, b *side) {
	info.T4Cell = t4Cells[pickWeighted(rng, t4Weights)]
	info.Swapped = rng.IntN(2) == 1
	a, b = newSide(src, o), newSide(src, o)
	switch info.T4Cell {
	case T4SurfSurf:
		a.add(rng, XSurf, want{})
		b.add(rng, XSurf, want{})
	case T4SurfSpk:
		a.add(rng, XSurf, want{})
		b.add(rng, XSpk, want{})
	case T4SpkSpk:
		a.add(rng, XSpk, want{})
		b.add(rng, XSpk, want{})
	default:
		tried := map[Kind]bool{}
		for {
			k, ok := drawFrom(rng, t4SameKinds, func(k Kind) bool { return tried[k] })
			if !ok {
				// no graph kind applies twice on this source: fall back to X-surf vs X-surf.
				info.T4Cell = T4SurfSurf
				a, b = newSide(src, o), newSide(src, o)
				a.add(rng, XSurf, want{})
				b.add(rng, XSurf, want{})
				return a, b
			}
			a, b = newSide(src, o), newSide(src, o)
			if a.add(rng, k, want{}) {
				e := a.edits[0]
				w := want{strength: e.Strength, direction: e.Direction, avoid: map[string]bool{}}
				if k != PAdd {
					w.avoid[targetKey(e)] = true
				}
				if k == PDelR {
					w.degree = e.EdgesRemoved
				}
				if b.add(rng, k, w) {
					return a, b
				}
				info.Redraws++
			}
			tried[k] = true
		}
	}
	return a, b
}

func drawT1(rng *rand.Rand, src ged.Graph, o *BuildOptions, info *PlanInfo) (a, b *side) {
	a = newSide(src, o)
	n := 1 + rng.IntN(2)
	for len(a.edits) < n && a.addFrom(rng, aKinds, want{}) {
	}
	va := a.variant()
	fa, errA := ged.Compute(src, va.Graph, PredSim(va, o.CosPred), *o.Theta0)
	target := pickWeighted(rng, o.T1Shares[:])
	info.T1Target = T1Strata[target]
	counts := t1ExtraCounts[target]
	if len(counts) > o.T1MaxExtra {
		counts = counts[:o.T1MaxExtra]
	}
	var fallback *side
	var fbDelta float64
	const attempts = 200
	for t := range attempts {
		bb := a.clone()
		bb.infeasible = nil
		m := 1 + pickWeighted(rng, counts)
		for len(bb.edits) < len(a.edits)+m && bb.addFrom(rng, t1Kinds, want{shiftW: t1ShiftWeights}) {
		}
		extra := len(bb.edits) - len(a.edits)
		if extra == 0 {
			continue
		}
		d := math.NaN()
		if errA == nil {
			vb := bb.variant()
			if fb, err := ged.Compute(src, vb.Graph, PredSim(vb, o.CosPred), *o.Theta0); err == nil {
				d = fb.Cost(*o.Theta0) - fa.Cost(*o.Theta0)
			}
		}
		if T1StratumOf(d) == info.T1Target {
			info.T1Delta0, info.T1Stratum, info.T1Extra, info.Redraws = d, info.T1Target, extra, t
			return a, bb
		}
		if fallback == nil && d >= 0.5 || fallback == nil && t == attempts-1 {
			fallback, fbDelta = bb, d
		}
	}
	if fallback == nil {
		fallback = a.clone()
	}
	info.T1Delta0, info.T1Stratum, info.T1Extra, info.Redraws = fbDelta, T1StratumOf(fbDelta), len(fallback.edits)-len(a.edits), attempts
	return a, fallback
}

// AssignSurface gives the graph-invariant edits of tp their place in the text: every X-surf edit gets the
// utterance it rewords and every X-spk edit the pair of speakers it swaps, different on the two sides. Without
// this both sides of an X-surf vs X-surf or X-spk vs X-spk triplet carry the same edit list, the realization
// requests are identical, and the two variants come out as the same text (dataset-spec §3: T4 compares two
// different graph-invariant variants). utterances and speakers are the source's counts (speakers = distinct
// names); utt maps a proposition to its utterance, so that X-surf avoids an utterance the side's other edits
// rewrite where it can.
//
// An X-spk that has no pair left becomes an X-surf and the T4 cell is renamed: with fewer than two speakers
// every X-spk, with exactly two speakers one side of X-spk vs X-spk (chosen at random). The conversions are
// listed in tp.Infeasible. It reports false when the source has fewer utterances than the plan has X-surf
// edits; the plan cannot be realized as two different texts and the caller discards it.
//
// It is a separate step, with the caller's rng, because the graph side of the plan does not depend on it: the
// simulator never calls it, so the simulated datasets of decisions-stats Appendix A.5 are unchanged.
func AssignSurface(rng *rand.Rand, tp *TripletPlan, utterances, speakers int, utt map[string]int) bool {
	// In T1, B's first len(A) edits are A's (B is realized from A's text): assign A and copy.
	shared := 0
	if tp.Type == T1Monotone {
		shared = len(tp.A.Edits)
	}
	sides := [2]*Variant{&tp.A, &tp.B}
	count := func(k Kind) (n [2]int) {
		for s, v := range sides {
			for i, e := range v.Edits {
				if e.Kind == k && !(s == 1 && i < shared) {
					n[s]++
				}
			}
		}
		return n
	}
	toSurf := func(s int, why string) {
		for i := range sides[s].Edits {
			if e := &sides[s].Edits[i]; e.Kind == XSpk {
				tp.Infeasible = append(tp.Infeasible, Edit{Kind: XSpk, Infeasible: why})
				*e = Edit{Kind: XSurf}
			}
		}
	}
	if n := count(XSpk); n[0]+n[1] > 0 {
		switch {
		case speakers < 2:
			toSurf(0, "fewer than two speakers")
			toSurf(1, "fewer than two speakers")
		case speakers == 2 && n[0] > 0 && n[1] > 0:
			toSurf(rng.IntN(2), "one pair of speakers, used by the other side")
		}
		n = count(XSpk)
		tp.XSpk = n[0]+n[1] > 0
		if tp.Type == T4Invariant && (tp.T4Cell == T4SurfSpk || tp.T4Cell == T4SpkSpk) {
			switch {
			case n[0] > 0 && n[1] > 0:
				tp.T4Cell = T4SpkSpk
			case tp.XSpk:
				tp.T4Cell = T4SurfSpk
			default:
				tp.T4Cell = T4SurfSurf
			}
		}
		var pairs [][]int
		for i := range speakers {
			for j := i + 1; j < speakers; j++ {
				pairs = append(pairs, []int{i, j})
			}
		}
		for s, v := range sides {
			for i := range v.Edits {
				if e := &v.Edits[i]; e.Kind == XSpk && !(s == 1 && i < shared) {
					k := rng.IntN(len(pairs))
					e.Speakers = pairs[k]
					pairs = slices.Delete(pairs, k, k+1)
				}
			}
		}
	}

	used := map[int]bool{}
	for s, v := range sides {
		busy := map[int]bool{} // utterances the side's other edits rewrite
		for _, e := range v.Edits {
			if u, ok := utt[e.Prop]; ok {
				busy[u] = true
			}
			if e.Edge != nil {
				for _, id := range []string{e.Edge.From, e.Edge.To} {
					if u, ok := utt[id]; ok {
						busy[u] = true
					}
				}
			}
		}
		for i := range v.Edits {
			e := &v.Edits[i]
			if e.Kind != XSurf || (s == 1 && i < shared) {
				continue
			}
			var free, any []int
			for u := range utterances {
				if used[u] {
					continue
				}
				any = append(any, u)
				if !busy[u] {
					free = append(free, u)
				}
			}
			if len(free) == 0 {
				free = any
			}
			if len(free) == 0 {
				return false
			}
			u := free[rng.IntN(len(free))]
			used[u] = true
			e.Utterance = &u
		}
	}
	copy(tp.B.Edits[:min(shared, len(tp.B.Edits))], tp.A.Edits)
	return true
}
