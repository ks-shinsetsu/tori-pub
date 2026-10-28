package stats

import (
	"math"
	"math/rand/v2"
	"testing"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestHolm(t *testing.T) {
	// Hand example: p = (0.01, 0.04, 0.03, 0.20), m = 4.
	// sorted: 0.01×4 = 0.04; 0.03×3 = 0.09; 0.04×2 = 0.08 → monotone 0.09; 0.20×1 = 0.20.
	adj, rej := Holm([]float64{0.01, 0.04, 0.03, 0.20}, 0.05)
	want := []float64{0.04, 0.09, 0.09, 0.20}
	for i := range want {
		if !near(adj[i], want[i], 1e-12) {
			t.Fatalf("adj = %v, want %v", adj, want)
		}
	}
	if !rej[0] || rej[1] || rej[2] || rej[3] {
		t.Fatalf("reject = %v", rej)
	}
	// Six tests all at 0.008: 6×0.008 = 0.048 → every one rejected (step-down).
	_, rej = Holm([]float64{0.008, 0.008, 0.008, 0.008, 0.008, 0.008}, 0.05)
	for _, r := range rej {
		if !r {
			t.Fatalf("expected all rejected: %v", rej)
		}
	}
}

func TestClopperPearson(t *testing.T) {
	// 0/100 → upper 90% bound = 1 − 0.1^(1/100) = 0.02276.
	if u := ClopperPearsonUpper(0, 100, 0.90); !near(u, 0.02276, 2e-5) {
		t.Fatalf("0/100: %v", u)
	}
	// 15/100 → ≈ 0.207.
	if u := ClopperPearsonUpper(15, 100, 0.90); !near(u, 0.207, 2e-3) {
		t.Fatalf("15/100: %v", u)
	}
	// 14/100 is the last passing count at n_T = 100, r_max = 0.20 (decisions §9.1: fail iff k_eff ≥ 15).
	if u := ClopperPearsonUpper(14, 100, 0.90); u > 0.20 {
		t.Fatalf("14/100 should pass: %v", u)
	}
	if u := ClopperPearsonUpper(100, 100, 0.90); u != 1 {
		t.Fatalf("n/n: %v", u)
	}
	// Two-sided 95% interval for 0/10: [0, 0.3085].
	ci := ClopperPearson(0, 10, 0.95)
	if ci.Lo != 0 || !near(ci.Hi, 0.3085, 1e-3) {
		t.Fatalf("0/10: %+v", ci)
	}
	// Lower bound for 10/10 at 95% one-sided: 0.05^(1/10) = 0.7411.
	if l := ClopperPearsonLower(10, 10, 0.95); !near(l, 0.7411, 1e-3) {
		t.Fatalf("10/10 lower: %v", l)
	}
}

func TestReversalGate(t *testing.T) {
	g := ReversalGate(15, 10, 200, 100, 0, 0)
	if !near(g.R, 0.075, 1e-12) || !near(g.KEff, 7.5, 1e-12) || g.Fail {
		t.Fatalf("%+v", g)
	}
	g = ReversalGate(30, 0, 200, 100, 0, 0) // r = 0.15 → k_eff = 15 → fails
	if !g.Fail {
		t.Fatalf("expected fail: %+v", g)
	}
	g = ReversalGate(29, 0, 200, 100, 0, 0) // k_eff = 14.5 → upper bound?
	t.Logf("k_eff 14.5: upper %.4f fail %v", g.Upper, g.Fail)
	// Pass probabilities of §9.1: 0.05 → 1.00, 0.10 → 0.93, 0.15 → 0.46, 0.20 → 0.08.
	for _, c := range []struct{ r, want, tol float64 }{{0.05, 1.00, 0.01}, {0.10, 0.93, 0.02}, {0.15, 0.46, 0.03}, {0.20, 0.08, 0.02}} {
		if p := GatePassProbability(c.r, 100, 0, 0); !near(p, c.want, c.tol) {
			t.Errorf("pass prob at r = %.2f: %.3f, want %.2f", c.r, p, c.want)
		}
	}
}

func TestDecide(t *testing.T) {
	boot := func(center, half float64, n int) []float64 {
		v := make([]float64, n)
		for i := range v {
			v[i] = center + half*(2*float64(i)/float64(n-1)-1)
		}
		return v
	}
	// keep: 90% CI [0.92, 1.08] inside [0.75, 1.25].
	d := Decide(DecisionInput{Est: 1, Boot: boot(1, 0.09, 1000), Theta0: 1, M: 0.25})
	if d.Label != Keep {
		t.Fatalf("keep: %v (%s)", d.Label, d.Reason)
	}
	// change: centred at 1.5, half-width 0.1.
	d = Decide(DecisionInput{Est: 1.5, Boot: boot(1.5, 0.1, 1000), Theta0: 1, M: 0.25})
	if d.Label != Change {
		t.Fatalf("change: %v", d.Label)
	}
	// undecided (different): centred at 1.2, 95% CI excludes 1 but the 90% CI is not outside the band.
	d = Decide(DecisionInput{Est: 1.2, Boot: boot(1.2, 0.15, 1000), Theta0: 1, M: 0.25})
	if d.Label != UndecidedDifferent {
		t.Fatalf("different: %v (%+v)", d.Label, d.CI95)
	}
	// undecided (imprecise): wide.
	d = Decide(DecisionInput{Est: 1, Boot: boot(1, 0.5, 1000), Theta0: 1, M: 0.25})
	if d.Label != UndecidedImprecise {
		t.Fatalf("imprecise: %v", d.Label)
	}
	// (a) zero column: every replicate equals θ₀ exactly → not estimable, never keep.
	zero := make([]float64, 1000)
	for i := range zero {
		zero[i] = 1
	}
	d = Decide(DecisionInput{Est: 1, Boot: zero, Theta0: 1, M: 0.25})
	if d.Label != NotEstimable {
		t.Fatalf("zero column: %v", d.Label)
	}
	// (b) zero-width CI with numerical noise far below the IQR floor → not estimable.
	d = Decide(DecisionInput{Est: 1, Boot: boot(1, 1e-9, 1000), Theta0: 1, M: 0.25})
	if d.Label != NotEstimable {
		t.Fatalf("zero width: %v", d.Label)
	}
	// A frozen anchored coordinate whose normalized column moves only through ŝ → not estimable.
	d = Decide(DecisionInput{Est: 1, Boot: boot(1, 0.09, 1000), Theta0: 1, M: 0.25, AnchoredIQR: 0, CheckAnchored: true})
	if d.Label != NotEstimable {
		t.Fatalf("frozen anchored: %v", d.Label)
	}
	// Information floor by count.
	d = Decide(DecisionInput{Est: 1, Boot: boot(1, 0.09, 1000), Theta0: 1, M: 0.25, NInformative: 100, MinInformative: 650})
	if d.Label != NotEstimable {
		t.Fatalf("floor: %v", d.Label)
	}
	// Boundary share 60% → not estimable; 30% → one-sided.
	d = Decide(DecisionInput{Est: 1, Boot: boot(1, 0.09, 1000), Theta0: 1, M: 0.25, BoundaryShare: 0.6})
	if d.Label != NotEstimable {
		t.Fatalf("boundary 60%%: %v", d.Label)
	}
	d = Decide(DecisionInput{Est: 0.2, Boot: boot(0.2, 0.2, 1000), Theta0: 1, M: 0.25, BoundaryShare: 0.3})
	if d.Label != Change || !d.OneSided {
		t.Fatalf("one-sided change: %v onesided=%v upper=%.3f", d.Label, d.OneSided, d.Upper90)
	}
	d = Decide(DecisionInput{Est: 0.9, Boot: boot(0.9, 0.05, 1000), Theta0: 1, M: 0.25, BoundaryShare: 0.3})
	if d.Label != UndecidedImprecise || !d.OneSided {
		t.Fatalf("one-sided keep impossible: %v", d.Label)
	}
	// Combine.
	if Combine(Keep, Keep) != Keep || Combine(Keep, Change) != UndecidedModelSensitive || Combine(NotEstimable, Keep) != NotEstimable {
		t.Fatal("combine")
	}
}

func TestVerdict(t *testing.T) {
	six := []Label{Keep, Keep, Keep, Keep, Keep, Keep}
	v, miss := Verdict(VerdictInput{Labels: six, H1: true, H2: true, DLower95: -1, DUpper95: 1.5})
	if v != Validated || len(miss) != 0 {
		t.Fatalf("%v %v", v, miss)
	}
	v, miss = Verdict(VerdictInput{Labels: six, H1: true, H2: false, DLower95: -1, DUpper95: 1.5})
	if v != NotContradicted || len(miss) != 1 {
		t.Fatalf("%v %v", v, miss)
	}
	six[2] = Change
	v, _ = Verdict(VerdictInput{Labels: six, H1: true, H2: true, DLower95: -1, DUpper95: 1.5})
	if v != Contradicted {
		t.Fatalf("%v", v)
	}
	six[2] = Keep
	v, _ = Verdict(VerdictInput{Labels: six, H1: true, H2: true, DLower95: 2.5, DUpper95: 4})
	if v != Contradicted {
		t.Fatalf("%v", v)
	}
	v, _ = Verdict(VerdictInput{Labels: six, H1: true, H2: true, DLower95: math.NaN(), DUpper95: math.NaN()})
	if v != NotContradicted {
		t.Fatalf("%v", v)
	}
}

func TestBootstrapContrast(t *testing.T) {
	boot := []float64{-1.0, -0.9, -0.8, -0.7, -0.6, -0.5, -0.4, -0.3, 0.1, 0.2}
	r := BootstrapContrast(-0.5, boot, 0, Less)
	// 2 of 10 replicates on the wrong side; q₀.₉₅ = 0.155 > 0 → not supported.
	if !near(r.P, 0.2, 1e-12) || r.Supported || !near(r.Bound, 0.155, 1e-9) {
		t.Fatalf("less: %+v", r)
	}
	r = BootstrapContrast(-0.5, boot[:8], 0, Less)
	if r.P != 0 || !r.Supported {
		t.Fatalf("less supported: %+v", r)
	}
	r = BootstrapContrast(-0.5, boot, -1.1, Greater)
	if r.P != 0 || !r.Supported {
		t.Fatalf("greater: %+v", r)
	}
	r = BootstrapContrast(-0.5, boot, -0.65, TwoSided)
	// ≤ −0.65: 4 values; ≥ −0.65: 6 → p = 2·0.4 = 0.8
	if !near(r.P, 0.8, 1e-12) || r.Supported {
		t.Fatalf("two-sided: %+v", r)
	}
}

func TestMcNemar(t *testing.T) {
	// b = 8, c = 2: one-sided P[X ≥ 8 | n = 10] = (45 + 10 + 1)/1024 = 0.0547.
	if p := McNemarExact(8, 2, true); !near(p, 56.0/1024, 1e-9) {
		t.Fatalf("one-sided %v", p)
	}
	if p := McNemarExact(8, 2, false); !near(p, 2*56.0/1024, 1e-9) {
		t.Fatalf("two-sided %v", p)
	}
	if McNemarExact(0, 0, true) != 1 {
		t.Fatal("empty")
	}
	// Clustered: A right on 70 of 100 units, B on 50, clusters of 2; the bootstrap lower bound must be > 0.
	rightA, rightB, elig := make([]bool, 100), make([]bool, 100), make([]bool, 100)
	cl := make([]int, 100)
	for u := range 100 {
		rightA[u], rightB[u], elig[u], cl[u] = u < 70, u%2 == 0, true, u/2
	}
	r := ClusteredMcNemar(rightA, rightB, elig, cl, nil, 200, 1)
	if !near(r.Est, 20, 1e-9) || !r.Supported {
		t.Fatalf("clustered: %+v", r)
	}
	b, c := DiscordantCounts(rightA, rightB, elig)
	if b != 35 || c != 15 {
		t.Fatalf("discordant %d %d", b, c)
	}
}

func TestKrippendorff(t *testing.T) {
	// Hand example: two coders, units (1,1), (1,1), (2,2), (1,2): α = 1 − 7·2/30 = 0.5333.
	v := [][]int{{1, 1}, {1, 1}, {2, 2}, {1, 2}}
	if a := KrippendorffAlpha(v); !near(a, 1-14.0/30, 1e-12) {
		t.Fatalf("α = %v", a)
	}
	if a := KrippendorffAlpha([][]int{{0, 0}, {1, 1}, {2, 2, -1}}); a != 1 {
		t.Fatalf("perfect: %v", a)
	}
	// Three coders with a missing value: (1,1,1), (1,2,·), (2,2,2).
	// o_11 = 3, o_12 = o_21 = 1, o_22 = 3; n_1 = n_2 = 4, n = 8; α = 1 − 7·2/32 = 0.5625.
	k := [][]int{{1, 1, 1}, {1, 2, -1}, {2, 2, 2}}
	if a := KrippendorffAlpha(k); !near(a, 0.5625, 1e-12) {
		t.Fatalf("three coders: %v", a)
	}
}

func TestClusterWald(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	boot := make([][]float64, 500)
	for r := range boot {
		boot[r] = []float64{rng.NormFloat64() * 0.1, rng.NormFloat64() * 0.2}
	}
	w := ClusterWald([]float64{0, 0}, boot)
	if w.DF != 2 || w.P < 0.9 {
		t.Fatalf("null: %+v", w)
	}
	w = ClusterWald([]float64{0.5, 0.0}, boot)
	if w.P > 1e-4 {
		t.Fatalf("alt: %+v", w)
	}
}

func TestNoiseCeiling(t *testing.T) {
	// 3 judges, 4 triplets; judge 2 disagrees with the others on triplet 3, so for judges 0 and 1 the panel
	// (the other two judges' four answers) ties 2–2 there and the triplet is skipped (n = 3).
	a := [][][]int{
		{{AnsA, AnsA}, {AnsB, AnsB}, {AnsA, AnsA}, {AnsA, AnsA}},
		{{AnsA, AnsA}, {AnsB, AnsB}, {AnsA, AnsTie}, {AnsA, AnsA}},
		{{AnsA, AnsA}, {AnsB, AnsB}, {AnsA, AnsA}, {AnsB, AnsB}},
	}
	c, per, n := NoiseCeiling(a)
	if n[0] != 3 || n[2] != 4 || !near(per[0], 1, 1e-12) || !near(per[2], 0.75, 1e-12) || !near(c, (1+1+0.75)/3, 1e-12) {
		t.Fatalf("ceiling %v per %v n %v", c, per, n)
	}
}
