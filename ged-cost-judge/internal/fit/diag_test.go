package fit_test

import (
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/simulate"
)

// Diagnostics on the P1 simulator (GCJ_DIAG=1): convergence trace of the alternating loop and the bias of the
// tied RA↔MA estimate. Not part of the ordinary suite. GCJ_DIAG_WORLD selects the world of the traces (default null).

func traceWorld() string {
	if w := os.Getenv("GCJ_DIAG_WORLD"); w != "" {
		return w
	}
	return "null"
}

func diagWorld(t *testing.T, name string, sources int, seed uint64, sigma, random float64) ([]fit.Triplet, []fit.Judgment, simulate.Config) {
	cfg := simulate.Default()
	w, err := simulate.WorldByName(name)
	if err != nil {
		t.Fatal(err)
	}
	cfg.World, cfg.True, cfg.TrueNeg = w.Name, w.True, w.TrueNeg
	cfg.Sources, cfg.Seed, cfg.Sigma, cfg.Random = sources, seed, sigma, random
	trips, js, _, err := simulate.Generate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return trips, js, cfg
}

func TestDiagConvergence(t *testing.T) {
	if os.Getenv("GCJ_DIAG") == "" {
		t.Skip("GCJ_DIAG=1")
	}
	trips, js, _ := diagWorld(t, traceWorld(), 750, 1, 1, 0.1)
	opts := fit.Options{Judges: 3, Theta0: ged.Current(), FitNeg: true, Tie: fit.TypeTie(), MaxOuter: 20}
	opts.Trace = func(r fit.RoundTrace) {
		t.Logf("round %2d inner %.4f true %.4f changed %4d maxShift %.2e sumShift %.2e iters %d conv %v θ=%.3f", r.Outer, r.Inner, r.True, r.Changed, r.MaxDeltaShift, r.SumDeltaShift, r.InnerIters, r.InnerConv, r.Theta)
	}
	start := time.Now()
	res, err := fit.FitMulti(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("independence (multi-start): outer %d conv %v cycled %v capped %v spread %.4f in %s", res.Outer, res.Converged, res.Cycled, res.Capped, res.StartSpread, time.Since(start))
	for _, s := range res.Starts {
		t.Logf("  start %s: objective %.4f conv %v cycled %v", s.Name, s.Objective, s.Converged, s.Cycled)
	}
	start = time.Now()
	mix, err := fit.FitMixed(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("mixed: outer %d conv %v cycled %v capped %v σ̂ %.3f in %s", mix.Outer, mix.Converged, mix.Cycled, mix.Capped, mix.Params.Sigma, time.Since(start))
}

// TestDiagFlips decomposes every matching flip of the alternating loop (null world, working independence) into the
// node-cost margin between the old and new assignment at the new θ and the edge-cost jump it carries, and counts how
// often the same triplet flips.
func TestDiagFlips(t *testing.T) {
	if os.Getenv("GCJ_DIAG") == "" {
		t.Skip("GCJ_DIAG=1")
	}
	trips, js, _ := diagWorld(t, traceWorld(), 750, 1, 1, 0.1)
	opts := fit.Options{Judges: 3, Theta0: ged.Current(), FitNeg: true, Tie: fit.TypeTie(), MaxOuter: 20}
	ft, fj, _ := fit.Filter(trips, js, opts)
	prev := opts.Theta0
	flips := map[int]int{}
	var nodeMargins, edgeJumps []float64
	opts.Trace = func(r fit.RoundTrace) {
		for _, i := range r.Flipped {
			flips[i]++
			tr := ft[i]
			for side, v := range []struct {
				g ged.Graph
				s ged.Sim
			}{{tr.A, tr.SimA}, {tr.B, tr.SimB}} {
				old, _ := ged.Compute(tr.Ref, v.g, v.s, prev)
				nw, _ := ged.Compute(tr.Ref, v.g, v.s, r.Theta)
				if old.Signature() == nw.Signature() {
					continue
				}
				margin := nodePart(nw, r.Theta) - nodePart(old, r.Theta)
				jump := (nw.Cost(r.Theta) - nodePart(nw, r.Theta)) - (old.Cost(r.Theta) - nodePart(old, r.Theta))
				nodeMargins = append(nodeMargins, margin)
				edgeJumps = append(edgeJumps, jump)
				t.Logf("round %2d triplet %4d side %d: node margin %+.4f edge jump %+.2f (old %s | new %s)", r.Outer, i, side, margin, jump, old.Signature(), nw.Signature())
			}
		}
		prev = r.Theta
	}
	res, err := fit.Fit(ft, fj, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	repeat := 0
	for _, n := range flips {
		if n > 1 {
			repeat++
		}
	}
	maxM, minJ := 0.0, math.Inf(1)
	for k := range nodeMargins {
		maxM = math.Max(maxM, math.Abs(nodeMargins[k]))
		minJ = math.Min(minJ, math.Abs(edgeJumps[k]))
	}
	t.Logf("outer %d conv %v cycled %v capped %v; distinct flipping triplets %d (of %d), flipping more than once %d; max |node margin| %.4f, min |edge jump| %.2f",
		res.Outer, res.Converged, res.Cycled, res.Capped, len(flips), len(ft), repeat, maxM, minJ)
}

// nodePart is the node (substitution, missing, extra) part of the GED at θ with the matching of f.
func nodePart(f ged.Features, th ged.Theta) float64 {
	v := 0.0
	for _, p := range []int{ged.PNodeMissing, ged.PNodeExtra, ged.PSub09, ged.PSub07, ged.PSub05, ged.PSub03} {
		v += f.Phi[p] * th[p]
	}
	return v
}

// TestDiagValid measures the share of valid bootstrap replicates (decisions §11.2) on the null and alt worlds at 750
// sources with a small B (working independence; GCJ_DIAG_B overrides B = 30).
func TestDiagValid(t *testing.T) {
	if os.Getenv("GCJ_DIAG") == "" {
		t.Skip("GCJ_DIAG=1")
	}
	reps := 30
	if b, err := strconv.Atoi(os.Getenv("GCJ_DIAG_B")); err == nil && b > 0 {
		reps = b
	}
	for _, world := range []string{"null", "alt"} {
		trips, js, _ := diagWorld(t, world, 750, 1, 1, 0.1)
		opts := fit.Options{Judges: 3, Theta0: ged.Current(), FitNeg: true, Tie: fit.TypeTie(), MaxOuter: 20}
		start := time.Now()
		res, err := fit.Fit(trips, js, opts, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s full fit: outer %d conv %v cycled %v capped %v in %s", world, res.Outer, res.Converged, res.Cycled, res.Capped, time.Since(start))
		start = time.Now()
		br, err := fit.BootstrapOpts(trips, js, opts, res.Params, fit.BootOptions{Reps: reps, Seed: 11})
		if err != nil {
			t.Fatal(err)
		}
		sh := fit.Summarise(br.Meta)
		outer := 0
		for _, m := range br.Meta {
			outer += m.Outer
		}
		t.Logf("%s bootstrap B=%d in %s: cycled %.1f%% capped %.1f%% invalid (§11.2) %.1f%% not converged (old metric) %.1f%% failed %d; mean outer %.1f",
			world, reps, time.Since(start), 100*sh.Cycled, 100*sh.Capped, 100*sh.Invalid, 100*sh.NotConverged, br.Failed, float64(outer)/float64(reps))
		// Trace the replicates that did not converge: refit their draw from the warm start (interior floor 0.05).
		warm := res.Params.Clone()
		for p := range ged.NumParams {
			if res.Free[p] && warm.Theta[p] < 0.05 {
				warm.Theta[p] = 0.05
			}
		}
		for p, m := range opts.Tie {
			warm.Theta[p] = warm.Theta[m]
		}
		shown := 0
		for r, m := range br.Meta {
			if (m.Converged && m.InnerConverged) || m.Cold || shown >= 3 {
				continue
			}
			shown++
			bt, bj := drawSubset(trips, js, br.Draws[r])
			var prev ged.Theta
			o := opts
			o.Trace = func(tr fit.RoundTrace) {
				move := 0.0
				for p := range tr.Theta {
					move = math.Max(move, math.Abs(tr.Theta[p]-prev[p]))
				}
				t.Logf("  rep %d round %2d true %.4f changed %3d maxShift %.2f |Δθ|max %.2e flipped %v", r, tr.Outer, tr.True, tr.Changed, tr.MaxDeltaShift, move, tr.Flipped)
				prev = tr.Theta
			}
			prev = warm.Theta
			rr, err := fit.Fit(bt, bj, o, &warm)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("  rep %d: outer %d conv %v cycled %v (spread %.4f) capped %v valid %v", r, rr.Outer, rr.Converged, rr.Cycled, rr.CycleSpread, rr.Capped, rr.Valid())
		}
	}
}

// drawSubset rebuilds a bootstrap draw (the copy of fit's subset that Fit needs; CV-fold bookkeeping left out).
func drawSubset(trips []fit.Triplet, js []fit.Judgment, sources []int) ([]fit.Triplet, []fit.Judgment) {
	bySource := map[int][]int{}
	for i, t := range trips {
		bySource[t.Source] = append(bySource[t.Source], i)
	}
	byTriplet := make([][]fit.Judgment, len(trips))
	for _, k := range js {
		byTriplet[k.Triplet] = append(byTriplet[k.Triplet], k)
	}
	var ot []fit.Triplet
	var oj []fit.Judgment
	for c, s := range sources {
		for _, i := range bySource[s] {
			t := trips[i]
			t.Source = c
			idx := len(ot)
			ot = append(ot, t)
			for _, k := range byTriplet[i] {
				k.Triplet = idx
				oj = append(oj, k)
			}
		}
	}
	return ot, oj
}

// TestDiagBias fits the null and alt worlds at large n with the pure model (σ = 0, no random answers) and with the
// reference judges, and prints the normalized estimates against the truth.
func TestDiagBias(t *testing.T) {
	if os.Getenv("GCJ_DIAG") == "" {
		t.Skip("GCJ_DIAG=1")
	}
	for _, c := range []struct {
		world         string
		sigma, random float64
		sources       int
	}{{"null", 0, 0, 2000}, {"alt", 0, 0, 2000}, {"alt", 1, 0.1, 2000}} {
		trips, js, cfg := diagWorld(t, c.world, c.sources, 11, c.sigma, c.random)
		opts := fit.Options{Judges: 3, Theta0: ged.Current(), FitNeg: true, Tie: fit.TypeTie(), MaxOuter: 20}
		start := time.Now()
		res, err := fit.Fit(trips, js, opts, nil)
		if err != nil {
			t.Fatal(err)
		}
		truth := append(cfg.True[:], cfg.TrueNeg)
		tN, sT := fit.Normalize(truth, nil)
		eN, sE := fit.NormalizeParams(res.Params, nil)
		t.Logf("%s σ=%g lapse=%g: %d triplets, %s, outer %d conv %v cycled %v; ŝ %.3f (true %.3f)", c.world, c.sigma, c.random, len(trips), time.Since(start), res.Outer, res.Converged, res.Cycled, sE, sT)
		for _, p := range []int{ged.PNodeMissing, ged.PNodeExtra, ged.PEdgeMissing, fit.NormEdgeExtra, ged.PTypeRAMA, ged.PDirection, fit.NormNeg} {
			t.Logf("  %-14s truth %.3f est %.3f (anchored est %.3f truth %.3f) diff %+.3f", fit.NormName(p), tN[p], eN[p], anchoredAt(res.Params, p), anchoredTruth(truth, p), eN[p]-tN[p])
		}
		if c.sigma > 0 {
			mix, err := fit.FitMixed(trips, js, opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			mN, _ := fit.NormalizeParams(mix.Params, nil)
			t.Logf("  mixed σ̂ %.3f ℓ̂ %v", mix.Params.Sigma, mix.Params.Lapse)
			for _, p := range []int{ged.PNodeMissing, ged.PNodeExtra, ged.PEdgeMissing, fit.NormEdgeExtra, ged.PTypeRAMA, ged.PDirection} {
				t.Logf("  mixed %-14s truth %.3f est %.3f diff %+.3f", fit.NormName(p), tN[p], mN[p], mN[p]-tN[p])
			}
		}
		_ = math.Abs
	}
}

// TestDiagBias750 repeats the P1 configuration (750 sources, σ = 1, 10% random answers) over seeds 1–5 for the
// null and alt worlds and prints the mean error of the normalized estimates (both fits).
func TestDiagBias750(t *testing.T) {
	if os.Getenv("GCJ_DIAG") == "" {
		t.Skip("GCJ_DIAG=1")
	}
	show := []int{ged.PNodeMissing, ged.PNodeExtra, ged.PEdgeMissing, fit.NormEdgeExtra, ged.PTypeRAMA, ged.PDirection}
	for _, world := range []string{"null", "alt"} {
		var sumI, sumM [fit.NormLen]float64
		var sumS [2]float64
		n := 0
		for seed := uint64(1); seed <= 5; seed++ {
			trips, js, cfg := diagWorld(t, world, 750, seed, 1, 0.1)
			opts := fit.Options{Judges: 3, Theta0: ged.Current(), FitNeg: true, Tie: fit.TypeTie(), MaxOuter: 20}
			ind, err := fit.Fit(trips, js, opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			mix, err := fit.FitMixed(trips, js, opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			tN, sT := fit.Normalize(append(cfg.True[:], cfg.TrueNeg), nil)
			iN, sI := fit.NormalizeParams(ind.Params, nil)
			mN, sM := fit.NormalizeParams(mix.Params, nil)
			t.Logf("%s seed %d: indep outer %d conv %v ŝ %.3f RA↔MA %+.3f; mixed outer %d conv %v ŝ %.3f RA↔MA %+.3f (ŝ_true %.3f)",
				world, seed, ind.Outer, ind.Converged, sI, iN[ged.PTypeRAMA]-tN[ged.PTypeRAMA], mix.Outer, mix.Converged, sM, mN[ged.PTypeRAMA]-tN[ged.PTypeRAMA], sT)
			for _, p := range show {
				sumI[p] += iN[p] - tN[p]
				sumM[p] += mN[p] - tN[p]
			}
			sumS[0] += sI - sT
			sumS[1] += sM - sT
			n++
		}
		t.Logf("%s mean error over %d seeds (indep | mixed): ŝ %+.3f | %+.3f", world, n, sumS[0]/float64(n), sumS[1]/float64(n))
		for _, p := range show {
			t.Logf("  %-14s %+.3f | %+.3f", fit.NormName(p), sumI[p]/float64(n), sumM[p]/float64(n))
		}
	}
}

func anchoredAt(p fit.Params, i int) float64 {
	switch {
	case i < ged.NumParams:
		return p.Theta[i]
	case i == fit.NormEdgeExtra:
		return 1
	}
	return p.ThetaNeg
}

func anchoredTruth(v []float64, i int) float64 {
	switch {
	case i < ged.NumParams:
		return v[i]
	case i == fit.NormEdgeExtra:
		return 1
	}
	return v[ged.NumParams]
}
