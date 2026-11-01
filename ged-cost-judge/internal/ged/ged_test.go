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
