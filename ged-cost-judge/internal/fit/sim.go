package fit

import (
	"errors"
	"math"
	"math/rand/v2"
)

// SimulateJudgments draws answers for the design (triplet, judge, order of every judgment in design) from the
// model at p, with Δ solved at p.Theta and one shared offset u_t ~ N(0, p.Sigma²) per triplet (Appendix A.6;
// p.Sigma = 0 gives the working-independence model). It is the simulator for parametric-bootstrap LR tests.
func SimulateJudgments(trips []Triplet, design []Judgment, p Params, rng *rand.Rand) ([]Judgment, error) {
	if len(p.Beta) == 0 {
		return nil, errors.New("simulate: params have no judges")
	}
	d, err := Delta(trips, p.Theta)
	if err != nil {
		return nil, err
	}
	u := make([]float64, len(trips))
	if p.Sigma > 0 {
		for t := range u {
			u[t] = p.Sigma * rng.NormFloat64()
		}
	}
	out := make([]Judgment, len(design))
	for i, k := range design {
		if k.Judge >= len(p.Beta) {
			return nil, errors.New("simulate: judge index out of range")
		}
		t := k.Triplet
		D := d[t] + p.ThetaNeg*trips[t].NegDiff
		z := p.Beta[k.Judge]*D + p.Pi[k.Judge]*k.Order + p.Gamma*trips[t].DeltaCoh() + u[t]
		pa, pb, _ := Probs(z, p.Tau[k.Judge])
		r := rng.Float64()
		k.Answer = AnswerTie
		switch {
		case r < pa:
			k.Answer = AnswerA
		case r < pa+pb:
			k.Answer = AnswerB
		}
		out[i] = k
	}
	return out, nil
}

// LRResult is the outcome of a parametric-bootstrap likelihood-ratio test.
type LRResult struct {
	// Observed is 2·(NLL_null − NLL_alt) on the data.
	Observed float64
	// Sim holds the statistic on each simulated dataset.
	Sim []float64
	// P is (1 + #{Sim ≥ Observed}) / (reps + 1).
	P float64
}

// ParametricBootstrapLR runs an LR test whose reference distribution is simulated from the fitted null model
// (Self & Liang 1987; decisions §12 H5 and §9 secondary judge test). fitNull and fitAlt return the negative
// log-likelihood of each model on the given judgments; null is the fitted null model used to simulate.
func ParametricBootstrapLR(trips []Triplet, js []Judgment, null Params, fitNull, fitAlt func(trips []Triplet, js []Judgment) (float64, error), reps int, seed uint64) (LRResult, error) {
	n0, err := fitNull(trips, js)
	if err != nil {
		return LRResult{}, err
	}
	n1, err := fitAlt(trips, js)
	if err != nil {
		return LRResult{}, err
	}
	res := LRResult{Observed: 2 * (n0 - n1), Sim: make([]float64, reps)}
	ge := 0
	for r := range reps {
		rng := rand.New(rand.NewPCG(seed, uint64(r)))
		sim, err := SimulateJudgments(trips, js, null, rng)
		if err != nil {
			return res, err
		}
		s0, err := fitNull(trips, sim)
		if err != nil {
			return res, err
		}
		s1, err := fitAlt(trips, sim)
		if err != nil {
			return res, err
		}
		res.Sim[r] = 2 * (s0 - s1)
		if res.Sim[r] >= res.Observed-1e-12 {
			ge++
		}
	}
	res.P = float64(1+ge) / float64(reps+1)
	if reps == 0 {
		res.P = math.NaN()
	}
	return res, nil
}
