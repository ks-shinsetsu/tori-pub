package fit

import (
	"math"
	"strings"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
)

// descentOpts are the options of TestRecovery with the descent outer loop.
func descentOpts() Options {
	opts := baseOpts(3)
	opts.Tie = TypeTie()
	opts.Outer = OuterDescent
	return opts
}

// TestDescentMonotone: under OuterDescent the true objectives of the accepted rounds are strictly decreasing, the
// returned objective is the last (smallest) of them, the loop never reports a cycle, and the result is a valid
// iterate. Both models, with a shared per-triplet offset so that the random-intercept fit has something to find.
func TestDescentMonotone(t *testing.T) {
	w := newWorld(11, altTheta(), 1.5)
	w.neg = 1.0
	trips, js := w.generate(200, true)
	for _, mixed := range []bool{false, true} {
		opts := descentOpts()
		var rounds []RoundTrace
		opts.Trace = func(tr RoundTrace) { rounds = append(rounds, tr) }
		var res Result
		var err error
		if mixed {
			res, err = FitMixed(trips, js, opts, nil)
		} else {
			res, err = Fit(trips, js, opts, nil)
		}
		if err != nil {
			t.Fatalf("mixed=%v: %v", mixed, err)
		}
		if len(rounds) == 0 {
			t.Fatalf("mixed=%v: no round was traced", mixed)
		}
		// The returned objective is not above any exploration round, the descent rounds decrease strictly from the
		// best exploration round, and the returned objective is the last accepted one.
		best := math.Inf(1)
		for _, r := range rounds {
			if !r.Descent {
				best = math.Min(best, r.True)
			}
		}
		if res.Objective > best+1e-9*math.Max(1, math.Abs(best)) {
			t.Errorf("mixed=%v: returned objective %.6f above the best exploration round %.6f", mixed, res.Objective, best)
		}
		prev, last := math.Inf(1), math.NaN()
		for _, r := range rounds {
			if !r.Descent {
				continue
			}
			if r.Step == 0 {
				last = r.True // a round without an accepted step repeats the current objective
				continue
			}
			if !(r.True < prev) {
				t.Errorf("mixed=%v round %d: true objective %.6f is not below the previous %.6f", mixed, r.Outer, r.True, prev)
			}
			if r.Step < descentMinStep || r.Step > 1 {
				t.Errorf("mixed=%v round %d: step %g outside [%g, 1]", mixed, r.Outer, r.Step, descentMinStep)
			}
			prev, last = r.True, r.True
		}
		if !math.IsNaN(last) && math.Abs(res.Objective-last) > 1e-9*math.Max(1, math.Abs(last)) {
			t.Errorf("mixed=%v: returned objective %.6f, last descent round %.6f", mixed, res.Objective, last)
		}
		if res.Explored < 1 || res.Outer < res.Explored {
			t.Errorf("mixed=%v: explored %d of %d rounds", mixed, res.Explored, res.Outer)
		}
		if res.Cycled || res.CycleSpread != 0 {
			t.Errorf("mixed=%v: descent reported a cycle (spread %g)", mixed, res.CycleSpread)
		}
		if !res.Valid() {
			t.Errorf("mixed=%v: not a valid iterate: converged %v capped %v inner %v stalled %v outer %d", mixed, res.Converged, res.Capped, res.InnerConverged, res.Stalled, res.Outer)
		}
		if res.Stalled && !res.Converged {
			t.Errorf("mixed=%v: stalled without converged", mixed)
		}
		t.Logf("mixed=%v: %d rounds, backtracked %d, stalled %v, objective %.3f", mixed, res.Outer, res.Backtracked, res.Stalled, res.Objective)
	}
}

