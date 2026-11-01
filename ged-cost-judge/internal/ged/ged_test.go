package ged

import (
	"math"
	"testing"
)

// The example from the slides: S = claim c, supported by p1 and attacked by p2.
func uniform() Graph {
	return Graph{
		Props: []Prop{{ID: "c"}, {ID: "p1"}, {ID: "p2"}},
		Edges: []Edge{{From: "p1", To: "c", Kind: RA}, {From: "p2", To: "c", Kind: CA}},
	}
}

func identity(a, b string) float64 {
	if a == b {
		return 1
	}
	return 0.2
}

func TestExampleFromSlides(t *testing.T) {
	s := uniform()
	th := Current()

	// A: remove reason p1 and the support arrow → missing proposition 1.0 + missing edge 1.0.
	a := Graph{Props: []Prop{{ID: "c"}, {ID: "p2"}}, Edges: []Edge{{From: "p2", To: "c", Kind: CA}}}
	fa, err := Compute(s, a, identity, th)
	if err != nil {
		t.Fatal(err)
	}
	if got := fa.Cost(th); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("A: GED = %v, want 2.0 (phi=%v)", got, fa.Phi)
	}

	// B: turn rebuttal p2 into support → type mismatch RA↔CA 0.75 (cheaper than removing and adding, 2.0).
	b := uniform()
	b.Edges[1].Kind = RA
	fb, err := Compute(s, b, identity, th)
	if err != nil {
		t.Fatal(err)
	}
	if got := fb.Cost(th); math.Abs(got-0.75) > 1e-9 || fb.Phi[PTypeRACA] != 1 {
		t.Fatalf("B: GED = %v, phi=%v", got, fb.Phi)
	}

	// Reversing direction is one direction mismatch.
	r := uniform()
	r.Edges[0] = Edge{From: "c", To: "p1", Kind: RA}
	fr, _ := Compute(s, r, identity, th)
	if fr.Phi[PDirection] != 1 || fr.Cost(th) != 1 {
		t.Fatalf("reverse: phi=%v", fr.Phi)
	}

	// Adding an unrelated edge is one extra.
	x := uniform()
	x.Edges = append(x.Edges, Edge{From: "p1", To: "p2", Kind: MA})
	fx, _ := Compute(s, x, identity, th)
	if fx.EdgeExtra != 1 || fx.Cost(th) != 1 {
		t.Fatalf("extra edge: %+v", fx)
	}
}

func TestSubstitutionIsOneMinusCosAboveKnot(t *testing.T) {
	th := Current()
	for _, c := range []float64{1, 0.95, 0.9, 0.8, 0.6, 0.45, 0.3} {
		if got, want := th.Sub(c), 1-c; math.Abs(got-want) > 1e-9 {
			t.Errorf("Sub(%v) = %v, want %v", c, got, want)
		}
	}
	if got := th.Sub(0.1); math.Abs(got-0.7) > 1e-9 {
		t.Errorf("Sub(0.1) = %v, want 0.7 (cos < 0.3 は一定)", got)
	}
}

