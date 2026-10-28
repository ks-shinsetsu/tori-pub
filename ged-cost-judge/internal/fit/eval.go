package fit

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"sort"
	"sync"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
)

// Majority is the majority judgment per triplet (ok = false on a tie).
func Majority(n int, js []Judgment) (maj []Answer, ok []bool) {
	counts := make([][3]int, n)
	for _, k := range js {
		counts[k.Triplet][k.Answer]++
	}
	maj = make([]Answer, n)
	ok = make([]bool, n)
	for t, c := range counts {
		best, top, tied := 0, -1, false
		for a, v := range c {
			switch {
			case v > top:
				best, top, tied = a, v, false
			case v == top:
				tied = true
			}
		}
		maj[t], ok[t] = Answer(best), top > 0 && !tied
	}
	return maj, ok
}

// Agreement is the fraction where the sign of Δ agrees with the majority judgment (A / B) (plan §7.3).
// Triplets whose majority is "about the same" or has no majority are excluded. Δ = 0 counts as 0.5.
// If only is non-nil, only triplets with only[t] true are counted.
func Agreement(delta []float64, js []Judgment, only []bool) (rate float64, n int) {
	maj, ok := Majority(len(delta), js)
	hit := 0.0
	for t, d := range delta {
		if !ok[t] || maj[t] == AnswerTie || (only != nil && !only[t]) {
			continue
		}
		n++
		switch {
		case math.Abs(d) < 1e-9:
			hit += 0.5
		case (d > 0) == (maj[t] == AnswerA):
			hit++
		}
	}
	if n == 0 {
		return math.NaN(), 0
	}
	return hit / float64(n), n
}

// Correct returns, per triplet, whether sign(Δ) matches the clear A/B majority (eligible[t] false when there is
// no clear A/B majority or only[t] is false). Δ = 0 counts as wrong here (no half credit), for McNemar-type counts.
func Correct(delta []float64, js []Judgment, only []bool) (right, eligible []bool) {
	maj, ok := Majority(len(delta), js)
	right = make([]bool, len(delta))
	eligible = make([]bool, len(delta))
	for t, d := range delta {
		if !ok[t] || maj[t] == AnswerTie || (only != nil && !only[t]) {
			continue
		}
		eligible[t] = true
		right[t] = math.Abs(d) >= 1e-9 && (d > 0) == (maj[t] == AnswerA)
	}
	return right, eligible
}

// subset rebuilds triplets and judgments from a list of source argument indices (duplicates allowed).
// orig[i] is the index in trips of the i-th triplet of the subset.
func subset(trips []Triplet, js []Judgment, sources []int) (ot []Triplet, oj []Judgment, orig []int) {
	bySource := map[int][]int{}
	for i, t := range trips {
		bySource[t.Source] = append(bySource[t.Source], i)
	}
	byTriplet := make([][]Judgment, len(trips))
	for _, k := range js {
		byTriplet[k.Triplet] = append(byTriplet[k.Triplet], k)
	}
	for c, s := range sources {
		for _, i := range bySource[s] {
			t := trips[i]
			t.Source = c // an argument drawn more than once is treated as a separate cluster
			idx := len(ot)
			ot = append(ot, t)
			orig = append(orig, i)
			for _, k := range byTriplet[i] {
				k.Triplet = idx
				oj = append(oj, k)
			}
		}
	}
	return ot, oj, orig
}

func sourceIDs(trips []Triplet) []int {
	seen := map[int]bool{}
	var out []int
	for _, t := range trips {
		if !seen[t.Source] {
			seen[t.Source] = true
			out = append(out, t.Source)
		}
	}
	slices.Sort(out)
	return out
}

// stratum is a set of sources sharing a generator (one stratum with name "" when generators are not recorded).
type stratum struct {
	name string
	ids  []int
}

// sourceStrata groups sources by generator (decisions §6: resampling within generator stratum).
func sourceStrata(trips []Triplet) []stratum {
	gen := map[int]string{}
	for _, t := range trips {
		if g, ok := gen[t.Source]; !ok || g == "" {
			gen[t.Source] = t.Generator
		}
	}
	byGen := map[string][]int{}
	for _, s := range sourceIDs(trips) {
		byGen[gen[s]] = append(byGen[gen[s]], s)
	}
	names := make([]string, 0, len(byGen))
	for g := range byGen {
		names = append(names, g)
	}
	sort.Strings(names)
	out := make([]stratum, 0, len(names))
	for _, g := range names {
		ids := byGen[g]
		slices.Sort(ids)
		out = append(out, stratum{g, ids})
	}
	return out
}

