package perturb

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
)

// testSource draws a QT30-like source: n propositions (one per utterance), a forest over the non-isolated ones
// with RA / MA / CA edges from later to earlier propositions, and ≥ 2 isolated propositions.
func testSource(rng *rand.Rand, n int) ged.Graph {
	var g ged.Graph
	for i := range n {
		g.Props = append(g.Props, ged.Prop{ID: fmt.Sprintf("p%d", i)})
	}
	iso := map[int]bool{}
	for _, i := range rng.Perm(n)[:2+rng.IntN(2)] {
		iso[i] = true
	}
	var conn []int
	for i := range n {
		if !iso[i] {
			conn = append(conn, i)
		}
	}
	for k := 1; k < len(conn); k++ {
		if rng.Float64() < 0.2 {
			continue
		}
		j := conn[max(0, k-1-rng.IntN(3))]
		kind := []string{ged.RA, ged.RA, ged.MA, ged.CA}[rng.IntN(4)]
		g.Edges = append(g.Edges, ged.Edge{From: g.Props[conn[k]].ID, To: g.Props[j].ID, Kind: kind})
	}
	return g
}

func pairCos(a, b string) float64 {
	// deterministic pseudo-cos in [0.1, 0.6]
	if a > b {
		a, b = b, a
	}
	h := 0
	for _, c := range a + "|" + b {
		h = h*31 + int(c)
	}
	return 0.1 + 0.5*float64(uint(h)%1000)/1000
}

func TestWeightsSumToOne(t *testing.T) {
	sum := 0.0
	for _, x := range KindWeights() {
		sum += x.W
		if x.Kind == XSpk {
			t.Errorf("X-spk must not be in the T3 weights")
		}
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Fatalf("weights sum to %v", sum)
	}
	want := map[Kind]float64{RDel: .12, RAdd: .11, RType: .10, RRev: .10, PPara: .08, PShift: .13,
		PNeg: .05, PAdd: .13, PDel: .06, PDelR: .10, XSurf: .02}
	if len(KindWeights()) != len(want) {
		t.Errorf("%d T3 kinds, want %d (the diagnostic relabel is drawn only in t3_diag)", len(KindWeights()), len(want))
	}
	for _, x := range KindWeights() {
		if want[x.Kind] != x.W {
			t.Errorf("%s: weight %v, dataset-spec §2.2 says %v", x.Kind, x.W, want[x.Kind])
		}
	}
}

// drawMany applies a single edit of kind on many random sources.
func drawMany(t *testing.T, kind Kind, n int, check func(src ged.Graph, e Edit, v Variant)) {
	t.Helper()
	rng := rand.New(rand.NewPCG(7, 1))
	got := 0
	for range n {
		src := testSource(rng, 10+rng.IntN(10))
		steps := BuildWith(rng, src, []Kind{kind}, BuildOptions{Cos: pairCos})
		v := steps[len(steps)-1]
		if len(v.Edits) == 0 {
			continue
		}
		got++
		check(src, v.Edits[0], v)
	}
	if got < n/2 {
		t.Fatalf("%s applied only %d of %d times", kind, got, n)
	}
}

func TestRTypeNeverCA(t *testing.T) {
	drawMany(t, RType, 2000, func(_ ged.Graph, e Edit, _ Variant) {
		if e.Edge.Kind == ged.CA || e.NewKind == ged.CA || e.Edge.Kind == e.NewKind {
			t.Fatalf("R-type %s → %s", e.Edge.Kind, e.NewKind)
		}
		if e.Direction != DirectionLabel(e.Edge.Kind, e.NewKind) {
			t.Fatalf("direction %q", e.Direction)
		}
	})
}

func TestRTypeCAInvolvesCA(t *testing.T) {
	dirs := map[string]int{}
	drawMany(t, RTypeCA, 4000, func(_ ged.Graph, e Edit, _ Variant) {
		if e.Edge.Kind != ged.CA && e.NewKind != ged.CA || e.Edge.Kind == e.NewKind {
			t.Fatalf("R-type-CA %s → %s", e.Edge.Kind, e.NewKind)
		}
		dirs[e.Direction]++
	})
	if len(dirs) != 4 {
		t.Fatalf("directions %v, want all four CA-involving ones", dirs)
	}
}

