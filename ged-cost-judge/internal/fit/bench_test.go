package fit

import (
	"os"
	"testing"
	"time"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
)

// TestTimingFullSize profiles the full-size analysis (3,000 triplets, 3 judges × 2 orders) with a reduced B.
// It runs only with GCJ_TIMING=1 so the ordinary suite stays short.
func TestTimingFullSize(t *testing.T) {
	if os.Getenv("GCJ_TIMING") == "" {
		t.Skip("set GCJ_TIMING=1 to run")
	}
	w := newWorld(21, altTheta(), 1)
	w.neg = 1.5
	trips, js := w.generate(750, true)
	opts := baseOpts(3)
	opts.Tie = TypeTie()
	t0 := time.Now()
	res, err := FitMulti(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("multi-start fit (3 starts): %s, outer %d, spread %.3f", time.Since(t0), res.Outer, res.StartSpread)
	t0 = time.Now()
	mix, err := FitMixed(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("mixed fit: %s, σ̂ %.3f", time.Since(t0), mix.Params.Sigma)
	const B = 50
	t0 = time.Now()
	br, err := BootstrapOpts(trips, js, opts, res.Params, BootOptions{Reps: B, Seed: 3, Mixed: true, MixedInit: &mix.Params})
	if err != nil {
		t.Fatal(err)
	}
	el := time.Since(t0)
	t.Logf("paired bootstrap B=%d (both fits): %s → B=1000 ≈ %s", B, el, el*1000/B)
	sh := Summarise(br.Meta)
	t.Logf("indep shares: cycled %.2f capped %.2f notconv %.2f", sh.Cycled, sh.Capped, sh.NotConverged)
	t0 = time.Now()
	_, err = NestedD(trips, js, opts, ged.Current(), nil, DOptions{Reps: 4, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	el = time.Since(t0)
	t.Logf("nested D, 4 reps: %s → 200 reps ≈ %s", el, el*200/4)
}