// An exactly tied node assignment (two variant propositions below the last substitution knot competing for one
// reference proposition, whose edge costs differ) must be broken the same way at every θ and in every input order;
// otherwise the estimation loop sees a matching that flips with rounding noise (decisions §11.5).
func TestTieBreakIsStable(t *testing.T) {
	ref := Graph{Props: []Prop{{ID: "s.p0"}, {ID: "s.p1"}}, Edges: []Edge{{From: "s.p1", To: "s.p0", Kind: RA}}}
	// s.p1 is shifted far from its origin (cos 0.28) and n1 is an added, unrelated proposition (cos 0.2 to s.p1):
	// both substitutions cost Σ knots, so "s.p1 → s.p1, n1 extra" and "n1 → s.p1, s.p1 extra" tie on node cost.
	v := Graph{Props: []Prop{{ID: "s.p0"}, {ID: "s.p1"}, {ID: "n1"}}, Edges: []Edge{{From: "s.p1", To: "s.p0", Kind: RA}}}
	sim := func(r, a string) float64 {
		switch {
		case r == a:
			if r == "s.p1" {
				return 0.28
			}
			return 1
		case r == "s.p1" && a == "n1":
			return 0.2
		}
		return 0.15
	}
	th := Current()
	base, err := Compute(ref, v, sim, th)
	if err != nil {
		t.Fatal(err)
	}
	if base.Phi[PNodeExtra] != 1 || base.Phi[PSub03] != 1 {
		t.Fatalf("unexpected features %+v", base.Phi)
	}
	rev := Graph{Props: []Prop{v.Props[2], v.Props[1], v.Props[0]}, Edges: v.Edges}
	for k := range 200 {
		// Walk the knots and the unit costs by small amounts (the scale of one outer round) around θ₀.
		p := th
		for i := range p {
			p[i] += 0.01 * float64((k*7+i*3)%11-5) / 5
		}
		f, err := Compute(ref, v, sim, p)
		if err != nil {
			t.Fatal(err)
		}
		if f.Signature() != base.Signature() || f.Phi != base.Phi {
			t.Fatalf("θ = %v: matching %s (phi %v), want %s (phi %v)", p, f.Signature(), f.Phi, base.Signature(), base.Phi)
		}
		g, err := Compute(ref, rev, sim, p)
		if err != nil {
			t.Fatal(err)
		}
		if g.Signature() != base.Signature() || g.Phi != base.Phi {
			t.Fatalf("θ = %v, reversed order: matching %s, want %s", p, g.Signature(), base.Signature())
		}
	}
}

// A knot increment at 0 flattens the substitution function below that knot, so candidates with different cos tie
// exactly; the tie must go the way the interior (increment > 0) goes, the GED value must come from the true costs,
// and the choice must not depend on proposition IDs or move under small θ perturbations (decisions §11 item 5).
func TestBoundaryTieMatchesInterior(t *testing.T) {
	// Single pair: s.p1 shifted far from its origin (cos 0.40) against an added proposition (cos 0.20 to s.p1); the
	// added proposition's ID sorts before, between and after the others, so the solver's index rule cannot help.
	for _, extra := range []string{"a1", "n1", "z1"} {
		ref := Graph{Props: []Prop{{ID: "s.p0"}, {ID: "s.p1"}}, Edges: []Edge{{From: "s.p1", To: "s.p0", Kind: RA}}}
		v := Graph{Props: []Prop{{ID: "s.p0"}, {ID: "s.p1"}, {ID: extra}}, Edges: []Edge{{From: "s.p1", To: "s.p0", Kind: RA}}}
		sim := func(r, a string) float64 {
			switch {
			case r == a && r == "s.p1":
				return 0.40
			case r == a:
				return 1
			case r == "s.p1" && a == extra:
				return 0.20
			}
			return 0.15
		}
		th := Current()
		th[PSub03] = 0
		f, err := Compute(ref, v, sim, th)
		if err != nil {
			t.Fatal(err)
		}
		if f.Phi[PEdgeMissing] != 0 || f.EdgeExtra != 0 || f.Phi[PNodeExtra] != 1 {
			t.Fatalf("extra %s: boundary tie not broken as in the interior: phi %v extra %v", extra, f.Phi, f.EdgeExtra)
		}
		// The GED value is the true cost of that assignment: Sub(0.40) + θ_extra, with no trace of the ε terms.
		if want := th.Sub(0.40) + th[PNodeExtra]; math.Abs(f.Cost(th)-want) > 1e-12 {
			t.Fatalf("extra %s: cost %v, want %v", extra, f.Cost(th), want)
		}
		for k := range 100 {
			p := th
			for i := range p {
				p[i] = math.Max(0, p[i]+0.01*float64((k*5+i*7)%9-4)/4)
			}
			p[PSub03] = 1e-6 * float64(k%3)
			g, err := Compute(ref, v, sim, p)
			if err != nil {
				t.Fatal(err)
			}
			if g.Signature() != f.Signature() {
				t.Fatalf("extra %s, θ = %v: matching %s, want %s", extra, p, g.Signature(), f.Signature())
			}
		}
	}

	// Two pairs whose cos straddle the knot: {0.10, 0.35} against {0.25, 0.31}. In the interior the first wins
	// (h₀.₃ sums 1 + 0.75 < 1 + 0.95) although its cos sum is lower, so a tie-break by cos alone would pick the second.
	two := func(c map[[2]string]float64) Sim {
		return func(r, a string) float64 {
			if x, ok := c[[2]string{r, a}]; ok {
				return x
			}
			return 0.05
		}
	}
	ref := Graph{Props: []Prop{{ID: "p"}, {ID: "q"}}}
	v := Graph{Props: []Prop{{ID: "a"}, {ID: "b"}}}
	sim := two(map[[2]string]float64{{"p", "a"}: 0.10, {"q", "b"}: 0.35, {"q", "a"}: 0.25, {"p", "b"}: 0.31})
	th := Current()
	th[PSub03] = 0
	f, err := Compute(ref, v, sim, th)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []float64{1e-6, 1e-4, 1e-2} {
		p := th
		p[PSub03] = d
		g, err := Compute(ref, v, sim, p)
		if err != nil {
			t.Fatal(err)
		}
		if g.Signature() != f.Signature() {
			t.Fatalf("θ_cos0.3 = %g: matching %s, boundary %s", d, g.Signature(), f.Signature())
		}
	}
	if f.Assign[0] != 0 || f.Assign[1] != 1 {
		t.Fatalf("boundary matching %v, want a→p, b→q (the interior order)", f.Assign)
	}

	// The first knot at 0 flattens [0.9, 1]: {0.92, 0.99} against {0.95, 0.95}; the interior prefers the first.
	sim = two(map[[2]string]float64{{"p", "a"}: 0.92, {"q", "b"}: 0.99, {"q", "a"}: 0.95, {"p", "b"}: 0.95})
	th = Current()
	th[PSub09] = 0
	f, err = Compute(ref, v, sim, th)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []float64{1e-6, 1e-4, 1e-2} {
		p := th
		p[PSub09] = d
		g, err := Compute(ref, v, sim, p)
		if err != nil {
			t.Fatal(err)
		}
		if g.Signature() != f.Signature() || f.Assign[0] != 0 {
			t.Fatalf("θ_cos0.9 = %g: matching %s, boundary %s (assign %v)", d, g.Signature(), f.Signature(), f.Assign)
		}
	}
}

