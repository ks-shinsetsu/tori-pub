package fit_test

import (
	"math"
	"os"
	"testing"
	"time"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/simulate"
)

// Diagnostics on the P1 simulator (GCJ_DIAG=1): convergence trace of the alternating loop and the bias of the
// tied RA↔MA estimate. Not part of the ordinary suite.

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
	trips, js, _ := diagWorld(t, "null", 750, 1, 1, 0.1)
	opts := fit.Options{Judges: 3, Theta0: ged.Current(), FitNeg: true, Tie: fit.TypeTie(), MaxOuter: 20}
	opts.Trace = func(r fit.RoundTrace) {
		t.Logf("round %2d inner %.4f true %.4f changed %4d maxShift %.2e sumShift %.2e iters %d conv %v θ=%.3f", r.Outer, r.Inner, r.True, r.Changed, r.MaxDeltaShift, r.SumDeltaShift, r.InnerIters, r.InnerConv, r.Theta)
	}
	start := time.Now()
	res, err := fit.Fit(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("independence: outer %d conv %v cycled %v capped %v in %s", res.Outer, res.Converged, res.Cycled, res.Capped, time.Since(start))
	start = time.Now()
	mix, err := fit.FitMixed(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("mixed: outer %d conv %v cycled %v capped %v σ̂ %.3f in %s", mix.Outer, mix.Converged, mix.Cycled, mix.Capped, mix.Params.Sigma, time.Since(start))
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
