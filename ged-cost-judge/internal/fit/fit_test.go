package fit

import (
	"math"
	"testing"
	"time"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
)

// TestGaussHermite checks the quadrature rule integrates e^{−x²} and x²e^{−x²}.
func TestGaussHermite(t *testing.T) {
	x, w := ghRule(20)
	s0, s2 := 0.0, 0.0
	for i := range x {
		s0 += w[i]
		s2 += w[i] * x[i] * x[i]
	}
	if math.Abs(s0-math.Sqrt(math.Pi)) > 1e-9 || math.Abs(s2-math.Sqrt(math.Pi)/2) > 1e-9 {
		t.Fatalf("GH moments %v %v", s0, s2)
	}
}

// TestProjLBFGS minimises a bounded quadratic whose unconstrained minimum lies outside the box.
func TestProjLBFGS(t *testing.T) {
	f := func(x, g []float64) float64 {
		// (x0 + 1)² + 10 (x1 − 2)² + (x2 − 0.3)²  → box [0, 1]³ → optimum (0, 1, 0.3)
		if g != nil {
			g[0], g[1], g[2] = 2*(x[0]+1), 20*(x[1]-2), 2*(x[2]-0.3)
		}
		return (x[0]+1)*(x[0]+1) + 10*(x[1]-2)*(x[1]-2) + (x[2]-0.3)*(x[2]-0.3)
	}
	r := projLBFGS(f, []float64{0.5, 0.5, 0.9}, []float64{0, 0, 0}, []float64{1, 1, 1}, boxOptions{})
	want := []float64{0, 1, 0.3}
	for i := range want {
		if math.Abs(r.X[i]-want[i]) > 1e-6 {
			t.Fatalf("x = %v, want %v (converged %v, iters %d)", r.X, want, r.Converged, r.Iter)
		}
	}
	if !r.Converged {
		t.Fatalf("not converged")
	}
}

// TestGradient compares the analytic gradient of both objectives with finite differences.
func TestGradient(t *testing.T) {
	w := newWorld(3, altTheta(), 0.8)
	w.neg = 1.2
	trips, js := w.generate(12, true)
	for _, mixed := range []bool{false, true} {
		opts := baseOpts(3)
		opts.Gamma = GammaFree
		for i := range trips {
			trips[i].CohA, trips[i].CohB = float64(i%3), float64(i%2)
			trips[i].Weight = 0.5 + float64(i%4)*0.4
		}
		opts.Weighted = true
		opts.Tie = TypeTie()
		l := layout{j: 3}
		ds, err := features(trips, altTheta())
		if err != nil {
			t.Fatal(err)
		}
		p := Init(opts)
		p.Theta = altTheta()
		p.ThetaNeg = 0.7
		p.Gamma = 0.2
		p.Sigma = 0.9
		for j := range 3 {
			p.Beta[j], p.Tau[j], p.Pi[j] = 1.5+0.3*float64(j), 0.5+0.1*float64(j), 0.1*float64(j)
		}
		// Centre the adaptive quadrature at slightly different parameters so the nodes are not exactly at the mode.
		at := p.Clone()
		at.Sigma = 1.1
		o := newObjective(l, ds, trips, js, opts, 1e-4, mixed, at)
		x := l.pack(p, mixed)
		g := make([]float64, len(x))
		f0 := o.eval(x, g)
		lo, hi := l.bounds(opts, p, mixed)
		for i := range x {
			if lo[i] == hi[i] {
				continue
			}
			h := 1e-5
			xp := append([]float64(nil), x...)
			xm := append([]float64(nil), x...)
			xp[i] += h
			xm[i] -= h
			fd := (o.eval(xp, nil) - o.eval(xm, nil)) / (2 * h)
			if math.Abs(fd-g[i]) > 1e-4*math.Max(1, math.Abs(fd)) {
				t.Errorf("mixed=%v coord %d: analytic %g, numeric %g (f0 %g)", mixed, i, g[i], fd, f0)
			}
		}
	}
}