// TestDescentReportsTrueObjective: the NegLogLik of a descent result is the likelihood at the returned parameters
// with the matching re-solved there (what NegLogLik and NegLogLikMixed compute independently of the loop).
func TestDescentReportsTrueObjective(t *testing.T) {
	w := newWorld(5, altTheta(), 1.0)
	w.neg = 1.0
	trips, js := w.generate(150, true)
	opts := descentOpts()
	ft, fj, _ := Filter(trips, js, opts)
	ind, err := Fit(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := NegLogLik(ft, fj, ind.Params); err != nil || math.Abs(v-ind.NegLogLik) > 1e-6*math.Max(1, v) {
		t.Errorf("independence: reported NLL %.6f, recomputed %.6f (err %v)", ind.NegLogLik, v, err)
	}
	mix, err := FitMixed(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The grid of the loop and that of NegLogLikMixed are both centred at the returned parameters.
	if v, err := NegLogLikMixed(ft, fj, mix.Params, 20); err != nil || math.Abs(v-mix.NegLogLik) > 1e-4*math.Max(1, v) {
		t.Errorf("random intercept: reported NLL %.6f, recomputed %.6f (err %v)", mix.NegLogLik, v, err)
	}
}

// TestDescentNotWorseThanAlternate: from the same start, the descent loop ends at a true objective no larger than
// the alternation's, and recovers the costs of the synthetic world as TestRecovery asks of the alternation.
func TestDescentNotWorseThanAlternate(t *testing.T) {
	truth := altTheta()
	w := newWorld(7, truth, 0)
	w.neg = 1.0
	trips, js := w.generate(300, true)
	alt := baseOpts(3)
	alt.Tie = TypeTie()
	for _, mixed := range []bool{false, true} {
		fitWith := func(o Options) Result {
			var r Result
			var err error
			if mixed {
				r, err = FitMixed(trips, js, o, nil)
			} else {
				r, err = Fit(trips, js, o, nil)
			}
			if err != nil {
				t.Fatalf("mixed=%v: %v", mixed, err)
			}
			return r
		}
		a, d := fitWith(alt), fitWith(descentOpts())
		t.Logf("mixed=%v: alternate %.4f (outer %d, cycled %v), descent %.4f (outer %d, backtracked %d, stalled %v)",
			mixed, a.Objective, a.Outer, a.Cycled, d.Objective, d.Outer, d.Backtracked, d.Stalled)
		if d.Objective > a.Objective+1e-6*math.Max(1, math.Abs(a.Objective)) {
			t.Errorf("mixed=%v: descent objective %.6f above the alternation's %.6f", mixed, d.Objective, a.Objective)
		}
		for _, p := range []int{ged.PNodeMissing, ged.PNodeExtra, ged.PEdgeMissing, ged.PTypeRAMA, ged.PDirection} {
			if math.Abs(d.Params.Theta[p]-truth[p]) > 0.3 {
				t.Errorf("mixed=%v: θ̂[%d] = %.3f, truth %.3f", mixed, p, d.Params.Theta[p], truth[p])
			}
		}
	}
}

// TestDescentFixedTheta: with θ fixed the matching never changes, so the two outer loops solve the same smooth
// problem and must agree.
func TestDescentFixedTheta(t *testing.T) {
	w := newWorld(3, altTheta(), 1.0)
	trips, js := w.generate(120, false)
	alt := baseOpts(3)
	alt.FixTheta = true
	des := alt
	des.Outer = OuterDescent
	for _, mixed := range []bool{false, true} {
		var a, d Result
		var err error
		if mixed {
			if a, err = FitMixed(trips, js, alt, nil); err == nil {
				d, err = FitMixed(trips, js, des, nil)
			}
		} else {
			if a, err = Fit(trips, js, alt, nil); err == nil {
				d, err = Fit(trips, js, des, nil)
			}
		}
		if err != nil {
			t.Fatalf("mixed=%v: %v", mixed, err)
		}
		if math.Abs(a.Objective-d.Objective) > 1e-5*math.Max(1, math.Abs(a.Objective)) {
			t.Errorf("mixed=%v: alternate %.6f, descent %.6f", mixed, a.Objective, d.Objective)
		}
		if d.Params.Theta != alt.Theta0 {
			t.Errorf("mixed=%v: θ moved although FixTheta is set", mixed)
		}
		if !d.Valid() {
			t.Errorf("mixed=%v: descent result is not a valid iterate", mixed)
		}
	}
}

// TestDescentIsADeviation: the descent loop is not the pre-registered procedure, so Preregistered must list it
// and the default options must not.
func TestDescentIsADeviation(t *testing.T) {
	opts := Options{Judges: 3, Theta0: ged.Current(), Tie: TypeTie()}
	co := ConfirmOptions{Theta0: ged.Current()}
	has := func(d []string) bool {
		for _, s := range d {
			if strings.Contains(s, "outer loop") {
				return true
			}
		}
		return false
	}
	if has(Preregistered(co, opts)) {
		t.Error("the default outer loop is listed as a deviation")
	}
	opts.Outer = OuterDescent
	if !has(Preregistered(co, opts)) {
		t.Error("OuterDescent is not listed as a deviation")
	}
}

// kinkCases returns small datasets with near-zero node costs, on which the alternation moves across kinks from
// random starts, so that the descent phase has to take steps (in the easy worlds above it only verifies a fixed
// point).
func kinkCases() (cases []struct {
	trips []Triplet
	js    []Judgment
	init  Params
}) {
	th := altTheta()
	th[ged.PNodeMissing], th[ged.PNodeExtra] = 0.02, 0.02
	for seed := uint64(1); seed <= 40; seed++ {
		w := newWorld(seed, th, 1.0)
		w.neg = 1.0
		trips, js := w.generate(25, true)
		for _, st := range RandomStarts(2, seed+50) {
			init := Init(descentOpts())
			init.Theta = st
			cases = append(cases, struct {
				trips []Triplet
				js    []Judgment
				init  Params
			}{trips, js, init})
		}
	}
	return cases
}

// TestDescentTakesSteps exercises the descent phase proper: on kink cases its accepted rounds decrease strictly,
// the result is not above any exploration round, the start, or the alternation's result from the same start, a stop
// after a shortened step is reported as a stall, and a restart from the result cannot end above it.
func TestDescentTakesSteps(t *testing.T) {
	stepped, stalled, cycled := 0, 0, 0
	for ci, c := range kinkCases() {
		if stepped >= 6 && stalled >= 1 && cycled >= 1 {
			break
		}
		alt := descentOpts()
		alt.Outer = OuterAlternate
		a, err := Fit(c.trips, c.js, alt, &c.init)
		if err != nil {
			continue
		}
		opts := descentOpts()
		var rounds []RoundTrace
		opts.Trace = func(tr RoundTrace) { rounds = append(rounds, tr) }
		d, err := Fit(c.trips, c.js, opts, &c.init)
		if err != nil {
			t.Fatalf("case %d: %v", ci, err)
		}
		tol := 1e-7 * math.Max(1, math.Abs(d.Objective))
		if d.Objective > a.Objective+tol {
			t.Errorf("case %d: descent %.6f above the alternation's %.6f", ci, d.Objective, a.Objective)
		}
		prev, accepted, lastStep := math.Inf(1), 0, 1.0
		for _, r := range rounds {
			if !r.Descent {
				if d.Objective > r.True+tol {
					t.Errorf("case %d: returned %.6f above exploration round %d (%.6f)", ci, d.Objective, r.Outer, r.True)
				}
				prev = math.Min(prev, r.True)
				continue
			}
			if r.Step == 0 {
				continue
			}
			accepted++
			lastStep = r.Step
			if !(r.True < prev) {
				t.Errorf("case %d round %d: true objective %.8f is not below %.8f", ci, r.Outer, r.True, prev)
			}
			prev = r.True
		}
		if accepted > 0 {
			stepped++
			if d.Backtracked == 0 && lastStep < 1 {
				t.Errorf("case %d: a shortened step was accepted but Backtracked is 0", ci)
			}
		}
		if d.Stalled {
			stalled++
			if !d.Converged || !d.Valid() {
				t.Errorf("case %d: a stall must be a converged, valid iterate (converged %v, valid %v, inner %v)", ci, d.Converged, d.Valid(), d.InnerConverged)
			}
		}
		if a.Cycled {
			cycled++
		}
		if d.Cycled || d.Capped && d.Converged {
			t.Errorf("case %d: inconsistent flags: cycled %v capped %v converged %v", ci, d.Cycled, d.Capped, d.Converged)
		}
		// The start is one of the candidates: restarting from the result cannot end above it.
		opts.Trace = nil
		r2, err := Fit(c.trips, c.js, opts, &d.Params)
		if err != nil {
			t.Fatalf("case %d restart: %v", ci, err)
		}
		if r2.Objective > d.Objective+tol {
			t.Errorf("case %d: restart ended at %.8f, above its start %.8f", ci, r2.Objective, d.Objective)
		}
		// A stall must hold up when the search starts afresh from the same point (radius reset, new exploration): a
		// restart that gains materially means the first run stopped on the state of its search, not on the point.
		if gain := d.Objective - r2.Objective; gain > 0.01 {
			t.Errorf("case %d: a restart from the result gains %.4f nats (%.6f → %.6f)", ci, gain, d.Objective, r2.Objective)
		}
	}
	t.Logf("cases with accepted descent steps %d, stalls %d, cycling alternations %d", stepped, stalled, cycled)
	if stepped == 0 {
		t.Error("no case made the descent phase take a step: the test does not exercise it")
	}
	if cycled == 0 {
		t.Error("no case made the alternation cycle: the kink cases are too easy")
	}
}

// TestDescentAtBounds: a judge that never answers "same" drives log τ to its lower bound, below the floor that
// pack applies. The returned parameters and the reported likelihood must still be the same point, and the result
// must not end above the alternation's.
func TestDescentAtBounds(t *testing.T) {
	w := newWorld(11, altTheta(), 1.5)
	w.neg = 1.0
	trips, js := w.generate(200, true)
	for i := range js {
		if js[i].Judge == 0 && js[i].Answer == AnswerTie {
			js[i].Answer = Answer(i % 2) // A or B
		}
	}
	alt := baseOpts(3)
	alt.Tie = TypeTie()
	ft, fj, _ := Filter(trips, js, alt)
	for _, mixed := range []bool{false, true} {
		var a, d Result
		var err error
		var v float64
		if mixed {
			if a, err = FitMixed(trips, js, alt, nil); err == nil {
				if d, err = FitMixed(trips, js, descentOpts(), nil); err == nil {
					v, err = NegLogLikMixed(ft, fj, d.Params, 20)
				}
			}
		} else {
			if a, err = Fit(trips, js, alt, nil); err == nil {
				if d, err = Fit(trips, js, descentOpts(), nil); err == nil {
					v, err = NegLogLik(ft, fj, d.Params)
				}
			}
		}
		if err != nil {
			t.Fatalf("mixed=%v: %v", mixed, err)
		}
		if math.Log(d.Params.Tau[0]) > math.Log(1e-3)-1e-9 {
			t.Logf("mixed=%v: log τ₀ = %.3f did not go below the pack floor; the case is not at the bound", mixed, math.Log(d.Params.Tau[0]))
		}
		tolNLL := 1e-6
		if mixed {
			tolNLL = 1e-4 // the grid of NegLogLikMixed is re-centred
		}
		if math.Abs(v-d.NegLogLik) > tolNLL*math.Max(1, v) {
			t.Errorf("mixed=%v: reported NLL %.6f, recomputed at the returned parameters %.6f", mixed, d.NegLogLik, v)
		}
		if d.Objective > a.Objective+1e-6*math.Max(1, math.Abs(a.Objective)) {
			t.Errorf("mixed=%v: descent %.6f above the alternation's %.6f", mixed, d.Objective, a.Objective)
		}
		if !d.Valid() {
			t.Errorf("mixed=%v: not a valid iterate (converged %v stalled %v capped %v inner %v)", mixed, d.Converged, d.Stalled, d.Capped, d.InnerConverged)
		}
		t.Logf("mixed=%v: alternate %.6f, descent %.6f (explored %d, outer %d, stalled %v), log τ₀ %.3f", mixed, a.Objective, d.Objective, d.Explored, d.Outer, d.Stalled, math.Log(d.Params.Tau[0]))
	}
}

// TestDescentOptions: the descent loop respects a fixed cost, the no-lapse model and the multi-start, and is not
// above the alternation under the same options.
func TestDescentOptions(t *testing.T) {
	w := newWorld(9, altTheta(), 1.0)
	w.neg = 1.0
	trips, js := w.generate(150, true)
	base := baseOpts(3)
	base.Tie = TypeTie()
	base.NoLapse = true
	base.Fixed = map[int]float64{ged.PDirection: 1.7}
	base.Starts, base.StartNames = DefaultStarts(base.Theta0)
	des := base
	des.Outer = OuterDescent
	for _, mixed := range []bool{false, true} {
		var a, d Result
		var err error
		if mixed {
			if a, err = FitMixed(trips, js, base, nil); err == nil {
				d, err = FitMixed(trips, js, des, nil)
			}
		} else {
			if a, err = Fit(trips, js, base, nil); err == nil {
				d, err = Fit(trips, js, des, nil)
			}
		}
		if err != nil {
			t.Fatalf("mixed=%v: %v", mixed, err)
		}
		if d.Params.Theta[ged.PDirection] != 1.7 {
			t.Errorf("mixed=%v: the fixed cost moved to %g", mixed, d.Params.Theta[ged.PDirection])
		}
		for i, m := range base.Tie {
			if d.Params.Theta[i] != d.Params.Theta[m] {
				t.Errorf("mixed=%v: tied cost %d = %g, master %d = %g", mixed, i, d.Params.Theta[i], m, d.Params.Theta[m])
			}
		}
		for j, l := range d.Params.Lapse {
			if l != 0 {
				t.Errorf("mixed=%v: lapse of judge %d is %g under NoLapse", mixed, j, l)
			}
		}
		if len(d.Starts) != len(base.Starts) {
			t.Errorf("mixed=%v: %d starts recorded, want %d", mixed, len(d.Starts), len(base.Starts))
		}
		if d.Objective > a.Objective+1e-6*math.Max(1, math.Abs(a.Objective)) {
			t.Errorf("mixed=%v: descent %.6f above the alternation's %.6f", mixed, d.Objective, a.Objective)
		}
		if !d.Valid() {
			t.Errorf("mixed=%v: not a valid iterate", mixed)
		}
	}
}

// TestDescentAgainstTighterRun measures what the stopping rule of the descent phase leaves on the table: on the kink
// cases the default run is compared with a run of the same loop whose tolerance is five orders tighter and whose
// round budget is larger. A stall is a statement about the rate of progress, so the tighter run may end lower; the
// test bounds the gap. The tighter run shares the search of the default one, so this checks the tolerance only;
// TestDescentTakesSteps checks the search itself by restarting it from the result.
func TestDescentAgainstTighterRun(t *testing.T) {
	defer func(s float64, p, f int) { descentStop, descentPatience, descentRoundFactor = s, p, f }(descentStop, descentPatience, descentRoundFactor)
	checked, worst := 0, 0.0
	for ci, c := range kinkCases() {
		if checked >= 8 {
			break
		}
		opts := descentOpts()
		var rounds []RoundTrace
		opts.Trace = func(tr RoundTrace) { rounds = append(rounds, tr) }
		d, err := Fit(c.trips, c.js, opts, &c.init)
		if err != nil {
			continue
		}
		stepped := false
		for _, r := range rounds {
			stepped = stepped || (r.Descent && r.Step > 0)
		}
		if !stepped {
			continue
		}
		checked++
		stop, patience, factor := descentStop, descentPatience, descentRoundFactor
		descentStop, descentPatience, descentRoundFactor = 1e-11, 10, 50
		opts.Trace = nil
		tight, err := Fit(c.trips, c.js, opts, &c.init)
		descentStop, descentPatience, descentRoundFactor = stop, patience, factor
		if err != nil {
			t.Fatalf("case %d (tight): %v", ci, err)
		}
		gap := d.Objective - tight.Objective
		worst = math.Max(worst, gap)
		t.Logf("case %d: default %.4f (outer %d, stalled %v, capped %v), tight %.4f (outer %d, stalled %v, capped %v), gap %.4f",
			ci, d.Objective, d.Outer, d.Stalled, d.Capped, tight.Objective, tight.Outer, tight.Stalled, tight.Capped, gap)
		if d.Capped {
			t.Errorf("case %d: the default run used all its descent rounds", ci)
		}
		// The default stop must not leave more than a rounding of the likelihood behind (0.01 nats; the first version of
		// the phase, a line search along the proposal, left up to 12.8 nats on these cases).
		if gap > 0.01 {
			t.Errorf("case %d: the default run stops %.4f nats above the tighter run", ci, gap)
		}
	}
	t.Logf("%d cases, largest gap to the tighter run %.4f nats", checked, worst)
	if checked == 0 {
		t.Fatal("no kink case made the descent phase take a step")
	}
}

// TestDescentRestart: cases found in review in which earlier versions of the loop returned a point that a restart of
// the procedure improved. A restart from the result (a new exploration, then a descent with a fresh radius) must not
// gain more than a rounding of the likelihood.
//   - mixed fit, 25 sources: a round without progress accepted a tiny box step before the gradient and the line
//     towards the proposal were tried, and the phase stalled 0.05 nats early;
//   - independence fit, 12 sources: the exploration of a restart reached a point 3.1 nats lower through a
//     non-monotone round that the descent cannot make (now the next pass of the same fit);
//   - independence fit, 25 sources, node costs 0.1: the gradient step was scaled to the length of a proposal that
//     promised almost nothing, and a basin 24 nats lower one unit away was missed.
func TestDescentRestart(t *testing.T) {
	for _, c := range []struct {
		name     string
		sources  int
		nodeCost float64
		seed     uint64
		start    int
		mixed    bool
	}{
		{"mixed, 25 sources", 25, 0.02, 24, 1, true},
		{"independence, 12 sources", 12, 0.02, 26, 1, false},
		{"independence, node costs 0.1", 25, 0.1, 44, 0, false},
	} {
		th := altTheta()
		th[ged.PNodeMissing], th[ged.PNodeExtra] = c.nodeCost, c.nodeCost
		w := newWorld(c.seed, th, 1.0)
		w.neg = 1.0
		trips, js := w.generate(c.sources, true)
		opts := descentOpts()
		init := Init(opts)
		init.Theta = RandomStarts(2, c.seed+50)[c.start]
		fitWith := func(p *Params) Result {
			var r Result
			var err error
			if c.mixed {
				r, err = FitMixed(trips, js, opts, p)
			} else {
				r, err = Fit(trips, js, opts, p)
			}
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			return r
		}
		d := fitWith(&init)
		r := fitWith(&d.Params)
		gain := d.Objective - r.Objective
		t.Logf("%s: %.6f (outer %d, explored %d, passes %d, stalled %v, capped %v), restart %.6f, gain %.6f",
			c.name, d.Objective, d.Outer, d.Explored, d.Passes, d.Stalled, d.Capped, r.Objective, gain)
		if gain > 0.01 {
			t.Errorf("%s: a restart from the result gains %.4f nats", c.name, gain)
		}
		if !d.Valid() {
			t.Errorf("%s: not a valid iterate (converged %v, capped %v, inner %v)", c.name, d.Converged, d.Capped, d.InnerConverged)
		}
	}
}