// The tie-break terms live on the substitution entries only: a substitution that is genuinely cheaper or dearer than
// delete + insert (by more than its perturbation) is not flipped, and an exact tie goes to delete + insert.
func TestTieBreakDoesNotFlipDeleteInsert(t *testing.T) {
	ref := Graph{Props: []Prop{{ID: "p"}}}
	v := Graph{Props: []Prop{{ID: "a"}}}
	sim := func(string, string) float64 { return 0.1 } // Sub(0.1) = Σ knots
	for _, c := range []struct {
		knots float64 // each knot increment; Sub = 4·knots against delete + insert = 2
		sub   bool    // substitution expected
	}{{0.5 - 0.0025, true}, {0.5 + 0.0025, false}, {0.5, false}} {
		th := Theta{1, 1, c.knots, c.knots, c.knots, c.knots, 1, 0.75, 0.75, 0.75, 1}
		f, err := Compute(ref, v, sim, th)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.Assign[0] == 0; got != c.sub {
			t.Errorf("knots %v: substitution %v, want %v (phi %v)", c.knots, got, c.sub, f.Phi)
		}
	}
}

// The linearization must agree: with the same matching, Cost(θ) equals the GED from Compute(θ).
func TestFeaturesAreLinear(t *testing.T) {
	s := uniform()
	v := uniform()
	v.Edges[0].Kind = MA
	sim := func(a, b string) float64 {
		if a == b {
			if a == "p2" {
				return 0.62
			}
			return 1
		}
		return 0.1
	}
	th := Theta{1.3, 0.7, 0.15, 0.3, 0.2, 0.1, 1.2, 1.5, 0.6, 0.9, 1.2}
	f, _ := Compute(s, v, sim, th)
	want := th.Sub(0.62) + th[PTypeRAMA]
	if got := f.Cost(th); math.Abs(got-want) > 1e-9 {
		t.Fatalf("Cost = %v, want %v", got, want)
	}
}