// TestRecovery (deliverable c): the bound-constrained fit recovers θ on synthetic data and the bootstrap CI has
// non-zero width and covers the truth.
func TestRecovery(t *testing.T) {
	start := time.Now()
	truth := altTheta()
	w := newWorld(7, truth, 0)
	w.neg = 1.0
	trips, js := w.generate(300, true)
	opts := baseOpts(3)
	opts.Tie = TypeTie()
	res, err := FitMulti(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fit in %s: outer=%d conv=%v cycled=%v spread=%.3f nll=%.1f", time.Since(start), res.Outer, res.Converged, res.Cycled, res.StartSpread, res.NegLogLik)
	t.Logf("θ̂ = %.2f (truth %.2f) neg %.2f", res.Params.Theta, truth, res.Params.ThetaNeg)
	for _, p := range []int{ged.PNodeMissing, ged.PNodeExtra, ged.PEdgeMissing, ged.PTypeRAMA, ged.PDirection} {
		if math.Abs(res.Params.Theta[p]-truth[p]) > 0.3 {
			t.Errorf("param %d: got %.3f want %.3f", p, res.Params.Theta[p], truth[p])
		}
	}
	if math.Abs(res.Params.Theta[ged.PTypeRACA]-res.Params.Theta[ged.PTypeRAMA]) > 1e-12 {
		t.Errorf("tie not enforced")
	}
	if NumFree(opts) != 9 {
		t.Errorf("free params = %d, want 9", NumFree(opts))
	}

	bstart := time.Now()
	br, err := BootstrapOpts(trips, js, opts, res.Params, BootOptions{Reps: 40, Seed: 11})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("bootstrap 40 reps in %s", time.Since(bstart))
	miss := 0
	for _, p := range []int{ged.PNodeMissing, ged.PNodeExtra, ged.PEdgeMissing, ged.PTypeRAMA, ged.PDirection} {
		col := Column(br.Theta, p)
		lo, hi := Quantile(col, 0.025), Quantile(col, 0.975)
		if hi-lo < 1e-3 {
			t.Errorf("param %d: zero-width CI [%g, %g]", p, lo, hi)
		}
		if truth[p] < lo || truth[p] > hi {
			miss++
			t.Logf("param %d: CI [%.3f, %.3f] misses truth %.2f", p, lo, hi, truth[p])
		}
	}
	if miss > 1 {
		t.Errorf("%d of 5 intervals miss the truth", miss)
	}
	sh := Summarise(br.Meta)
	t.Logf("shares: cycled %.2f capped %.2f notconv %.2f cold %d", sh.Cycled, sh.Capped, sh.NotConverged, sh.Cold)
}

// TestMixedSigmaZero (deliverable d): FitMixed with σ fixed near 0 agrees with the independence fit.
func TestMixedSigmaZero(t *testing.T) {
	w := newWorld(5, altTheta(), 0)
	trips, js := w.generate(60, false)
	opts := baseOpts(3)
	opts.Tie = TypeTie()
	opts.FitNeg = false
	ind, err := Fit(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Mixed likelihood at σ → 0 must equal the independence likelihood.
	p := ind.Params
	p.Sigma = 1e-6
	nllM, err := NegLogLikMixed(trips, js, p, 20)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(nllM-ind.NegLogLik) > 1e-4*math.Max(1, ind.NegLogLik) {
		t.Fatalf("mixed nll at σ≈0 = %.4f, independence %.4f", nllM, ind.NegLogLik)
	}
	// With σ generated as 0 the mixed fit should land close to the independence fit.
	mix, err := FitMixed(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("σ̂ = %.3f; θ ind %.2f; θ mix %.2f", mix.Params.Sigma, ind.Params.Theta, mix.Params.Theta)
	if mix.Params.Sigma > 0.6 {
		t.Errorf("σ̂ = %.3f, expected small", mix.Params.Sigma)
	}
	for q := range ged.NumParams {
		if math.Abs(mix.Params.Theta[q]-ind.Params.Theta[q]) > 0.25 {
			t.Errorf("param %d: mixed %.3f vs independence %.3f", q, mix.Params.Theta[q], ind.Params.Theta[q])
		}
	}
}

// TestMixedRecoversSigma: with a real shared offset, FitMixed finds σ > 0.
func TestMixedRecoversSigma(t *testing.T) {
	w := newWorld(8, altTheta(), 1.5)
	trips, js := w.generate(120, false)
	opts := baseOpts(3)
	opts.Tie = TypeTie()
	opts.FitNeg = false
	mix, err := FitMixed(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("σ̂ = %.3f (truth 1.5), θ = %.2f", mix.Params.Sigma, mix.Params.Theta)
	if math.Abs(mix.Params.Sigma-1.5) > 0.3 {
		t.Errorf("σ̂ = %.3f, want 1.5 ± 0.3", mix.Params.Sigma)
	}
	// The conditional β of the mixed fit is near the generating β (the independence β is attenuated).
	for j, want := range []float64{2.0, 1.5, 2.5} {
		if math.Abs(mix.Params.Beta[j]-want) > 0.25*want {
			t.Errorf("β_%d = %.3f, want %.2f ± 25%%", j, mix.Params.Beta[j], want)
		}
	}
}

// TestTie (deliverable e): the tied fit has 9 free parameters and reproduces an untied fit when the three type costs are equal in truth.
func TestTie(t *testing.T) {
	w := newWorld(9, altTheta(), 0) // altTheta has equal type costs (1.0)
	trips, js := w.generate(250, false)
	opts := baseOpts(3)
	opts.FitNeg = false
	untied, err := Fit(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.Tie = TypeTie()
	if NumFree(opts) != 9 {
		t.Fatalf("free = %d", NumFree(opts))
	}
	tied, err := Fit(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("untied types %.2f %.2f %.2f; tied %.2f; nll %.1f vs %.1f", untied.Params.Theta[ged.PTypeRACA], untied.Params.Theta[ged.PTypeRAMA], untied.Params.Theta[ged.PTypeCAMA], tied.Params.Theta[ged.PTypeRAMA], untied.NegLogLik, tied.NegLogLik)
	mean := (untied.Params.Theta[ged.PTypeRACA] + untied.Params.Theta[ged.PTypeRAMA] + untied.Params.Theta[ged.PTypeCAMA]) / 3
	if math.Abs(tied.Params.Theta[ged.PTypeRAMA]-mean) > 0.3 {
		t.Errorf("tied %.3f vs mean of untied %.3f", tied.Params.Theta[ged.PTypeRAMA], mean)
	}
	if tied.NegLogLik < untied.NegLogLik-1e-6 {
		t.Errorf("tied fit beats untied: %.4f < %.4f", tied.NegLogLik, untied.NegLogLik)
	}
	for q := range ged.NumParams {
		if q == ged.PTypeRACA || q == ged.PTypeRAMA || q == ged.PTypeCAMA {
			continue
		}
		if math.Abs(tied.Params.Theta[q]-untied.Params.Theta[q]) > 0.2 {
			t.Errorf("param %d: tied %.3f vs untied %.3f", q, tied.Params.Theta[q], untied.Params.Theta[q])
		}
	}
}

// TestFiltersWeightsNoop (deliverable f): empty flags/families/weights leave the fit unchanged.
func TestFiltersWeightsNoop(t *testing.T) {
	w := newWorld(4, altTheta(), 0)
	trips, js := w.generate(30, false)
	opts := baseOpts(3)
	opts.FitNeg = false
	opts.IncludeAll = false // default exclusion list, but no triplet carries a flag
	opts.ExcludeSameFamily = true
	a, err := Fit(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	ft, fj, kept := Filter(trips, js, opts)
	if len(ft) != len(trips) || len(fj) != len(js) || len(kept) != len(trips) {
		t.Fatalf("filter dropped something: %d/%d triplets, %d/%d judgments", len(ft), len(trips), len(fj), len(js))
	}
	opts.Weighted = true // all Weight = 0 → 1
	b, err := Fit(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(a.NegLogLik-b.NegLogLik) > 1e-6 {
		t.Fatalf("weights changed the fit: %.6f vs %.6f", a.NegLogLik, b.NegLogLik)
	}
	// Now actually filter: flag half the triplets and give the judges families.
	for i := range trips {
		if i%2 == 0 {
			trips[i].Flags = []string{"x_spk"}
		}
		trips[i].VerifierFamily = "anthropic"
	}
	for i := range js {
		if js[i].Judge == 0 {
			js[i].Family = "Anthropic"
		}
	}
	ft, fj, _ = Filter(trips, js, opts)
	if len(ft) != len(trips)/2 {
		t.Fatalf("x_spk filter kept %d of %d", len(ft), len(trips))
	}
	for _, k := range fj {
		if k.Judge == 0 {
			t.Fatalf("family-coupling filter kept judge 0")
		}
	}
	// Weights change the objective when they differ.
	for i := range trips {
		trips[i].Weight = 2
	}
	c, err := Fit(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.NegLogLik <= 0 || c.Judgments == len(js) {
		t.Fatalf("weighted/filtered fit: nll %.3f judgments %d", c.NegLogLik, c.Judgments)
	}
}

// TestNormalize checks ŝ and θ̃ against hand values.
func TestNormalize(t *testing.T) {
	th := ged.Current()
	tt, s := Normalize(th[:], nil)
	if math.Abs(s-1) > 1e-12 || math.Abs(tt[NormEdgeExtra]-1) > 1e-12 || len(tt) != 12 {
		t.Fatalf("θ₀ should normalize to itself: s=%v len=%d", s, len(tt))
	}
	// Every unit cost × 1.25: ŝ = (1.25·4.75 + 1)/5.75; θ̃_extra = 1/ŝ.
	th2 := th
	for _, p := range []int{ged.PNodeMissing, ged.PNodeExtra, ged.PEdgeMissing, ged.PDirection, ged.PTypeRAMA} {
		th2[p] *= 1.25
	}
	v := append(th2[:], 0.5)
	tt, s = Normalize(v, nil)
	want := (1.25*4.75 + 1) / 5.75
	if math.Abs(s-want) > 1e-12 || len(tt) != 13 {
		t.Fatalf("s = %v, want %v (len %d)", s, want, len(tt))
	}
	if math.Abs(tt[NormEdgeExtra]-1/want) > 1e-12 || math.Abs(tt[NormNeg]-0.5/want) > 1e-12 {
		t.Fatalf("θ̃ = %v", tt)
	}
	sum := 0.0
	for _, p := range DefaultU {
		sum += tt[p]
	}
	if math.Abs(sum-5.75) > 1e-9 {
		t.Fatalf("normalized U sum = %v, want 5.75", sum)
	}
}

// TestCrossValidate: OOF log loss is finite and R0 with FixTheta fits aux only.
func TestCrossValidate(t *testing.T) {
	w := newWorld(6, altTheta(), 0)
	trips, js := w.generate(40, false)
	opts := baseOpts(3)
	opts.FitNeg = false
	opts.Tie = TypeTie()
	cv, err := CrossValidateOpts(trips, js, opts, CVOptions{K: 4, Repeats: 2, Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(cv.Delta) != 2 || len(cv.LogLoss[0]) != len(js) {
		t.Fatalf("shape %d %d", len(cv.Delta), len(cv.LogLoss[0]))
	}
	ll := cv.MeanLogLoss(0)
	if math.IsNaN(ll) || ll <= 0 || ll > 2 {
		t.Fatalf("mean log loss %v", ll)
	}
	fixed := opts
	fixed.FixTheta = true
	cv0, err := CrossValidateOpts(trips, js, fixed, CVOptions{K: 4, Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range cv0.Fits[0] {
		if f.Params.Theta != ged.Current() {
			t.Fatalf("FixTheta fit moved θ: %v", f.Params.Theta)
		}
	}
	// Folds are balanced across generators.
	cnt := map[[2]string]int{}
	for i, f := range cv.Fold[0] {
		cnt[[2]string{trips[i].Generator, string(rune('0' + f))}]++
	}
	for g := range map[string]bool{"g1": true, "g2": true} {
		for f := range 4 {
			if cnt[[2]string{g, string(rune('0' + f))}] == 0 {
				t.Fatalf("generator %s missing from fold %d", g, f)
			}
		}
	}
}

// TestNestedDSmall runs the nested bootstrap at a tiny size for shape and finiteness.
func TestNestedDSmall(t *testing.T) {
	w := newWorld(10, altTheta(), 0)
	trips, js := w.generate(30, false)
	opts := baseOpts(3)
	opts.FitNeg = false
	opts.Tie = TypeTie()
	labels := make([]string, len(trips))
	for i := range labels {
		labels[i] = []string{"near", "clear"}[i%2]
	}
	d, err := NestedD(trips, js, opts, ged.Current(), labels, DOptions{Reps: 4, Seed: 1, K: 3})
	if err != nil {
		t.Fatal(err)
	}
	if math.IsNaN(d.Agree) || len(d.AgreeBoot) != 4 || len(d.StrataBoot["near"]) != 4 {
		t.Fatalf("D = %+v", d)
	}
	t.Logf("D = %.2f pt, logloss %.4f, boot %v", d.Agree, d.LogLoss, d.AgreeBoot)
}

// TestSimulateAndLR: the parametric bootstrap LR machinery runs and gives a p in (0, 1].
func TestSimulateAndLR(t *testing.T) {
	w := newWorld(12, altTheta(), 0.5)
	trips, js := w.generate(20, false)
	opts := baseOpts(3)
	opts.FitNeg = false
	opts.Tie = TypeTie()
	null, err := Fit(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	fitNull := func(tr []Triplet, j []Judgment) (float64, error) {
		r, err := Fit(tr, j, opts, nil)
		return r.NegLogLik, err
	}
	alt := opts
	alt.Tie = nil
	fitAlt := func(tr []Triplet, j []Judgment) (float64, error) {
		r, err := Fit(tr, j, alt, nil)
		return r.NegLogLik, err
	}
	lr, err := ParametricBootstrapLR(trips, js, null.Params, fitNull, fitAlt, 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !(lr.P > 0 && lr.P <= 1) || lr.Observed < -1e-6 {
		t.Fatalf("LR %+v", lr)
	}
}

// TestTransferSlope: with Δ2 = Δ0 the slope is unidentified but finite; with judgments generated at Δ2, κ ≈ 1.
func TestTransferSlope(t *testing.T) {
	w := newWorld(13, altTheta(), 0)
	trips, js := w.generate(80, false)
	d2, _ := Delta(trips, altTheta())
	d0, _ := Delta(trips, ged.Current())
	cl := make([]int, len(trips))
	for i := range cl {
		cl[i] = trips[i].Source
	}
	res, err := TransferSlope(d0, d2, js, 3, cl, 10, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("κ = %.3f boot %v", res.Kappa, res.Boot)
	if res.Kappa < 0.4 || res.Kappa > 2 {
		t.Errorf("κ = %.3f, expected near 1", res.Kappa)
	}
}

// TestDeterministicSignatures: shuffling the proposition order of the inputs leaves Δ and the signature unchanged.
func TestDeterministicSignatures(t *testing.T) {
	w := newWorld(14, altTheta(), 0)
	trips, _ := w.generate(10, false)
	s1, _ := Signatures(trips, ged.Current())
	d1, _ := Delta(trips, ged.Current())
	for i := range trips {
		g := trips[i].A.Clone()
		for a, b := 0, len(g.Props)-1; a < b; a, b = a+1, b-1 {
			g.Props[a], g.Props[b] = g.Props[b], g.Props[a]
		}
		trips[i].A = g
	}
	s2, _ := Signatures(trips, ged.Current())
	d2, _ := Delta(trips, ged.Current())
	for i := range trips {
		if s1[i] != s2[i] || math.Abs(d1[i]-d2[i]) > 1e-12 {
			t.Fatalf("triplet %d: signature or Δ changed with input order", i)
		}
	}
}