func TestRRevOnlyRA(t *testing.T) {
	drawMany(t, RRev, 2000, func(_ ged.Graph, e Edit, v Variant) {
		if e.Edge.Kind != ged.RA {
			t.Fatalf("R-rev of a %s edge", e.Edge.Kind)
		}
		if !slices.Contains(v.Graph.Edges, ged.Edge{From: e.Edge.To, To: e.Edge.From, Kind: ged.RA}) {
			t.Fatalf("reversed edge missing")
		}
	})
}

func TestRAdd(t *testing.T) {
	var strata [3]int
	drawMany(t, RAdd, 6000, func(src ged.Graph, e Edit, _ Variant) {
		a, b := e.Edge.From, e.Edge.To
		nb := map[string]map[string]bool{}
		for _, x := range src.Edges {
			if nb[x.From] == nil {
				nb[x.From] = map[string]bool{}
			}
			if nb[x.To] == nil {
				nb[x.To] = map[string]bool{}
			}
			nb[x.From][x.To], nb[x.To][x.From] = true, true
		}
		if nb[a][b] {
			t.Fatalf("R-add duplicates an edge %s–%s", a, b)
		}
		for c := range nb[a] {
			if nb[b][c] {
				t.Fatalf("R-add closes a 2-step path %s–%s–%s", a, c, b)
			}
		}
		if pairCos(a, b) < 0.35 {
			t.Fatalf("R-add pair cos %.2f < 0.35", pairCos(a, b))
		}
		if e.Distance == nil || AddDistStratum(*e.Distance) < 0 {
			t.Fatalf("R-add distance %v", e.Distance)
		}
		strata[AddDistStratum(*e.Distance)]++
		if e.Edge.Kind != ged.RA && propIndex(src, e.Edge.From) < propIndex(src, e.Edge.To) {
			t.Fatalf("R-add %s must run from the later to the earlier utterance", e.Edge.Kind)
		}
	})
	tot := float64(strata[0] + strata[1] + strata[2])
	for k, want := range []float64{0.45, 0.45, 0.10} {
		if got := float64(strata[k]) / tot; math.Abs(got-want) > 0.03 {
			t.Errorf("stratum %d share %.3f, want %.2f ± 0.03 (%v)", k, got, want, strata)
		}
	}
}

func propIndex(g ged.Graph, id string) int {
	return slices.IndexFunc(g.Props, func(p ged.Prop) bool { return p.ID == id })
}

func drawPlans(seed uint64, n int, typ TripletType) []TripletPlan {
	ps, _ := drawPlansSrc(seed, n, typ)
	return ps
}

func drawPlansSrc(seed uint64, n int, typ TripletType) ([]TripletPlan, []ged.Graph) {
	rng := rand.New(rand.NewPCG(seed, 2))
	var out []TripletPlan
	var srcs []ged.Graph
	for range n {
		src := testSource(rng, 10+rng.IntN(10))
		ty := typ
		if ty == "" {
			ty = DrawType(rng)
		}
		out = append(out, DrawTriplet(rng, src, ty, BuildOptions{Cos: pairCos}))
		srcs = append(srcs, src)
	}
	return out, srcs
}

func kindsOf(es []Edit) []Kind {
	var ks []Kind
	for _, e := range es {
		ks = append(ks, e.Kind)
	}
	return ks
}

func TestNoDelAddCooccur(t *testing.T) {
	for _, p := range drawPlans(3, 10000, "") {
		if DelAddCooccur(p.A.Edits) || DelAddCooccur(p.B.Edits) || p.DelAddCooccur {
			t.Fatalf("%s: P-add with P-del in one side: %v | %v", p.Type, kindsOf(p.A.Edits), kindsOf(p.B.Edits))
		}
	}
}