// drawSources resamples sources with replacement within every stratum.
func drawSources(strata []stratum, rng *rand.Rand) []int {
	var draw []int
	for _, s := range strata {
		for range s.ids {
			draw = append(draw, s.ids[rng.IntN(len(s.ids))])
		}
	}
	return draw
}

// ThetaVec returns the anchored vector [θ_0..θ_10, θ_neg] of p.
func ThetaVec(p Params) []float64 {
	return append(slices.Clone(p.Theta[:]), p.ThetaNeg)
}

// BootOptions configures the cluster bootstrap (decisions §6).
type BootOptions struct {
	Reps int
	Seed uint64
	// ColdShare is the share of replicates started cold from θ₀ and all-0.5 instead of the warm start (0 → 0.05; < 0 → none).
	ColdShare float64
	// Interior is the floor applied to the warm start (every θ_p below it is raised to it; 0 → 0.05).
	Interior float64
	// Mixed also runs FitMixed on the same draws (paired co-primary bootstrap).
	Mixed bool
	// MixedInit is the full-data random-intercept estimate (nil → derived from est with σ = 1).
	MixedInit *Params
	// Workers bounds the parallel replicates (0 → GOMAXPROCS).
	Workers int
}

// RepMeta is the per-replicate record of decisions §6.
type RepMeta struct {
	Outer     int
	Converged bool
	Cycled    bool
	Capped    bool
	Cold      bool
	Boundary  [ged.NumParams]bool
	Divergent [ged.NumParams]bool
	NegLogLik float64
	Sigma     float64
	Err       string
}

// BootResult holds the replicates of the paired bootstrap.
type BootResult struct {
	// Theta[r] is ThetaVec of replicate r (working independence, anchored scale).
	Theta [][]float64
	Meta  []RepMeta
	// MixedTheta, MixedMeta are the random-intercept replicates on the same draws (nil unless Mixed).
	MixedTheta [][]float64
	MixedMeta  []RepMeta
	// Draws[r] is the resampled source list of replicate r.
	Draws [][]int
}

// Shares summarises the replicate metadata per cost: boundary and divergent shares, and overall cycled/capped/non-converged shares.
type Shares struct {
	Boundary     [ged.NumParams]float64
	Divergent    [ged.NumParams]float64
	Cycled       float64
	Capped       float64
	NotConverged float64
	Cold         int
}

// Summarise computes Shares from replicate metadata.
func Summarise(meta []RepMeta) Shares {
	var s Shares
	if len(meta) == 0 {
		return s
	}
	n := float64(len(meta))
	for _, m := range meta {
		for p := range ged.NumParams {
			if m.Boundary[p] {
				s.Boundary[p]++
			}
			if m.Divergent[p] {
				s.Divergent[p]++
			}
		}
		if m.Cycled {
			s.Cycled++
		}
		if m.Capped {
			s.Capped++
		}
		if !m.Converged {
			s.NotConverged++
		}
		if m.Cold {
			s.Cold++
		}
	}
	for p := range ged.NumParams {
		s.Boundary[p] /= n
		s.Divergent[p] /= n
	}
	s.Cycled /= n
	s.Capped /= n
	s.NotConverged /= n
	return s
}

func metaOf(r Result, cold bool) RepMeta {
	return RepMeta{Outer: r.Outer, Converged: r.Converged, Cycled: r.Cycled, Capped: r.Capped, Cold: cold,
		Boundary: r.Boundary, Divergent: r.Divergent, NegLogLik: r.NegLogLik, Sigma: r.Params.Sigma}
}

// interior raises every free cost below floor to floor (interior restart, review B1).
func interior(p Params, opts Options, floor float64) Params {
	q := clone(p)
	free := FreeTheta(opts)
	for i := range ged.NumParams {
		if free[i] && q.Theta[i] < floor {
			q.Theta[i] = floor
		}
	}
	for i, m := range opts.Tie {
		q.Theta[i] = q.Theta[m]
	}
	return q
}

