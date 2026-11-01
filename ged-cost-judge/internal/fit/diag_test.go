package fit_test

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
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

// TestDiagA5Cycles reproduces the independence bootstrap of Confirmatory for the A.5 null runs (GCJ_DIAG_SEEDS, default
// 104; GCJ_DIAG_B, default 200; GCJ_DIAG_MIXED=1 adds the random-intercept replicates) and traces every cycling
// replicate: cycle length, CycleSpread and its widest coordinate, the objective range over the cycle, the flipping
// triplets with their Δ jumps and node-cost margins, against the bootstrap SD.
func TestDiagA5Cycles(t *testing.T) {
	if os.Getenv("GCJ_DIAG") == "" {
		t.Skip("GCJ_DIAG=1")
	}
	reps := 200
	if b, err := strconv.Atoi(os.Getenv("GCJ_DIAG_B")); err == nil && b > 0 {
		reps = b
	}
	seeds := []uint64{104}
	if s := os.Getenv("GCJ_DIAG_SEEDS"); s != "" {
		seeds = nil
		for _, x := range strings.Split(s, ",") {
			v, err := strconv.ParseUint(x, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			seeds = append(seeds, v)
		}
	}
	th0 := ged.Current()
	names := append([]string{}, ged.ParamNames[:]...)
	names = append(names, "θ_neg")
	for _, seed := range seeds {
		trips, js, _ := diagWorld(t, "null", 750, seed, 1, 0.1)
		opts := fit.Options{Judges: 3, Theta0: th0, Tie: fit.TypeTie(), MaxOuter: 20, FitNeg: true}
		ft, fj, _ := fit.Filter(trips, js, opts)
		opts.IncludeAll = true
		// Full-data multi-start fit as Confirmatory runs it (θ₀, R1, all-0.5 + 2 random starts seeded seed+7).
		fo := opts
		fo.Starts, fo.StartNames = fit.DefaultStarts(th0)
		fo.Starts = append(fo.Starts, fit.RandomStarts(2, seed+7)...)
		res, err := fit.Fit(ft, fj, fo, nil)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		br, err := fit.BootstrapOpts(ft, fj, opts, res.Params, fit.BootOptions{Reps: reps, Seed: seed + 100, Mixed: os.Getenv("GCJ_DIAG_MIXED") != ""})
		if err != nil {
			t.Fatal(err)
		}
		sh := fit.Summarise(br.Meta)
		t.Logf("seed %d: %d fit triplets; full fit outer %d valid %v spread %.5f; bootstrap B=%d in %s: cycled %.3f capped %.3f invalid %.3f cold %d",
			seed, len(ft), res.Outer, res.Valid(), res.StartSpread, reps, time.Since(start), sh.Cycled, sh.Capped, sh.Invalid, sh.Cold)
		if br.MixedMeta != nil {
			shm := fit.Summarise(br.MixedMeta)
			t.Logf("seed %d mixed: cycled %.3f capped %.3f invalid %.3f", seed, shm.Cycled, shm.Capped, shm.Invalid)
		}
		// Bootstrap SD per anchored coordinate and on the normalized scale.
		var sdA, sdN [fit.NormLen]float64
		normRows, _ := fit.NormalizeRows(br.Theta, nil)
		for q := range fit.NormLen {
			if q < fit.NormEdgeExtra {
				sdA[q] = fit.SD(fit.Column(br.Theta, q))
			} else if q == fit.NormNeg {
				sdA[q] = fit.SD(fit.Column(br.Theta, ged.NumParams))
			}
			sdN[q] = fit.SD(fit.Column(normRows, q))
		}
		warm := res.Params.Clone()
		for p := range ged.NumParams {
			if res.Free[p] && warm.Theta[p] < 0.05 {
				warm.Theta[p] = 0.05
			}
		}
		for p, m := range opts.Tie {
			warm.Theta[p] = warm.Theta[m]
		}
		var spreads []float64
		flipCount := map[int]int{}
		flipJump := map[int]float64{}
		exactTies, flips := 0, 0
		cycledCold := 0
		for r, m := range br.Meta {
			if !m.Cycled {
				continue
			}
			spreads = append(spreads, m.CycleSpread)
			bt, bj := drawSubset(ft, fj, br.Draws[r])
			var rounds [][]float64
			var sigs []string
			var trues []float64
			nFlip, maxJump, ties := 0, 0.0, 0
			prev := fit.ThetaVec(warm)
			o := opts
			if m.Cold {
				cycledCold++
				o.Starts = []ged.Theta{th0, func() ged.Theta {
					var h ged.Theta
					for i := range h {
						h[i] = 0.5
					}
					return h
				}()}
				prev = fit.ThetaVec(fit.Init(o))
			}
			o.Trace = func(tr fit.RoundTrace) {
				var prevTh ged.Theta
				copy(prevTh[:], prev[:ged.NumParams])
				for _, i := range tr.Flipped {
					x := bt[i]
					for _, v := range []struct {
						g ged.Graph
						s ged.Sim
					}{{x.A, x.SimA}, {x.B, x.SimB}} {
						old, _ := ged.Compute(x.Ref, v.g, v.s, prevTh)
						nw, _ := ged.Compute(x.Ref, v.g, v.s, tr.Theta)
						if old.Signature() == nw.Signature() {
							continue
						}
						margin := nodePart(nw, tr.Theta) - nodePart(old, tr.Theta)
						jump := math.Abs(nw.Cost(tr.Theta) - old.Cost(tr.Theta))
						nFlip++
						flips++
						if margin == 0 {
							ties++
							exactTies++
						}
						maxJump = math.Max(maxJump, jump)
						flipCount[br.Draws[r][x.Source]]++ // original source index (x.Source is the draw position)
						flipJump[br.Draws[r][x.Source]] = math.Max(flipJump[br.Draws[r][x.Source]], jump)
					}
				}
				s, _ := fit.Signatures(bt, tr.Theta)
				sigs = append(sigs, strings.Join(s, "|"))
				rounds = append(rounds, append([]float64{}, fit.ThetaVec(fit.Params{Theta: tr.Theta})[:ged.NumParams]...))
				trues = append(trues, tr.True)
				prev = fit.ThetaVec(fit.Params{Theta: tr.Theta})
			}
			var rr fit.Result
			if m.Cold {
				rr, err = fit.Fit(bt, bj, o, nil)
			} else {
				rr, err = fit.Fit(bt, bj, o, &warm)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Cycle length from the per-round matchings (last round's matching equals an earlier one).
			cycleLen, first := 0, -1
			if n := len(sigs); n > 0 {
				for k := 0; k < n-1; k++ {
					if sigs[k] == sigs[n-1] {
						first = k
						break
					}
				}
				if first >= 0 {
					cycleLen = n - 1 - first
				}
			}
			widest, wmax := -1, 0.0
			if first >= 0 {
				for q := range ged.NumParams {
					lo, hi := math.Inf(1), math.Inf(-1)
					for _, v := range rounds[first+1:] {
						lo, hi = math.Min(lo, v[q]), math.Max(hi, v[q])
					}
					if hi-lo > wmax {
						widest, wmax = q, hi-lo
					}
				}
			}
			objLo, objHi := math.Inf(1), math.Inf(-1)
			if first >= 0 {
				for _, v := range trues[first+1:] {
					objLo, objHi = math.Min(objLo, v), math.Max(objHi, v)
				}
			}
			wname, wsd, wsdN := "?", math.NaN(), math.NaN()
			if widest >= 0 {
				wname, wsd, wsdN = names[widest], sdA[widest], sdN[widest]
			}
			t.Logf("  rep %3d cold %v: outer %d (refit %d) cycle len %d spread %.4f (refit widest %s %.4f; boot SD anchored %.4f normalized %.4f) obj range %.2f flips %d max|Δjump| %.2f exact-tie flips %d valid %v",
				r, m.Cold, m.Outer, rr.Outer, cycleLen, m.CycleSpread, wname, wmax, wsd, wsdN, objHi-objLo, nFlip, maxJump, ties, m.Valid())
		}
		sort.Float64s(spreads)
		q := func(p float64) float64 { return fit.Quantile(spreads, p) }
		if len(spreads) > 0 {
			t.Logf("seed %d: %d cycled replicates (%d cold); CycleSpread quantiles 50%% %.4f 75%% %.4f 90%% %.4f max %.4f; share ≥ 0.01: %.3f, ≥ 0.02: %.3f, ≥ 0.03: %.3f, ≥ 0.05: %.3f, ≥ 0.10: %.3f (of B)",
				seed, len(spreads), cycledCold, q(0.5), q(0.75), q(0.9), spreads[len(spreads)-1],
				shareGE(spreads, 0.01, reps), shareGE(spreads, 0.02, reps), shareGE(spreads, 0.03, reps), shareGE(spreads, 0.05, reps), shareGE(spreads, 0.10, reps))
		}
		t.Logf("seed %d: flips %d, of which exact node-cost ties %d; bootstrap SD anchored: I-miss %.4f I-extra %.4f E-miss %.4f RA↔MA %.4f dir %.4f neg %.4f knots %.4f/%.4f/%.4f/%.4f",
			seed, flips, exactTies, sdA[ged.PNodeMissing], sdA[ged.PNodeExtra], sdA[ged.PEdgeMissing], sdA[ged.PTypeRAMA], sdA[ged.PDirection], sdA[fit.NormNeg], sdA[ged.PSub09], sdA[ged.PSub07], sdA[ged.PSub05], sdA[ged.PSub03])
		type kv struct {
			src, n int
			jump   float64
		}
		var top []kv
		for s, n := range flipCount {
			top = append(top, kv{s, n, flipJump[s]})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n })
		for i := 0; i < len(top) && i < 6; i++ {
			t.Logf("seed %d: source %d flips in %d replicate-rounds, max |Δ jump| %.2f", seed, top[i].src, top[i].n, top[i].jump)
		}
	}
}

// TestDiagA5One traces one bootstrap replicate (GCJ_DIAG_SEEDS first entry, GCJ_DIAG_REP, default 8) of the A.5 null
// run round by round: θ, objective, flips, and the cos structure of the first tied swaps.
func TestDiagA5One(t *testing.T) {
	if os.Getenv("GCJ_DIAG") == "" {
		t.Skip("GCJ_DIAG=1")
	}
	seed := uint64(104)
	if s := os.Getenv("GCJ_DIAG_SEEDS"); s != "" {
		v, _ := strconv.ParseUint(strings.Split(s, ",")[0], 10, 64)
		seed = v
	}
	rep := 8
	if s := os.Getenv("GCJ_DIAG_REP"); s != "" {
		rep, _ = strconv.Atoi(s)
	}
	th0 := ged.Current()
	trips, js, _ := diagWorld(t, "null", 750, seed, 1, 0.1)
	opts := fit.Options{Judges: 3, Theta0: th0, Tie: fit.TypeTie(), MaxOuter: 20, FitNeg: true}
	ft, fj, _ := fit.Filter(trips, js, opts)
	opts.IncludeAll = true
	fo := opts
	fo.Starts, fo.StartNames = fit.DefaultStarts(th0)
	fo.Starts = append(fo.Starts, fit.RandomStarts(2, seed+7)...)
	res, err := fit.Fit(ft, fj, fo, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("seed %d full fit θ̂ = %.4f neg %.4f (outer %d valid %v)", seed, res.Params.Theta, res.Params.ThetaNeg, res.Outer, res.Valid())
	br, err := fit.BootstrapOpts(ft, fj, opts, res.Params, fit.BootOptions{Reps: rep + 1, Seed: seed + 100})
	if err != nil {
		t.Fatal(err)
	}
	m := br.Meta[rep]
	t.Logf("rep %d: cold %v outer %d cycled %v spread %.4f valid %v", rep, m.Cold, m.Outer, m.Cycled, m.CycleSpread, m.Valid())
	warm := res.Params.Clone()
	for p := range ged.NumParams {
		if res.Free[p] && warm.Theta[p] < 0.05 {
			warm.Theta[p] = 0.05
		}
	}
	for p, mm := range opts.Tie {
		warm.Theta[p] = warm.Theta[mm]
	}
	t.Logf("warm start θ = %.4f", warm.Theta)
	bt, bj := drawSubset(ft, fj, br.Draws[rep])
	prev := warm.Theta
	o := opts
	o.Trace = func(tr fit.RoundTrace) {
		t.Logf("round %d: true %.3f inner %.3f changed %d maxShift %.2f θ = %.4f", tr.Outer, tr.True, tr.Inner, tr.Changed, tr.MaxDeltaShift, tr.Theta)
		ties, kinks, shown := 0, 0, 0
		for _, i := range tr.Flipped {
			x := bt[i]
			for side, v := range []struct {
				g ged.Graph
				s ged.Sim
			}{{x.A, x.SimA}, {x.B, x.SimB}} {
				old, _ := ged.Compute(x.Ref, v.g, v.s, prev)
				nw, _ := ged.Compute(x.Ref, v.g, v.s, tr.Theta)
				if old.Signature() == nw.Signature() {
					continue
				}
				margin := nodePart(nw, tr.Theta) - nodePart(old, tr.Theta)
				if margin == 0 {
					ties++
				} else {
					kinks++
				}
				if shown >= 6 {
					continue
				}
				shown++
				// cos of the pairs that changed, in canonical order
				vp := append([]ged.Prop{}, v.g.Props...)
				rp := append([]ged.Prop{}, x.Ref.Props...)
				sort.Slice(vp, func(a, b int) bool { return vp[a].ID < vp[b].ID })
				sort.Slice(rp, func(a, b int) bool { return rp[a].ID < rp[b].ID })
				var desc []string
				for k := range vp {
					if old.Assign[k] == nw.Assign[k] {
						continue
					}
					oc, nc := -1.0, -1.0
					if old.Assign[k] >= 0 {
						oc = v.s(rp[old.Assign[k]].ID, vp[k].ID)
					}
					if nw.Assign[k] >= 0 {
						nc = v.s(rp[nw.Assign[k]].ID, vp[k].ID)
					}
					desc = append(desc, fmt.Sprintf("%s: cos %.3f→%.3f", vp[k].ID, oc, nc))
				}
				t.Logf("   triplet %d side %d: margin %+.4f jump %+.2f edges(old→new) miss %.0f→%.0f extra %.0f→%.0f; %s", i, side, margin,
					nw.Cost(tr.Theta)-old.Cost(tr.Theta), old.Phi[ged.PEdgeMissing], nw.Phi[ged.PEdgeMissing], old.EdgeExtra, nw.EdgeExtra, strings.Join(desc, "; "))
			}
		}
		t.Logf("   flips: exact ties %d, non-zero margin %d", ties, kinks)
		prev = tr.Theta
	}
	rr, err := fit.Fit(bt, bj, o, &warm)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("refit: outer %d cycled %v spread %.4f returned θ = %.4f", rr.Outer, rr.Cycled, rr.CycleSpread, rr.Params.Theta)
}

func shareGE(sorted []float64, tol float64, total int) float64 {
	n := 0
	for _, v := range sorted {
		if v >= tol {
			n++
		}
	}
	return float64(n) / float64(total)
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