func TestT3(t *testing.T) {
	ps := drawPlans(4, 10000, T3Contrast)
	ident, diag := 0, 0
	cells := map[string]int{}
	for _, p := range ps {
		for _, n := range []int{p.EditsA, p.EditsB} {
			if n < 1 || n > 4 {
				t.Fatalf("T3 side with %d edits", n)
			}
		}
		if p.EditsA != len(p.A.Edits) || p.EditsB != len(p.B.Edits) {
			t.Fatalf("edit counts out of sync")
		}
		hasCA := false
		for _, e := range append(slices.Clone(p.A.Edits), p.B.Edits...) {
			if e.Kind == XSpk {
				t.Fatalf("X-spk in T3")
			}
			hasCA = hasCA || e.Kind == RTypeCA
		}
		if hasCA != p.T3Diag || hasCA != p.DiagCAType {
			t.Fatalf("CA relabel outside t3_diag or flags out of sync: diag=%v flag=%v %v | %v", p.T3Diag, p.DiagCAType, kindsOf(p.A.Edits), kindsOf(p.B.Edits))
		}
		if p.T3Diag {
			diag++
			ca, ord := p.A, p.B
			if p.Swapped {
				ca, ord = ord, ca
			}
			if len(ca.Edits) != 1 || ca.Edits[0].Kind != RTypeCA || len(ord.Edits) != 1 || ord.Edits[0].Kind == RTypeCA {
				t.Fatalf("t3_diag is not one CA relabel vs one estimation edit: %v | %v", kindsOf(ca.Edits), kindsOf(ord.Edits))
			}
			continue
		}
		if !p.T3Ident {
			continue
		}
		ident++
		cells[p.T3IdentCell]++
		x, y := p.A, p.B
		if p.Swapped {
			x, y = y, x
		}
		xk, yk := kindsOf(x.Edits), kindsOf(y.Edits)
		del, delPool, keepPool := PDel, []Kind{PDel, PAdd, PPara, XSurf}, []Kind{RAdd, RRev, RType}
		if p.T3IdentCell == IdentEdgeCell {
			del, delPool, keepPool = RDel, []Kind{RDel, RAdd, RRev, RType}, []Kind{PAdd, PPara, XSurf}
		} else if p.T3IdentCell != IdentNodeCell {
			t.Fatalf("t3_ident cell %q", p.T3IdentCell)
		}
		if !slices.Contains(xk, del) {
			t.Fatalf("t3_ident %s cell without %s: %v | %v", p.T3IdentCell, del, xk, yk)
		}
		for _, k := range xk {
			if !slices.Contains(delPool, k) {
				t.Fatalf("t3_ident %s cell deleting side has %s", p.T3IdentCell, k)
			}
		}
		for _, k := range yk {
			if !slices.Contains(keepPool, k) {
				t.Fatalf("t3_ident %s cell other side has %s", p.T3IdentCell, k)
			}
		}
	}
	if share := float64(ident) / float64(len(ps)); math.Abs(share-0.10) > 0.02 {
		t.Fatalf("t3_ident share %.3f, want 0.10 ± 0.02", share)
	}
	if share := float64(diag) / float64(len(ps)); math.Abs(share-0.10) > 0.02 {
		t.Fatalf("t3_diag share %.3f, want 0.10 ± 0.02", share)
	}
	if lo, hi := cells[IdentNodeCell], cells[IdentEdgeCell]; math.Abs(float64(lo-hi))/float64(ident) > 0.15 {
		t.Fatalf("t3_ident cells %d / %d, want ≈ half each", lo, hi)
	}
}

func TestT1Incremental(t *testing.T) {
	allowed := []Kind{RDel, RAdd, RRev, RType, PDel, PAdd, PDelR, PShift}
	strata := map[string]int{}
	ps, srcs := drawPlansSrc(5, 1500, T1Monotone)
	for i, p := range ps {
		na := len(p.A.Edits)
		if na < 1 || len(p.B.Edits) <= na {
			t.Fatalf("T1: |A| = %d, |B| = %d", na, len(p.B.Edits))
		}
		a, _ := json.Marshal(p.A.Edits)
		b, _ := json.Marshal(p.B.Edits[:na])
		if string(a) != string(b) {
			t.Fatalf("T1: A is not a prefix of B")
		}
		extra := p.B.Edits[na:]
		if len(extra) != p.T1Extra || len(extra) > 3 {
			t.Fatalf("T1: %d extra edits (recorded %d)", len(extra), p.T1Extra)
		}
		for _, e := range extra {
			if !slices.Contains(allowed, e.Kind) {
				t.Fatalf("T1 extra edit of kind %s", e.Kind)
			}
			if e.Kind == PShift && e.Strength == "small" {
				t.Fatalf("T1 extra P-shift small")
			}
		}
		d, err := Delta0(srcs[i], p.A, p.B, ged.Current(), nil)
		if err != nil || math.Abs(d-p.T1Delta0) > 1e-9 {
			t.Fatalf("T1Delta0 %v, recomputed %v", p.T1Delta0, d)
		}
		if p.T1Stratum != T1StratumOf(p.T1Delta0) {
			t.Fatalf("stratum %s for Δ %v", p.T1Stratum, p.T1Delta0)
		}
		strata[p.T1Stratum]++
	}
	for k, want := range map[string]float64{T1Low: 95. / 300, T1Mid: 95. / 300, T1High: 110. / 300} {
		if got := float64(strata[k]) / float64(len(ps)); math.Abs(got-want) > 0.05 {
			t.Errorf("T1 stratum %s share %.3f, want %.3f (%v)", k, got, want, strata)
		}
	}
}