// Bootstrap is a cluster bootstrap over source arguments (plan §6.4) with the defaults of decisions §6.
// It returns the anchored estimates [θ..., θ_neg] of each round.
func Bootstrap(trips []Triplet, js []Judgment, opts Options, est Params, reps int, seed uint64) ([][]float64, error) {
	r, err := BootstrapOpts(trips, js, opts, est, BootOptions{Reps: reps, Seed: seed})
	if err != nil {
		return nil, err
	}
	return r.Theta, nil
}

// BootstrapOpts is the stratified cluster bootstrap of decisions §6: sources resampled within generator,
// warm start from est with the interior floor, a cold multi-start (θ₀ and all-0.5, best kept) for a random share of
// replicates, per-replicate metadata, and (optionally) the random-intercept fit on the same draws.
func BootstrapOpts(trips []Triplet, js []Judgment, opts Options, est Params, bo BootOptions) (BootResult, error) {
	if bo.Reps <= 0 {
		return BootResult{}, errors.New("bootstrap: Reps must be positive")
	}
	if opts.Weights != nil {
		if len(opts.Weights) != len(trips) {
			return BootResult{}, fmt.Errorf("bootstrap: len(Weights) = %d, want %d", len(opts.Weights), len(trips))
		}
		trips = slices.Clone(trips)
		for i := range trips {
			trips[i].Weight = opts.Weights[i]
		}
		opts.Weights = nil
	}
	cold := bo.ColdShare
	if cold == 0 {
		cold = 0.05
	}
	floor := bo.Interior
	if floor <= 0 {
		floor = 0.05
	}
	opts.Starts, opts.StartNames = nil, nil
	warm := interior(est, opts, floor)
	var warmMixed Params
	if bo.Mixed {
		if bo.MixedInit != nil {
			warmMixed = interior(*bo.MixedInit, opts, floor)
		} else {
			warmMixed = warm
			warmMixed.Sigma = 1
		}
	}
	coldOpts := opts
	var half ged.Theta
	for i := range half {
		half[i] = 0.5
	}
	coldOpts.Starts, coldOpts.StartNames = []ged.Theta{opts.Theta0, half}, []string{"θ₀", "all-0.5"}

	strata := sourceStrata(trips)
	out := BootResult{Theta: make([][]float64, bo.Reps), Meta: make([]RepMeta, bo.Reps), Draws: make([][]int, bo.Reps)}
	if bo.Mixed {
		out.MixedTheta = make([][]float64, bo.Reps)
		out.MixedMeta = make([]RepMeta, bo.Reps)
	}
	// Draws and the cold assignment are made sequentially so they do not depend on scheduling.
	isCold := make([]bool, bo.Reps)
	for r := range bo.Reps {
		rng := rand.New(rand.NewPCG(bo.Seed, uint64(r)))
		out.Draws[r] = drawSources(strata, rng)
		isCold[r] = cold > 0 && rng.Float64() < cold
	}
	workers := bo.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	errs := make([]error, bo.Reps)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for r := range bo.Reps {
		wg.Add(1)
		sem <- struct{}{}
		go func(r int) {
			defer func() { <-sem; wg.Done() }()
			bt, bj, _ := subset(trips, js, out.Draws[r])
			o := opts
			if isCold[r] {
				o = coldOpts
			}
			res, err := Fit(bt, bj, o, &warm)
			if err != nil {
				errs[r] = err
				out.Meta[r].Err = err.Error()
				return
			}
			out.Theta[r] = ThetaVec(res.Params)
			out.Meta[r] = metaOf(res, isCold[r])
			if bo.Mixed {
				mr, err := FitMixed(bt, bj, o, &warmMixed)
				if err != nil {
					errs[r] = err
					out.MixedMeta[r].Err = err.Error()
					return
				}
				out.MixedTheta[r] = ThetaVec(mr.Params)
				out.MixedMeta[r] = metaOf(mr, isCold[r])
			}
		}(r)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// CVOptions configures cross-validation (decisions §7).
type CVOptions struct {
	K       int    // folds (0 → 5)
	Repeats int    // repeated partitions (0 → 1)
	Seed    uint64 // seed of the first repeat; repeat i uses Seed + i
	Mixed   bool   // fit the random-intercept model in the folds instead of working independence
}

// CVResult holds out-of-fold quantities per repeat.
type CVResult struct {
	// Delta[rep][t] is Δ at the θ fitted without t's source.
	Delta [][]float64
	// LogLoss[rep][k] is −log P(answer_k) under the fold's parameters (NaN for judgments the filters drop).
	LogLoss [][]float64
	// Fold[rep][t] is the fold index of triplet t.
	Fold [][]int
	// Fits[rep][f] is the fold fit.
	Fits [][]Result
}

// MeanLogLoss is the mean OOF log loss per judgment over the non-NaN entries of repeat rep (NaN when empty).
func (r CVResult) MeanLogLoss(rep int) float64 {
	s, n := 0.0, 0
	for _, v := range r.LogLoss[rep] {
		if !math.IsNaN(v) {
			s += v
			n++
		}
	}
	if n == 0 {
		return math.NaN()
	}
	return s / float64(n)
}

// assignFolds assigns every source to a fold, stratified by generator (shuffled within stratum).
func assignFolds(trips []Triplet, k int, rng *rand.Rand) map[int]int {
	fold := map[int]int{}
	offset := 0
	for _, s := range sourceStrata(trips) {
		ids := slices.Clone(s.ids)
		rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
		for i, id := range ids {
			fold[id] = (i + offset) % k
		}
		offset = (offset + len(ids)) % k
	}
	return fold
}

// CrossValidate is k-fold cross-validation over source arguments. It returns Δ on triplets not used for training.
func CrossValidate(trips []Triplet, js []Judgment, opts Options, k int, seed uint64) ([]float64, error) {
	r, err := CrossValidateOpts(trips, js, opts, CVOptions{K: k, Seed: seed})
	if err != nil {
		return nil, err
	}
	return r.Delta[0], nil
}

// CrossValidateOpts is grouped (by source), generator-stratified, repeated k-fold CV. For a fixed rule set
// opts.FixTheta and opts.Theta0 to the rule: the auxiliary parameters are then fitted in-fold and the OOF log loss
// is comparable across rules (decisions §7).
func CrossValidateOpts(trips []Triplet, js []Judgment, opts Options, cvo CVOptions) (CVResult, error) {
	if cvo.K <= 1 {
		cvo.K = 5
	}
	if cvo.Repeats <= 0 {
		cvo.Repeats = 1
	}
	if opts.Weights != nil {
		if len(opts.Weights) != len(trips) {
			return CVResult{}, fmt.Errorf("cv: len(Weights) = %d, want %d", len(opts.Weights), len(trips))
		}
		trips = slices.Clone(trips)
		for i := range trips {
			trips[i].Weight = opts.Weights[i]
		}
		opts.Weights = nil
	}
	_, keepJ := filterMasks(trips, js, opts)
	var out CVResult
	for rep := range cvo.Repeats {
		rng := rand.New(rand.NewPCG(cvo.Seed+uint64(rep), 7))
		fold := assignFolds(trips, cvo.K, rng)
		delta := make([]float64, len(trips))
		ll := make([]float64, len(js))
		for i := range ll {
			ll[i] = math.NaN()
		}
		foldOf := make([]int, len(trips))
		for i, t := range trips {
			foldOf[i] = fold[t.Source]
		}
		var fits []Result
		for f := range cvo.K {
			var train []int
			for _, s := range sourceIDs(trips) {
				if fold[s] != f {
					train = append(train, s)
				}
			}
			tt, tj, _ := subset(trips, js, train)
			var res Result
			var err error
			if cvo.Mixed {
				res, err = FitMixed(tt, tj, opts, nil)
			} else {
				res, err = Fit(tt, tj, opts, nil)
			}
			if err != nil {
				return out, fmt.Errorf("cv: repeat %d fold %d: %w", rep, f, err)
			}
			fits = append(fits, res)
			var test []Triplet
			var idx []int
			for i, t := range trips {
				if foldOf[i] == f {
					test = append(test, t)
					idx = append(idx, i)
				}
			}
			if len(test) == 0 {
				continue
			}
			d, err := Delta(test, res.Params.Theta)
			if err != nil {
				return out, err
			}
			pos := map[int]int{}
			for i, v := range d {
				delta[idx[i]] = v
				pos[idx[i]] = i
			}
			var tjs []Judgment
			var tidx []int
			for ki, k := range js {
				if p, ok := pos[k.Triplet]; ok && keepJ[ki] {
					k.Triplet = p
					tjs = append(tjs, k)
					tidx = append(tidx, ki)
				}
			}
			lls, err := LogLik(test, tjs, res.Params)
			if err != nil {
				return out, err
			}
			for i, v := range lls {
				ll[tidx[i]] = -v
			}
		}
		out.Delta = append(out.Delta, delta)
		out.LogLoss = append(out.LogLoss, ll)
		out.Fold = append(out.Fold, foldOf)
		out.Fits = append(out.Fits, fits)
	}
	return out, nil
}

// filterMasks returns which triplets and judgments survive the filters of opts (without renumbering).
func filterMasks(trips []Triplet, js []Judgment, opts Options) (keepT, keepJ []bool) {
	_, _, kept := Filter(trips, js, opts)
	keepT = make([]bool, len(trips))
	for _, i := range kept {
		keepT[i] = true
	}
	keepJ = make([]bool, len(js))
	for i, k := range js {
		if k.Triplet < 0 || k.Triplet >= len(trips) || !keepT[k.Triplet] {
			continue
		}
		if opts.ExcludeSameFamily && k.Family != "" && trips[k.Triplet].VerifierFamily != "" &&
			equalFold(k.Family, trips[k.Triplet].VerifierFamily) {
			continue
		}
		keepJ[i] = true
	}
	return keepT, keepJ
}

// DOptions configures the nested bootstrap for the D criterion (decisions §7).
type DOptions struct {
	Reps    int    // 0 → 200
	Seed    uint64 // seed of the point CV and of the replicates
	K       int    // folds (0 → 5)
	Workers int    // 0 → GOMAXPROCS
}

// DResult is the D criterion with its nested-bootstrap distribution.
type DResult struct {
	// Agree is agree(R2 OOF) − agree(R0) in percentage points on non-P-neg triplets with a clear A/B majority.
	Agree     float64
	AgreeBoot []float64
	// LogLoss is mean OOF log loss(R0, aux fitted in-fold) − mean OOF log loss(R2), nats per judgment (positive favours R2).
	LogLoss     float64
	LogLossBoot []float64
	// Strata holds the agreement form of D per triplet stratum label (when strata labels are given).
	Strata     map[string]float64
	StrataBoot map[string][]float64
	// N is the number of eligible triplets at the point estimate.
	N int
}

// dStats computes the agreement and log-loss forms of D on one dataset given the OOF Δ of R2, Δ at R0, and the OOF log losses.
func dStats(trips []Triplet, js []Judgment, d2, d0, ll2, ll0 []float64, labels []string) (agree, logLoss float64, n int, strata map[string]float64) {
	nonNeg := make([]bool, len(trips))
	for i, t := range trips {
		nonNeg[i] = t.NegDiff == 0
	}
	a2, n2 := Agreement(d2, js, nonNeg)
	a0, _ := Agreement(d0, js, nonNeg)
	agree = 100 * (a2 - a0)
	n = n2
	s, m := 0.0, 0
	for i := range ll2 {
		if math.IsNaN(ll2[i]) || math.IsNaN(ll0[i]) {
			continue
		}
		s += ll0[i] - ll2[i]
		m++
	}
	if m > 0 {
		logLoss = s / float64(m)
	} else {
		logLoss = math.NaN()
	}
	if labels != nil {
		strata = map[string]float64{}
		set := map[string]bool{}
		for _, l := range labels {
			set[l] = true
		}
		for l := range set {
			mask := make([]bool, len(trips))
			for i := range trips {
				mask[i] = nonNeg[i] && labels[i] == l
			}
			x2, _ := Agreement(d2, js, mask)
			x0, _ := Agreement(d0, js, mask)
			strata[l] = 100 * (x2 - x0)
		}
	}
	return agree, logLoss, n, strata
}

// NestedD computes D = agree(R2 OOF) − agree(R0) with a source-level, generator-stratified bootstrap in which the
// k-fold CV of R2 (opts) and the in-fold auxiliary fit of R0 (rule0, FixTheta) are repeated inside every replicate
// (review M7). labels, if non-nil, gives a stratum name per triplet for the per-stratum D.
func NestedD(trips []Triplet, js []Judgment, opts Options, rule0 ged.Theta, labels []string, do DOptions) (DResult, error) {
	if do.Reps <= 0 {
		do.Reps = 200
	}
	if do.K <= 1 {
		do.K = 5
	}
	if labels != nil && len(labels) != len(trips) {
		return DResult{}, fmt.Errorf("nested D: len(labels) = %d, want %d", len(labels), len(trips))
	}
	if opts.Weights != nil {
		if len(opts.Weights) != len(trips) {
			return DResult{}, fmt.Errorf("nested D: len(Weights) = %d, want %d", len(opts.Weights), len(trips))
		}
		trips = slices.Clone(trips)
		for i := range trips {
			trips[i].Weight = opts.Weights[i]
		}
		opts.Weights = nil
	}
	opts0 := opts
	opts0.FixTheta, opts0.Theta0, opts0.Starts, opts0.Lambda = true, rule0, nil, 0
	one := func(tr []Triplet, j []Judgment, lab []string, seed uint64) (float64, float64, int, map[string]float64, error) {
		cv2, err := CrossValidateOpts(tr, j, opts, CVOptions{K: do.K, Seed: seed})
		if err != nil {
			return 0, 0, 0, nil, err
		}
		cv0, err := CrossValidateOpts(tr, j, opts0, CVOptions{K: do.K, Seed: seed})
		if err != nil {
			return 0, 0, 0, nil, err
		}
		d0, err := Delta(tr, rule0)
		if err != nil {
			return 0, 0, 0, nil, err
		}
		a, l, n, s := dStats(tr, j, cv2.Delta[0], d0, cv2.LogLoss[0], cv0.LogLoss[0], lab)
		return a, l, n, s, nil
	}
	var out DResult
	var err error
	out.Agree, out.LogLoss, out.N, out.Strata, err = one(trips, js, labels, do.Seed)
	if err != nil {
		return out, err
	}
	strata := sourceStrata(trips)
	draws := make([][]int, do.Reps)
	for r := range do.Reps {
		rng := rand.New(rand.NewPCG(do.Seed+1000003, uint64(r)))
		draws[r] = drawSources(strata, rng)
	}
	workers := do.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	out.AgreeBoot = make([]float64, do.Reps)
	out.LogLossBoot = make([]float64, do.Reps)
	perRep := make([]map[string]float64, do.Reps)
	errs := make([]error, do.Reps)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for r := range do.Reps {
		wg.Add(1)
		sem <- struct{}{}
		go func(r int) {
			defer func() { <-sem; wg.Done() }()
			bt, bj, orig := subset(trips, js, draws[r])
			var lab []string
			if labels != nil {
				lab = make([]string, len(bt))
				for i, o := range orig {
					lab[i] = labels[o]
				}
			}
			a, l, _, s, err := one(bt, bj, lab, do.Seed+uint64(r)+1)
			if err != nil {
				errs[r] = err
				return
			}
			out.AgreeBoot[r], out.LogLossBoot[r], perRep[r] = a, l, s
		}(r)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return out, e
		}
	}
	if labels != nil {
		out.StrataBoot = map[string][]float64{}
		for l := range out.Strata {
			col := make([]float64, do.Reps)
			for r := range do.Reps {
				col[r] = perRep[r][l]
			}
			out.StrataBoot[l] = col
		}
	}
	return out, nil
}

// DropJudge removes judge j's judgments and renumbers the remaining judges (leave-one-judge-out, decisions §9).
func DropJudge(js []Judgment, j int) []Judgment {
	out := make([]Judgment, 0, len(js))
	for _, k := range js {
		if k.Judge == j {
			continue
		}
		if k.Judge > j {
			k.Judge--
		}
		out = append(out, k)
	}
	return out
}

// Quantile is the quantile of values sorted in ascending order (linear interpolation).
func Quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := min(lo+1, len(sorted)-1)
	return sorted[lo] + (pos-float64(lo))*(sorted[hi]-sorted[lo])
}

// SD is the sample standard deviation.
func SD(v []float64) float64 {
	if len(v) < 2 {
		return math.NaN()
	}
	m := Mean(v)
	s := 0.0
	for _, x := range v {
		s += (x - m) * (x - m)
	}
	return math.Sqrt(s / float64(len(v)-1))
}

// Mean is the arithmetic mean (NaN when empty).
func Mean(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// Column extracts column p of rows as a sorted copy (rows with p out of range or nil rows are skipped).
func Column(rows [][]float64, p int) []float64 {
	var col []float64
	for _, r := range rows {
		if p < len(r) {
			col = append(col, r[p])
		}
	}
	slices.Sort(col)
	return col
}