func TestT4Mix(t *testing.T) {
	rng := rand.New(rand.NewPCG(6, 3))
	cells := map[T4Cell]int{}
	swapped := 0
	const n = 10000
	for range n {
		src := testSource(rng, 10+rng.IntN(10))
		p := DrawTriplet(rng, src, T4Invariant, BuildOptions{Cos: pairCos})
		cells[p.T4Cell]++
		if p.Swapped {
			swapped++
		}
		if len(p.A.Edits) != 1 || len(p.B.Edits) != 1 {
			t.Fatalf("T4 with %d / %d edits", len(p.A.Edits), len(p.B.Edits))
		}
		ea, eb := p.A.Edits[0], p.B.Edits[0]
		hasSpk := ea.Kind == XSpk || eb.Kind == XSpk
		if hasSpk != p.XSpk || hasSpk != (p.T4Cell == T4SurfSpk || p.T4Cell == T4SpkSpk) {
			t.Fatalf("x_spk flag %v for cell %s", p.XSpk, p.T4Cell)
		}
		if p.T4Cell == T4SameKind {
			if ea.Kind != eb.Kind || ea.Kind == XSurf || ea.Kind == XSpk || ea.Kind == RTypeCA {
				t.Fatalf("same-kind cell with %s / %s", ea.Kind, eb.Kind)
			}
			if ea.Kind != PAdd && targetKey(ea) == targetKey(eb) {
				t.Fatalf("same-kind cell at the same target %s", targetKey(ea))
			}
			if ea.Strength != eb.Strength || ea.Direction != eb.Direction && ea.Kind == RType {
				t.Fatalf("same-kind cell with different strength / direction")
			}
		}
	}
	for c, want := range map[T4Cell]float64{T4SurfSurf: .4, T4SameKind: .4, T4SurfSpk: .1, T4SpkSpk: .1} {
		if got := float64(cells[c]) / n; math.Abs(got-want) > 0.02 {
			t.Errorf("T4 cell %s share %.3f, want %.2f", c, got, want)
		}
	}
	if s := float64(swapped) / n; math.Abs(s-0.5) > 0.03 {
		t.Errorf("T4 side swapped in %.3f", s)
	}
}

func TestDeterminism(t *testing.T) {
	a, _ := json.Marshal(drawPlans(11, 400, ""))
	b, _ := json.Marshal(drawPlans(11, 400, ""))
	if string(a) != string(b) {
		t.Fatal("plans differ under the same seed")
	}
	c, _ := json.Marshal(drawPlans(12, 400, ""))
	if string(a) == string(c) {
		t.Fatal("plans identical under different seeds")
	}
}

func TestPlanKindLevel(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 9))
	for range 5000 {
		typ := DrawType(rng)
		a, b := Plan(rng, typ)
		if len(a) == 0 || len(b) == 0 {
			t.Fatalf("%s: empty side", typ)
		}
		switch typ {
		case T1Monotone:
			if len(b) != len(a)+1 || !slices.Equal(a, b[:len(a)]) {
				t.Fatalf("T1 kinds %v / %v", a, b)
			}
		case T3Contrast:
			if len(a) > 4 || len(b) > 4 || slices.Contains(a, XSpk) || slices.Contains(b, XSpk) {
				t.Fatalf("T3 kinds %v / %v", a, b)
			}
		}
		for _, s := range [][]Kind{a, b} {
			if slices.Contains(s, PAdd) && (slices.Contains(s, PDel) || slices.Contains(s, PDelR)) {
				t.Fatalf("%s: P-add with P-del in %v", typ, s)
			}
		}
	}
	// the legacy entry point still works
	src := testSource(rng, 12)
	if steps := Build(rng, src, []Kind{RDel, PPara}); len(steps) != 2 {
		t.Fatalf("Build returned %d steps", len(steps))
	}
}

// TestAssignSurface: the two sides of a triplet never carry the same graph-invariant edit. X-surf edits get
// different utterances, X-spk edits different speaker pairs, and an X-spk without a pair left becomes an X-surf.
func TestAssignSurface(t *testing.T) {
	plans, srcs := drawPlansSrc(11, 4000, T4Invariant)
	t3, srcs3 := drawPlansSrc(12, 2000, T3Contrast)
	t1, srcs1 := drawPlansSrc(13, 1000, T1Monotone)
	plans, srcs = append(append(plans, t3...), t1...), append(append(srcs, srcs3...), srcs1...)
	rng := rand.New(rand.NewPCG(5, 5))
	cells := map[T4Cell]int{}
	for i, tp := range plans {
		speakers := 1 + i%4
		n := len(srcs[i].Props) // testSource: one proposition per utterance
		utt := map[string]int{}
		for j, p := range srcs[i].Props {
			utt[p.ID] = j
		}
		before := tp.T4Cell
		if !AssignSurface(rng, &tp, n, speakers, utt) {
			t.Fatalf("plan %d: no utterance left on a source with %d utterances", i, n)
		}
		seenU, seenP := map[int]bool{}, map[[2]int]bool{}
		spk := [2]int{}
		for s, v := range []Variant{tp.A, tp.B} {
			for k, e := range v.Edits {
				shared := tp.Type == T1Monotone && s == 1 && k < len(tp.A.Edits)
				switch e.Kind {
				case XSurf:
					if e.Utterance == nil || *e.Utterance < 0 || *e.Utterance >= n {
						t.Fatalf("plan %d: X-surf without a valid utterance: %+v", i, e)
					}
					if shared != seenU[*e.Utterance] {
						t.Fatalf("plan %d (%s): X-surf utterance %d, shared with A %v, already used %v", i, tp.Type, *e.Utterance, shared, seenU[*e.Utterance])
					}
					seenU[*e.Utterance] = true
				case XSpk:
					spk[s]++
					if len(e.Speakers) != 2 || e.Speakers[0] == e.Speakers[1] || e.Speakers[0] < 0 || e.Speakers[1] >= speakers {
						t.Fatalf("plan %d: X-spk pair %v with %d speakers", i, e.Speakers, speakers)
					}
					p := [2]int{e.Speakers[0], e.Speakers[1]}
					if seenP[p] {
						t.Fatalf("plan %d: both sides swap speakers %v", i, p)
					}
					seenP[p] = true
				}
			}
		}
		if tp.XSpk != (spk[0]+spk[1] > 0) || (speakers < 2 && tp.XSpk) || (speakers == 2 && spk[0] > 0 && spk[1] > 0) {
			t.Fatalf("plan %d: %d speakers, X-spk edits %v, flag %v", i, speakers, spk, tp.XSpk)
		}
		if tp.Type == T4Invariant {
			want := before
			switch {
			case before == T4SpkSpk && speakers == 2, before == T4SurfSpk && speakers < 2:
				want = T4SurfSpk
				if speakers < 2 {
					want = T4SurfSurf
				}
			case before == T4SpkSpk && speakers < 2:
				want = T4SurfSurf
			}
			if tp.T4Cell != want {
				t.Fatalf("plan %d: cell %s with %d speakers became %s, want %s", i, before, speakers, tp.T4Cell, want)
			}
			cells[tp.T4Cell]++
		}
		if tp.Type == T1Monotone && !slices.EqualFunc(tp.A.Edits, tp.B.Edits[:len(tp.A.Edits)], func(a, b Edit) bool {
			return a.Kind == b.Kind && (a.Utterance == nil) == (b.Utterance == nil) && (a.Utterance == nil || *a.Utterance == *b.Utterance)
		}) {
			t.Fatalf("plan %d: T1's B does not start with A's edits", i)
		}
	}
	for _, c := range []T4Cell{T4SurfSurf, T4SurfSpk, T4SpkSpk} {
		if cells[c] == 0 {
			t.Errorf("no %s plan left; the test does not cover it", c)
		}
	}
	// One utterance cannot host two different rewordings.
	for _, tp := range plans {
		if tp.T4Cell == T4SurfSurf {
			if AssignSurface(rng, &tp, 1, 3, nil) {
				t.Error("X-surf vs X-surf was placed on a one-utterance source")
			}
			break
		}
	}
}
