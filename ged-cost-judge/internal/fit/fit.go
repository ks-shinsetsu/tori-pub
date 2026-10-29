// Package fit estimates GED costs from 3-way judgments on triplets (plan §6, decisions-stats §2, §11, §12b–c).
//
// Model (Rao–Kupper-style paired comparison with ties; the same form as an ordered logit):
//
//	Δ_t(θ) = GED_θ(S, B) − GED_θ(S, A)                     (positive means A is closer)
//	z_k    = β_j·(Δ_t(θ) + θ_neg·Δneg_t) + π_j·o_k + γ·Δcoh_t
//	P(A) = σ(z_k − τ_j),  P(B) = σ(−z_k − τ_j),  P(same) = the rest
//
// Two likelihoods are implemented on the same code path (decisions §2 #12, Appendix A.6):
//
//   - working independence (Fit): the judgments are treated as independent;
//   - random intercept (FitMixed): the judgments of one triplet share u_t ~ N(0, σ²) added to every z_k,
//     integrated out by Gauss–Hermite quadrature.
//
// GED is a minimum over proposition matchings, so it has points where it is not differentiable in θ.
// With the matching fixed it is linear in θ, so "solve the matching → fix it and fit" is repeated until the
// matching stops changing or the true objective stops moving (decisions §11.2).
//
// θ ≥ 0 is imposed by a bound-constrained quasi-Newton method on θ directly (decisions §11.1, review B1);
// β and τ keep the log map. Optional pieces: a column-sum tie for the type costs (§12b), triplet/judgment
// filters (§12c, #15), per-triplet weights (§12c), the coherence covariate γ (§10), and multi-start (§11.3).
package fit

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"gonum.org/v1/gonum/integrate/quad"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
)

// Answer is a 3-way answer.
type Answer int

const (
	AnswerA Answer = iota
	AnswerB
	AnswerTie
)

func (a Answer) String() string {
	return [...]string{"A", "B", "同程度"}[a]
}

// Triplet is a triplet (S, A, B).
//
// The descriptive fields (Type, Flags, Generator, …) are optional. Empty values behave as before:
// no stratification, no filtering, weight 1.
type Triplet struct {
	// Source is the source argument index. The unit for cluster bootstrap and cross-validation.
	Source int
	Ref    ged.Graph
	A, B   ged.Graph
	SimA   ged.Sim
	SimB   ged.Sim
	// NegDiff is the difference in the number of polarity flips (B − A). GED cannot observe it.
	NegDiff float64

	// Type is the triplet type ("T1".."T4").
	Type string
	// Flags are dataset flags; values among "x_spk", "diag_ca_type", "qt30", "del_add_cooccur", "t3_ident", "t1_gate".
	Flags []string
	// Generator is the model that wrote the source and variants (bootstrap / CV stratum).
	Generator string
	// VerifierFamily is the model family that verified the variants (family-coupling filter, §12c).
	VerifierFamily string
	// ExemplarPair and Domain are recorded for the ICC checks of §6; not used by the fit.
	ExemplarPair string
	Domain       string
	// Weight is the importance weight for the reweighted fit of §12c (0 means 1); used only with Options.Weighted.
	Weight float64
	// CohA, CohB are the verifier's coherence ratings of A and B; Δcoh = CohA − CohB (§10).
	CohA, CohB float64
	// T1Delta0 is Δ(θ₀) of a T1 triplet (the judge QC gate stratum of §9.1).
	T1Delta0 float64

	// orig is the original Source of a bootstrap copy (set by subset); see foldKey.
	orig    int
	hasOrig bool
}

// HasFlag reports whether the triplet carries flag f.
func (t Triplet) HasFlag(f string) bool { return slices.Contains(t.Flags, f) }

// weight returns the effective importance weight.
func (t Triplet) weight() float64 {
	if t.Weight == 0 || math.IsNaN(t.Weight) {
		return 1
	}
	return t.Weight
}

// checkWeights rejects negative weights (a zero weight means 1 by the field contract; exclude via Flags instead).
func checkWeights(trips []Triplet, opts Options) error {
	if !opts.Weighted {
		return nil
	}
	for i, t := range trips {
		w := t.Weight
		if opts.Weights != nil && i < len(opts.Weights) {
			w = opts.Weights[i]
		}
		if w < 0 || math.IsInf(w, 0) {
			return fmt.Errorf("fit: triplet %d has weight %g (must be ≥ 0; 0 or NaN means 1)", i, w)
		}
	}
	return nil
}

// foldKey is the cluster identity used to group folds: the original source of a bootstrap copy, so that copies of
// one source never straddle a CV fold inside a nested bootstrap (review-fit-impl M2).
func (t Triplet) foldKey() int {
	if t.hasOrig {
		return t.orig
	}
	return t.Source
}

// DeltaCoh is coh(A) − coh(B).
func (t Triplet) DeltaCoh() float64 { return t.CohA - t.CohB }

// Judgment is a single judgment.
type Judgment struct {
	Triplet int
	Judge   int
	// Order is +1 if A was shown first, −1 if second.
	Order  float64
	Answer Answer
	// Family is the judge's model family (family-coupling filter, §12c). Empty disables the filter for this judgment.
	Family string
}

// Params are the parameters to estimate.
type Params struct {
	Theta    ged.Theta
	ThetaNeg float64
	Beta     []float64
	Tau      []float64
	Pi       []float64
	// Lapse is the per-judge random-answer rate ℓ_j of the lapse mixture P'(y) = (1 − ℓ_j)·P(y) + ℓ_j/3
	// (decisions §2 as revised 2026-10-29; nil or 0 means no lapses).
	Lapse []float64
	// Gamma is the coefficient of Δcoh (0 unless Options.Gamma is on).
	Gamma float64
	// Sigma is the random-intercept SD (0 for the working-independence fit).
	Sigma float64
}

// GammaMode selects how the coherence covariate enters (§10).
type GammaMode int

const (
	// GammaOff leaves Δcoh out of the model (primary).
	GammaOff GammaMode = iota
	// GammaFree estimates γ (sensitivity a′).
	GammaFree
	// GammaFixed enters Options.GammaValue·Δcoh as a fixed offset (sensitivity a; γ from the negative control).
	GammaFixed
)

// DefaultExclude is the default triplet-flag exclusion list: X-spk (RQ4 only, #15), the diagnostic CA cell (§12b),
// QT30 transfer triplets (transfer-only, #15), P-del/P-add co-occurrence (#15, §15.5) and pilot/P2 triplets (§13, §15.9).
var DefaultExclude = []string{"x_spk", "diag_ca_type", "qt30", "del_add_cooccur", "pilot"}

// ThetaMax is the upper bound of every cost (decisions §6: θ_p > 10 counts as divergent).
const ThetaMax = 10.0

// BoundaryEps is the threshold below which a cost counts as "at the boundary" (decisions §6).
const BoundaryEps = 1e-3

// Options configures estimation.
type Options struct {
	Judges int
	// Theta0 is the center of L2 regularization and the default start.
	Theta0 ged.Theta
	// Lambda is the coefficient of λ‖θ − θ₀‖² (0 for every inferential fit, decisions §5).
	Lambda float64
	// If FitNeg is false, θ_neg is fixed at 0.
	FitNeg bool
	// If FixTheta is true, θ is fixed at Theta0 and only the auxiliary parameters are fitted
	// (to give fixed rules a chance at calibration too and compare log loss; plan §7.3).
	FixTheta bool
	MaxOuter int
	// AuxRidge is the L2 coefficient on log β, log τ and π (0 means 1e-4).
	// With a single judge and few judgments, perfect separation makes β diverge, so at small scale it acts as a weak prior.
	AuxRidge float64
	// Fixed holds θ indices and values to fix (for drawing profile likelihoods).
	Fixed map[int]float64

	// Tie maps a cost index to the index whose value it shares; the tied column of Δφ is added to the
	// master column (decisions §12b). TypeTie() gives the pre-registered type tie (9 free costs).
	Tie map[int]int

	// Exclude lists triplet flags that remove the triplet from the fit. nil means DefaultExclude;
	// an empty non-nil slice (or IncludeAll) excludes nothing.
	Exclude    []string
	IncludeAll bool
	// ExcludeSameFamily drops judgments whose judge family equals the triplet's verifier family (§12c).
	ExcludeSameFamily bool
	// CohZeroOnly restricts the fit to triplets with Δcoh = 0 (§10 sensitivity b).
	CohZeroOnly bool

	// Weighted uses Triplet.Weight (and Weights, if set) as a multiplier of each triplet's log-likelihood (§12c).
	Weighted bool
	// Weights, if non-nil, overrides Triplet.Weight by position (len(Weights) must equal len(trips)).
	Weights []float64

	// Gamma selects the coherence covariate mode; GammaValue is the fixed offset coefficient for GammaFixed.
	Gamma      GammaMode
	GammaValue float64

	// Starts, if non-empty, makes Fit run from each listed θ (plus the explicit init, if any) and keep the best
	// true objective (decisions §11.3). DefaultStarts gives θ₀, R1 and all-0.5.
	Starts []ged.Theta
	// StartNames labels Starts in Result.Starts (optional).
	StartNames []string

	// Quadrature is the number of Gauss–Hermite nodes for FitMixed (0 → 20).
	Quadrature int
	// Sigma0 is the initial random-intercept SD for FitMixed (0 → 1).
	Sigma0 float64
	// NoLapse fixes every ℓ_j at 0 (the pre-2026-10-29 model; a recorded deviation in Confirmatory).
	NoLapse bool
}

// TypeTie is the pre-registered tie of the three type-mismatch costs to the RA↔MA cost (decisions §12b).
func TypeTie() map[int]int {
	return map[int]int{ged.PTypeRACA: ged.PTypeRAMA, ged.PTypeCAMA: ged.PTypeRAMA}
}

// R1 is the naive uniform table (plan §7.2): substitution 1 − cos, type and direction mismatch = delete + insert = 2.
func R1() ged.Theta {
	t := ged.Current()
	t[ged.PTypeRACA], t[ged.PTypeRAMA], t[ged.PTypeCAMA], t[ged.PDirection] = 2, 2, 2, 2
	return t
}

// DefaultStarts are the three pre-registered starts of the full-data fit: θ₀, R1, all-0.5 (decisions §11.3).
func DefaultStarts(theta0 ged.Theta) ([]ged.Theta, []string) {
	var half ged.Theta
	for i := range half {
		half[i] = 0.5
	}
	return []ged.Theta{theta0, R1(), half}, []string{"θ₀", "R1", "all-0.5"}
}

// FreeTheta reports which cost coordinates are free under opts (not fixed, not tied).
func FreeTheta(opts Options) [ged.NumParams]bool {
	var free [ged.NumParams]bool
	for p := range ged.NumParams {
		_, fixed := opts.Fixed[p]
		_, tied := opts.Tie[p]
		free[p] = !opts.FixTheta && !fixed && !tied
	}
	return free
}

// NumFree is the number of free cost parameters.
func NumFree(opts Options) int {
	n := 0
	for _, f := range FreeTheta(opts) {
		if f {
			n++
		}
	}
	return n
}

// StartResult records one start of a multi-start fit.
type StartResult struct {
	Name      string
	Theta     ged.Theta
	Objective float64
	NegLogLik float64
	Converged bool
	Cycled    bool
}

// Result is the estimation result.
type Result struct {
	Params Params
	// NegLogLik is the (weighted) negative log-likelihood excluding penalties, at the final matching.
	NegLogLik float64
	// Objective is NegLogLik plus the penalties (the quantity compared across starts and rounds).
	Objective float64
	Outer     int
	Converged bool
	// Cycled indicates the matching returned to an earlier state (the alternating optimization cycled).
	// In that case, the round with the smallest true objective, re-solving the matching at each round's θ, is returned.
	Cycled bool
	// Capped indicates the outer loop hit MaxOuter without converging or cycling.
	Capped bool
	// InnerConverged is false if the bound-constrained inner solver of the returned round stopped on a line-search
	// failure or an iteration/evaluation cap rather than on its tolerances.
	InnerConverged bool
	// Boundary[p] is true if the free cost p ended below BoundaryEps; Divergent[p] if it ended at ThetaMax (clipped).
	Boundary  [ged.NumParams]bool
	Divergent [ged.NumParams]bool
	// Free marks the cost coordinates that were estimated.
	Free [ged.NumParams]bool
	// Mixed is true for a random-intercept fit.
	Mixed bool
	// Starts lists the multi-start runs (empty for a single start); StartSpread is the largest |Δθ_p| between
	// the best start and any other start (decisions §11.3 asks to report disagreement > 0.05).
	Starts      []StartResult
	StartSpread float64
	// Triplets and Judgments are the counts after filtering.
	Triplets, Judgments int
}

// AnyBoundary reports whether any free cost is at the lower boundary.
func (r Result) AnyBoundary() bool {
	for p, b := range r.Boundary {
		if b && r.Free[p] {
			return true
		}
	}
	return false
}

// diff is Δ_t = dphi·θ + c with the matching fixed (dphi is the unfolded 11-column difference).
type diff struct {
	dphi [ged.NumParams]float64
	c    float64
	sig  string
}

// canon returns a copy of g with propositions sorted by ID and edges sorted by (From, To, Kind), so the
// assignment solver sees the same input order regardless of how the graph was built (decisions §11.5:
// deterministic tie-breaking, reproducible signatures).
func canon(g ged.Graph) ged.Graph {
	c := g.Clone()
	sort.SliceStable(c.Props, func(i, j int) bool { return c.Props[i].ID < c.Props[j].ID })
	sort.SliceStable(c.Edges, func(i, j int) bool {
		a, b := c.Edges[i], c.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Kind < b.Kind
	})
	return c
}

// features re-solves the matching of every triplet at θ and returns the linearization of Δ.
func features(trips []Triplet, th ged.Theta) ([]diff, error) {
	out := make([]diff, len(trips))
	errs := make([]error, len(trips))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i := range trips {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			t := trips[i]
			ref := canon(t.Ref)
			fa, err := ged.Compute(ref, canon(t.A), t.SimA, th)
			if err != nil {
				errs[i] = err
				return
			}
			fb, err := ged.Compute(ref, canon(t.B), t.SimB, th)
			if err != nil {
				errs[i] = err
				return
			}
			var d diff
			for p := range ged.NumParams {
				d.dphi[p] = fb.Phi[p] - fa.Phi[p]
			}
			d.c = (fb.EdgeExtra - fa.EdgeExtra) * ged.EdgeExtraCost
			d.sig = fa.Signature() + "|" + fb.Signature()
			out[i] = d
		}(i)
	}
	wg.Wait()
	return out, errors.Join(errs...)
}

// Signatures identifies the proposition matching (A and B) per triplet at θ.
func Signatures(trips []Triplet, th ged.Theta) ([]string, error) {
	ds, err := features(trips, th)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.sig
	}
	return out, nil
}

// Delta returns Δ_t at θ (the difference in real GED).
func Delta(trips []Triplet, th ged.Theta) ([]float64, error) {
	ds, err := features(trips, th)
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(ds))
	for i, d := range ds {
		out[i] = d.value(th)
	}
	return out, nil
}

// DeltaPhi returns, per triplet, the 12-vector (Δφ_0..Δφ_10, Δ edge-extra) at θ, i.e. the columns whose
// non-zero entries make the triplet informative for a cost (decisions §15.3).
func DeltaPhi(trips []Triplet, th ged.Theta) ([][ged.NumParams + 1]float64, error) {
	ds, err := features(trips, th)
	if err != nil {
		return nil, err
	}
	out := make([][ged.NumParams + 1]float64, len(ds))
	for i, d := range ds {
		copy(out[i][:ged.NumParams], d.dphi[:])
		out[i][ged.NumParams] = d.c / ged.EdgeExtraCost
	}
	return out, nil
}

// Informative counts, per cost (index NumParams = edge-extra), the triplets with a non-zero Δφ column at θ.
// With tie non-nil the tied columns are folded into their master before counting.
func Informative(trips []Triplet, th ged.Theta, tie map[int]int) ([ged.NumParams + 1]int, error) {
	var n [ged.NumParams + 1]int
	dp, err := DeltaPhi(trips, th)
	if err != nil {
		return n, err
	}
	for _, d := range dp {
		var f [ged.NumParams + 1]float64
		for p := range ged.NumParams {
			q := p
			if m, ok := tie[p]; ok {
				q = m
			}
			f[q] += d[p]
		}
		f[ged.NumParams] = d[ged.NumParams]
		for p, v := range f {
			if math.Abs(v) > 1e-12 {
				n[p]++
			}
		}
	}
	return n, nil
}

func (d diff) value(th ged.Theta) float64 {
	v := d.c
	for p := range ged.NumParams {
		v += d.dphi[p] * th[p]
	}
	return v
}

// Variable vector layout: θ[11] (direct, bounded), neg, log β[J], log τ[J], π[J], γ, log σ.
type layout struct{ j int }

func (l layout) size() int       { return ged.NumParams + 1 + 4*l.j + 2 }
func (l layout) neg() int        { return ged.NumParams }
func (l layout) beta(j int) int  { return ged.NumParams + 1 + j }
func (l layout) tau(j int) int   { return ged.NumParams + 1 + l.j + j }
func (l layout) pi(j int) int    { return ged.NumParams + 1 + 2*l.j + j }
func (l layout) lapse(j int) int { return ged.NumParams + 1 + 3*l.j + j }
func (l layout) gamma() int      { return ged.NumParams + 1 + 4*l.j }
func (l layout) logSigma() int   { return ged.NumParams + 1 + 4*l.j + 1 }

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

// logSigmoid computes log σ(x) stably.
func logSigmoid(x float64) float64 {
	if x < 0 {
		return x - math.Log1p(math.Exp(x))
	}
	return -math.Log1p(math.Exp(-x))
}

// broadStep is the largest trapezoid spacing of the adaptive quadrature at nq = 20 (logit units); minStep its floor.
const (
	broadStep   = 0.6
	minStep     = 0.1
	pruneMargin = 40.0
)

// LapseMax / LapseMin bound a judge's random-answer rate (ℓ is optimised on the logit scale, so the lower bound is
// a floor rather than an active constraint); LapseInit is the starting value.
const (
	LapseMax  = 0.5
	LapseMin  = 1e-4
	LapseInit = 0.05
)

func logit(p float64) float64 { return math.Log(p / (1 - p)) }

const (
	logBetaMin, logBetaMax   = -7.0, 7.0
	logTauMin, logTauMax     = -7.0, 5.0
	piBound                  = 20.0
	negBound                 = 20.0
	gammaBound               = 20.0
	logSigmaMin, logSigmaMax = -6.0, 3.0
)

func (l layout) pack(p Params, mixed bool) []float64 {
	x := make([]float64, l.size())
	for i := range ged.NumParams {
		x[i] = clampTo(p.Theta[i], 0, ThetaMax)
	}
	x[l.neg()] = p.ThetaNeg
	for j := range l.j {
		x[l.beta(j)] = math.Log(math.Max(p.Beta[j], 1e-3))
		x[l.tau(j)] = math.Log(math.Max(p.Tau[j], 1e-3))
		x[l.pi(j)] = p.Pi[j]
		if j < len(p.Lapse) {
			x[l.lapse(j)] = logit(clampTo(p.Lapse[j], LapseMin, LapseMax))
		} else {
			x[l.lapse(j)] = logit(LapseMin)
		}
	}
	x[l.gamma()] = p.Gamma
	if mixed {
		x[l.logSigma()] = math.Log(math.Max(p.Sigma, 1e-3))
	}
	return x
}

func (l layout) unpack(x []float64, tie map[int]int, mixed bool) Params {
	p := Params{Beta: make([]float64, l.j), Tau: make([]float64, l.j), Pi: make([]float64, l.j), Lapse: make([]float64, l.j)}
	for i := range ged.NumParams {
		p.Theta[i] = x[i]
	}
	for i, m := range tie {
		p.Theta[i] = x[m]
	}
	p.ThetaNeg = x[l.neg()]
	for j := range l.j {
		p.Beta[j] = math.Exp(x[l.beta(j)])
		p.Tau[j] = math.Exp(x[l.tau(j)])
		p.Pi[j] = x[l.pi(j)]
		p.Lapse[j] = sigmoid(x[l.lapse(j)])
	}
	p.Gamma = x[l.gamma()]
	if mixed {
		p.Sigma = math.Exp(x[l.logSigma()])
	}
	return p
}

// bounds builds the box for the variable vector under opts; a fixed coordinate has lo = hi.
func (l layout) bounds(opts Options, p Params, mixed bool) (lo, hi []float64) {
	n := l.size()
	lo, hi = make([]float64, n), make([]float64, n)
	free := FreeTheta(opts)
	for i := range ged.NumParams {
		if free[i] {
			lo[i], hi[i] = 0, ThetaMax
		} else {
			v := clampTo(p.Theta[i], 0, ThetaMax)
			if _, tied := opts.Tie[i]; tied {
				v = 0 // unused coordinate; the value comes from the master
			}
			lo[i], hi[i] = v, v
		}
	}
	if opts.FitNeg {
		lo[l.neg()], hi[l.neg()] = -negBound, negBound
	} else {
		lo[l.neg()], hi[l.neg()] = 0, 0
	}
	for j := range l.j {
		lo[l.beta(j)], hi[l.beta(j)] = logBetaMin, logBetaMax
		lo[l.tau(j)], hi[l.tau(j)] = logTauMin, logTauMax
		lo[l.pi(j)], hi[l.pi(j)] = -piBound, piBound
		if opts.NoLapse {
			lo[l.lapse(j)], hi[l.lapse(j)] = logit(LapseMin), logit(LapseMin)
		} else {
			lo[l.lapse(j)], hi[l.lapse(j)] = logit(LapseMin), logit(LapseMax)
		}
	}
	switch opts.Gamma {
	case GammaFree:
		lo[l.gamma()], hi[l.gamma()] = -gammaBound, gammaBound
	case GammaFixed:
		lo[l.gamma()], hi[l.gamma()] = opts.GammaValue, opts.GammaValue
	default:
		lo[l.gamma()], hi[l.gamma()] = 0, 0
	}
	if mixed {
		lo[l.logSigma()], hi[l.logSigma()] = logSigmaMin, logSigmaMax
	} else {
		lo[l.logSigma()], hi[l.logSigma()] = 0, 0
	}
	return lo, hi
}

// answerLogLik returns the log-likelihood of one judgment and its derivatives with respect to z and τ.
func answerLogLik(y Answer, z, tau float64) (ll, dz, dtau float64) {
	const floor = 1e-12
	switch y {
	case AnswerA:
		a := sigmoid(z - tau)
		return logSigmoid(z - tau), 1 - a, -(1 - a)
	case AnswerB:
		b := sigmoid(-z - tau)
		return logSigmoid(-z - tau), -(1 - b), -(1 - b)
	default:
		a, b := sigmoid(z-tau), sigmoid(-z-tau)
		pt := 1 - a - b
		if pt < floor {
			// Floored: the value is constant, so its derivatives are 0 (review-fit-impl m18).
			return math.Log(floor), 0, 0
		}
		return math.Log(pt), (-a*(1-a) + b*(1-b)) / pt, (a*(1-a) + b*(1-b)) / pt
	}
}

// mixLogLik is the log-likelihood of one judgment under the lapse mixture P' = (1 − ℓ)·P(y) + ℓ/3 and its
// derivatives with respect to z, τ and ℓ.
func mixLogLik(y Answer, z, tau, lapse float64) (ll, dz, dtau, dl float64) {
	ll0, dz0, dtau0 := answerLogLik(y, z, tau)
	if lapse <= 0 {
		return ll0, dz0, dtau0, 1/3.0/math.Exp(ll0) - 1
	}
	p := math.Exp(ll0)
	pm := (1-lapse)*p + lapse/3
	a := (1 - lapse) * p / pm
	return math.Log(pm), a * dz0, a * dtau0, (1/3.0 - p) / pm
}

// mixCurvature is d²/dz² of the mixture log-likelihood.
func mixCurvature(y Answer, z, tau, lapse float64) float64 {
	ll0, dz0, _ := answerLogLik(y, z, tau)
	d20 := answerCurvature(y, z, tau)
	if lapse <= 0 {
		return d20
	}
	p := math.Exp(ll0)
	pm := (1-lapse)*p + lapse/3
	a := (1 - lapse) * p
	return (a*(dz0*dz0+d20)*pm - (a*dz0)*(a*dz0)) / (pm * pm)
}

// answerCurvature is d²/dz² of the log-likelihood of one judgment (used to centre the adaptive quadrature).
func answerCurvature(y Answer, z, tau float64) float64 {
	switch y {
	case AnswerA:
		a := sigmoid(z - tau)
		return -a * (1 - a)
	case AnswerB:
		b := sigmoid(-z - tau)
		return -b * (1 - b)
	default:
		a, b := sigmoid(z-tau), sigmoid(-z-tau)
		pt := 1 - a - b
		if pt < 1e-12 {
			return 0
		}
		f := -a*(1-a) + b*(1-b)
		fp := -a*(1-a)*(1-2*a) - b*(1-b)*(1-2*b)
		return (fp*pt - f*f) / (pt * pt)
	}
}

// Probs returns (P(A), P(B), P(same)) at z, τ.
func Probs(z, tau float64) (pa, pb, pt float64) {
	pa, pb = sigmoid(z-tau), sigmoid(-z-tau)
	return pa, pb, math.Max(1-pa-pb, 1e-12)
}

// ghRule returns the n-point Gauss–Hermite nodes and weights (weight function e^{−x²}).
func ghRule(n int) (x, w []float64) {
	x, w = make([]float64, n), make([]float64, n)
	quad.Hermite{}.FixedLocations(x, w, math.Inf(-1), math.Inf(1))
	return x, w
}

// objective is the penalized negative log-likelihood and its gradient (independence or random intercept).
type objective struct {
	l      layout
	ds     []diff
	fold   [][ged.NumParams]float64 // Δφ with tied columns folded into their master
	trips  []Triplet
	js     []Judgment
	byTrip [][]int // judgments per triplet (mixed mode)
	opts   Options
	ridge  float64
	mixed  bool
	ghX    []float64
	ghLogW []float64
	// Adaptive quadrature (review-fit-impl M3, N12): per triplet, a trapezoid rule on the integrand
	// Π_k P'(y_k | z_k + u)·φ(u; σ) over [−7σ, 7σ] ∪ [û_t − 8 s_t, û_t + 8 s_t] with spacing min(broadStep·20/nq, s_t, σ/3),
	// where û_t and s_t are the mode and curvature scale of the log-integrand (Liu & Pierce 1994). The trapezoid rule converges exponentially on smooth integrands with Gaussian tails, resolves the
	// logistic-scale structure of contradictory judgments and the broad prior-shaped plateau that the lapse mixture's
	// floor ℓ/3 produces (which mode-centred Gauss–Hermite nodes, spaced ≈ σ apart, cannot), and the spacing bound s_t
	// resolves a narrow posterior. nodes[t][q] is the node and logc[t][q] = log(spacing): the integrand's own
	// log φ(u; σ) and Σ_k log P(y_k | z_k + u) are added in eval, so σ keeps an exact gradient. The nodes are computed
	// once per outer round at the round's starting parameters and held fixed inside the inner optimizer, so the inner
	// objective is smooth with exact gradients. nq (Options.Quadrature) is a resolution knob: 20 → spacing ≤ 0.6.
	nodes, logc [][]float64
	nllRaw      float64
}

func newObjective(l layout, ds []diff, trips []Triplet, js []Judgment, opts Options, ridge float64, mixed bool, at Params) *objective {
	o := &objective{l: l, ds: ds, trips: trips, js: js, opts: opts, ridge: ridge, mixed: mixed}
	o.fold = make([][ged.NumParams]float64, len(ds))
	for i, d := range ds {
		for p := range ged.NumParams {
			q := p
			if m, ok := opts.Tie[p]; ok {
				q = m
			}
			o.fold[i][q] += d.dphi[p]
		}
	}
	if mixed {
		o.byTrip = make([][]int, len(trips))
		for k, j := range js {
			o.byTrip[j.Triplet] = append(o.byTrip[j.Triplet], k)
		}
		nq := opts.Quadrature
		if nq <= 0 {
			nq = 20
		}
		o.ghX = make([]float64, nq) // only the length (the resolution knob) is used by recenter
		o.recenter(at)
	}
	return o
}

// recenter computes the mode û_t and the scale s_t = (−g″(û_t))^{−1/2} of g(u) = Σ_k log P(y_k | z_k + u) − u²/(2σ²)
// for every triplet at the parameters p (damped Newton; g is strictly concave because the Rao–Kupper
// probabilities are log-concave in z).
func (o *objective) recenter(p Params) {
	n := len(o.byTrip)
	o.nodes, o.logc = make([][]float64, n), make([][]float64, n)
	sigma := math.Max(p.Sigma, 1e-8)
	nq := len(o.ghX)
	build := func(t int, center, scale float64, logf func(u float64) float64) {
		// Support: the prior confines u to ±7σ (L ≤ 1), and the mode ± 8 s_t covers a likelihood-pulled posterior;
		// spacing: the logistic scale (broadStep at nq = 20), the posterior scale and the prior scale. Nodes whose
		// log-integrand at the round's start parameters lies more than pruneMargin below the maximum are dropped
		// (their total weight is < e^{−pruneMargin}·n; the inner optimizer moves the integrand little, and every
		// outer round rebuilds the nodes).
		step := math.Min(math.Min(broadStep*20/float64(nq), math.Max(scale, minStep)), sigma/3)
		lo := math.Min(-7*sigma, center-8*scale)
		hi := math.Max(7*sigma, center+8*scale)
		n := int(math.Ceil((hi-lo)/step)) + 1
		vals := make([]float64, n)
		best := math.Inf(-1)
		for i := range n {
			vals[i] = logf(lo + float64(i)*step)
			best = math.Max(best, vals[i])
		}
		o.nodes[t], o.logc[t] = make([]float64, 0, n), make([]float64, 0, n)
		for i := range n {
			if vals[i] >= best-pruneMargin {
				o.nodes[t] = append(o.nodes[t], lo+float64(i)*step)
				o.logc[t] = append(o.logc[t], math.Log(step))
			}
		}
	}
	for t, ks := range o.byTrip {
		if len(ks) == 0 {
			continue
		}
		D := o.delta(t, p)
		base := make([]float64, len(ks))
		for i, ki := range ks {
			k := o.js[ki]
			base[i] = p.Beta[k.Judge]*D + p.Pi[k.Judge]*k.Order + p.Gamma*o.trips[t].DeltaCoh()
		}
		g := func(u float64) (val, d1, d2 float64) {
			val, d1, d2 = -u*u/(2*sigma*sigma), -u/(sigma*sigma), -1/(sigma*sigma)
			for i, ki := range ks {
				k := o.js[ki]
				ll, dz, _, _ := mixLogLik(k.Answer, base[i]+u, p.Tau[k.Judge], p.Lapse[k.Judge])
				val += ll
				d1 += dz
				d2 += mixCurvature(k.Answer, base[i]+u, p.Tau[k.Judge], p.Lapse[k.Judge])
			}
			return val, d1, d2
		}
		u := 0.0
		val, d1, d2 := g(u)
		for it := 0; it < 50 && math.Abs(d1) > 1e-10; it++ {
			step := -d1 / math.Min(d2, -1e-12)
			for ls := 0; ls < 30; ls++ {
				v, nd1, nd2 := g(u + step)
				if v >= val-1e-12 {
					u, val, d1, d2 = u+step, v, nd1, nd2
					break
				}
				step /= 2
			}
		}
		build(t, u, 1/math.Sqrt(math.Max(-d2, 1/(sigma*sigma))), func(u float64) float64 { v, _, _ := g(u); return v })
	}
}

func (o *objective) weight(t int) float64 {
	if !o.opts.Weighted {
		return 1
	}
	return o.trips[t].weight()
}

// delta returns D_t = Δ_t(θ) + θ_neg·Δneg_t at the folded features.
func (o *objective) delta(t int, p Params) float64 {
	v := o.ds[t].c
	for q := range ged.NumParams {
		v += o.fold[t][q] * p.Theta[q]
	}
	return v + p.ThetaNeg*o.trips[t].NegDiff
}

// accum adds the gradient contribution of one judgment given dz, dtau (derivatives of its log-likelihood), scaled by w.
func (o *objective) accum(g []float64, k Judgment, p Params, D, dz, dtau, dl, w float64) {
	l := o.l
	j := k.Judge
	t := k.Triplet
	for q := range ged.NumParams {
		g[q] -= w * dz * p.Beta[j] * o.fold[t][q]
	}
	g[l.neg()] -= w * dz * p.Beta[j] * o.trips[t].NegDiff
	g[l.beta(j)] -= w * dz * D * p.Beta[j] // d/d log β
	g[l.tau(j)] -= w * dtau * p.Tau[j]     // d/d log τ
	g[l.pi(j)] -= w * dz * k.Order
	g[l.lapse(j)] -= w * dl * p.Lapse[j] * (1 - p.Lapse[j]) // d/d logit ℓ
	g[l.gamma()] -= w * dz * o.trips[t].DeltaCoh()
}

func (o *objective) eval(x, grad []float64) float64 {
	l := o.l
	p := l.unpack(x, o.opts.Tie, o.mixed)
	if grad != nil {
		clear(grad)
	}
	nll := 0.0
	if !o.mixed {
		for _, k := range o.js {
			t := k.Triplet
			w := o.weight(t)
			D := o.delta(t, p)
			j := k.Judge
			z := p.Beta[j]*D + p.Pi[j]*k.Order + p.Gamma*o.trips[t].DeltaCoh()
			ll, dz, dtau, dl := mixLogLik(k.Answer, z, p.Tau[j], p.Lapse[j])
			nll -= w * ll
			if grad != nil {
				o.accum(grad, k, p, D, dz, dtau, dl, w)
			}
		}
	} else {
		var llq []float64
		var dzq, dtq, dlq [][]float64
		for t, ks := range o.byTrip {
			if len(ks) == 0 {
				continue
			}
			nq := len(o.nodes[t])
			if cap(llq) < nq {
				llq = make([]float64, nq)
				dzq, dtq, dlq = make([][]float64, nq), make([][]float64, nq), make([][]float64, nq)
			}
			llq, dzq, dtq, dlq = llq[:nq], dzq[:nq], dtq[:nq], dlq[:nq]
			w := o.weight(t)
			D := o.delta(t, p)
			sig2 := p.Sigma * p.Sigma
			logPrior := -math.Log(p.Sigma) - 0.5*math.Log(2*math.Pi)
			for q := range nq {
				u := o.nodes[t][q]
				s := o.logc[t][q] + logPrior - u*u/(2*sig2)
				if grad != nil {
					dzq[q] = make([]float64, len(ks))
					dtq[q] = make([]float64, len(ks))
					dlq[q] = make([]float64, len(ks))
				}
				for i, ki := range ks {
					k := o.js[ki]
					j := k.Judge
					z := p.Beta[j]*D + p.Pi[j]*k.Order + p.Gamma*o.trips[t].DeltaCoh() + u
					ll, dz, dtau, dl := mixLogLik(k.Answer, z, p.Tau[j], p.Lapse[j])
					s += ll
					if grad != nil {
						dzq[q][i], dtq[q][i], dlq[q][i] = dz, dtau, dl
					}
				}
				llq[q] = s
			}
			lse := logSumExp(llq)
			nll -= w * lse
			if grad == nil {
				continue
			}
			for q := range nq {
				r := math.Exp(llq[q] - lse)
				if r < 1e-300 {
					continue
				}
				u := o.nodes[t][q]
				for i, ki := range ks {
					k := o.js[ki]
					o.accum(grad, k, p, D, dzq[q][i], dtq[q][i], dlq[q][i], w*r)
				}
				// The nodes are fixed; σ enters only through the prior density: d/dlogσ log φ(u; σ) = u²/σ² − 1.
				grad[l.logSigma()] -= w * r * (u*u/sig2 - 1)
			}
		}
	}
	o.nllRaw = nll

	f := nll
	free := FreeTheta(o.opts)
	for i := range ged.NumParams {
		if !free[i] {
			continue
		}
		dev := p.Theta[i] - o.opts.Theta0[i]
		f += o.opts.Lambda * dev * dev
		if grad != nil {
			grad[i] += 2 * o.opts.Lambda * dev
		}
	}
	f += o.ridge * p.ThetaNeg * p.ThetaNeg
	if grad != nil {
		grad[l.neg()] += 2 * o.ridge * p.ThetaNeg
	}
	for j := range l.j {
		lb, lt := x[l.beta(j)], x[l.tau(j)]
		f += o.ridge * (lb*lb + lt*lt + p.Pi[j]*p.Pi[j] + p.Lapse[j]*p.Lapse[j])
		if grad != nil {
			grad[l.beta(j)] += 2 * o.ridge * lb
			grad[l.tau(j)] += 2 * o.ridge * lt
			grad[l.pi(j)] += 2 * o.ridge * p.Pi[j]
			grad[l.lapse(j)] += 2 * o.ridge * p.Lapse[j] * p.Lapse[j] * (1 - p.Lapse[j])
		}
	}
	if grad != nil {
		for i := range ged.NumParams {
			if !free[i] {
				grad[i] = 0
			}
		}
		if !o.opts.FitNeg {
			grad[l.neg()] = 0
		}
		if o.opts.Gamma != GammaFree {
			grad[l.gamma()] = 0
		}
		if o.opts.NoLapse {
			for j := range l.j {
				grad[l.lapse(j)] = 0
			}
		}
		if !o.mixed {
			grad[l.logSigma()] = 0
		}
	}
	return f
}

func logSumExp(v []float64) float64 {
	m := math.Inf(-1)
	for _, x := range v {
		if x > m {
			m = x
		}
	}
	if math.IsInf(m, 0) {
		return m
	}
	s := 0.0
	for _, x := range v {
		s += math.Exp(x - m)
	}
	return m + math.Log(s)
}

// Init returns the initial values (θ = Theta0, β = 1, τ = 0.5, π = 0, γ = GammaValue if fixed, σ = Sigma0).
func Init(opts Options) Params {
	p := Params{Theta: opts.Theta0, Beta: make([]float64, opts.Judges), Tau: make([]float64, opts.Judges), Pi: make([]float64, opts.Judges), Lapse: make([]float64, opts.Judges)}
	for j := range opts.Judges {
		p.Beta[j], p.Tau[j], p.Lapse[j] = 1, 0.5, LapseInit
		if opts.NoLapse {
			p.Lapse[j] = LapseMin
		}
	}
	if opts.Gamma == GammaFixed {
		p.Gamma = opts.GammaValue
	}
	p.Sigma = opts.Sigma0
	if p.Sigma <= 0 {
		p.Sigma = 1
	}
	return p
}

// Fit estimates by alternating optimization under working independence. If init is nil, it starts from Init(opts).
// With opts.Starts set it runs every start and keeps the best true objective.
func Fit(trips []Triplet, js []Judgment, opts Options, init *Params) (Result, error) {
	return fitMulti(trips, js, opts, init, false)
}

// FitMixed is the random-intercept fit (Appendix A.6): one Gaussian offset per triplet shared by its judgments,
// integrated by Gauss–Hermite quadrature; parameters (θ, θ_neg, β, τ, π, γ, log σ).
func FitMixed(trips []Triplet, js []Judgment, opts Options, init *Params) (Result, error) {
	return fitMulti(trips, js, opts, init, true)
}

// FitMulti runs Fit from the three pre-registered starts (θ₀, R1, all-0.5) plus init, keeping the best (decisions §11.3).
func FitMulti(trips []Triplet, js []Judgment, opts Options, init *Params) (Result, error) {
	if len(opts.Starts) == 0 {
		opts.Starts, opts.StartNames = DefaultStarts(opts.Theta0)
	}
	return fitMulti(trips, js, opts, init, false)
}

// FitMixedMulti is FitMixed with the pre-registered multi-start.
func FitMixedMulti(trips []Triplet, js []Judgment, opts Options, init *Params) (Result, error) {
	if len(opts.Starts) == 0 {
		opts.Starts, opts.StartNames = DefaultStarts(opts.Theta0)
	}
	return fitMulti(trips, js, opts, init, true)
}

func fitMulti(trips []Triplet, js []Judgment, opts Options, init *Params, mixed bool) (Result, error) {
	if opts.Judges <= 0 {
		return Result{}, errors.New("fit: Judges must be positive")
	}
	if opts.Weights != nil && len(opts.Weights) != len(trips) {
		return Result{}, fmt.Errorf("fit: len(Weights) = %d, want %d", len(opts.Weights), len(trips))
	}
	if err := checkWeights(trips, opts); err != nil {
		return Result{}, err
	}
	trips, js, _ = Filter(trips, js, opts)
	opts.Weights = nil // materialised into Triplet.Weight by Filter
	if len(js) == 0 {
		return Result{}, errors.New("fit: no judgments after filtering")
	}
	if len(opts.Starts) == 0 {
		return fitOnce(trips, js, opts, init, mixed)
	}
	type start struct {
		name string
		p    Params
	}
	var starts []start
	if init != nil {
		starts = append(starts, start{"init", clone(*init)})
	}
	for i, th := range opts.Starts {
		p := Init(opts)
		if init != nil {
			p = clone(*init)
		}
		p.Theta = th
		name := fmt.Sprintf("start %d", i+1)
		if i < len(opts.StartNames) {
			name = opts.StartNames[i]
		}
		starts = append(starts, start{name, p})
	}
	var best Result
	bestI := -1
	var errs []error
	results := make([]Result, len(starts))
	for i, s := range starts {
		r, err := fitOnce(trips, js, opts, &s.p, mixed)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.name, err))
			continue
		}
		results[i] = r
		if bestI < 0 || r.Objective < best.Objective {
			best, bestI = r, i
		}
	}
	if bestI < 0 {
		return Result{}, errors.Join(errs...)
	}
	for i, s := range starts {
		r := results[i]
		if r.Params.Beta == nil {
			continue
		}
		best.Starts = append(best.Starts, StartResult{Name: s.name, Theta: r.Params.Theta, Objective: r.Objective, NegLogLik: r.NegLogLik, Converged: r.Converged, Cycled: r.Cycled})
		for p := range ged.NumParams {
			best.StartSpread = math.Max(best.StartSpread, math.Abs(r.Params.Theta[p]-best.Params.Theta[p]))
		}
	}
	return best, nil
}

// fitOnce is the alternating optimization from one start (filters already applied).
func fitOnce(trips []Triplet, js []Judgment, opts Options, init *Params, mixed bool) (Result, error) {
	if opts.MaxOuter <= 0 {
		opts.MaxOuter = 20
	}
	p := Init(opts)
	if init != nil {
		p = clone(*init)
		if len(p.Beta) != opts.Judges {
			return Result{}, fmt.Errorf("fit: init has %d judges, want %d", len(p.Beta), opts.Judges)
		}
	}
	if opts.FixTheta {
		p.Theta = opts.Theta0
	}
	if !opts.FitNeg {
		p.ThetaNeg = 0
	}
	for i, v := range opts.Fixed {
		p.Theta[i] = v
	}
	for i, m := range opts.Tie {
		p.Theta[i] = p.Theta[m]
	}
	switch opts.Gamma {
	case GammaOff:
		p.Gamma = 0
	case GammaFixed:
		p.Gamma = opts.GammaValue
	}
	if len(p.Lapse) != opts.Judges {
		p.Lapse = make([]float64, opts.Judges)
		for j := range p.Lapse {
			p.Lapse[j] = LapseInit
		}
	}
	if opts.NoLapse {
		for j := range p.Lapse {
			p.Lapse[j] = LapseMin
		}
	}
	if mixed && p.Sigma <= 0 {
		p.Sigma = opts.Sigma0
		if p.Sigma <= 0 {
			p.Sigma = 1
		}
	}
	for i := range ged.NumParams {
		p.Theta[i] = clampTo(p.Theta[i], 0, ThetaMax)
	}
	l := layout{j: opts.Judges}
	lo, hi := l.bounds(opts, p, mixed)

	ds, err := features(trips, p.Theta)
	if err != nil {
		return Result{}, err
	}
	ridge := opts.AuxRidge
	if ridge <= 0 {
		ridge = 1e-4
	}
	seen := map[string]bool{sigOf(ds): true}
	best, bestF := Result{}, math.Inf(1)
	prevTrue := math.Inf(1)
	finish := func(r Result) Result {
		r.Free = FreeTheta(opts)
		r.Mixed = mixed
		r.Triplets, r.Judgments = len(trips), len(js)
		for q := range ged.NumParams {
			if !r.Free[q] {
				continue
			}
			r.Boundary[q] = r.Params.Theta[q] < BoundaryEps
			r.Divergent[q] = r.Params.Theta[q] >= ThetaMax-1e-9
		}
		if !mixed {
			r.Params.Sigma = 0
		}
		return r
	}
	for outer := 1; outer <= opts.MaxOuter; outer++ {
		obj := newObjective(l, ds, trips, js, opts, ridge, mixed, p)
		br := projLBFGS(obj.eval, l.pack(p, mixed), lo, hi, boxOptions{})
		if math.IsNaN(br.F) || math.IsInf(br.F, 0) {
			return best, errors.New("fit: objective is not finite")
		}
		p = l.unpack(br.X, opts.Tie, mixed)

		// Re-solve the matching at this θ (and re-centre the quadrature) and measure the objective under real GED.
		next, err := features(trips, p.Theta)
		if err != nil {
			return best, err
		}
		trueObj := newObjective(l, next, trips, js, opts, ridge, mixed, p)
		f := trueObj.eval(br.X, nil)
		cur := Result{Params: p, NegLogLik: trueObj.nllRaw, Objective: f, Outer: outer, InnerConverged: br.Converged}
		if f < bestF {
			best, bestF = cur, f
		}
		sig := sigOf(next)
		centersStable := !mixed || math.Abs(f-br.F) < 1e-6*math.Max(1, math.Abs(f))
		if sig == sigOf(ds) {
			if centersStable {
				cur.Converged = true
				return finish(cur), nil
			}
			// Same matching but the adaptive quadrature centres moved: another inner round at the new centres.
			ds, prevTrue = next, f
			continue
		}
		if math.Abs(f-prevTrue) < 1e-6*math.Max(1, math.Abs(f)) && centersStable {
			// Objective-based stopping (decisions §11.2): the matching moved but the true objective did not.
			best.Converged = true
			best.Outer = outer
			return finish(best), nil
		}
		if seen[sig] {
			best.Cycled, best.Outer = true, outer
			return finish(best), nil
		}
		seen[sig] = true
		ds = next
		prevTrue = f
	}
	best.Outer = opts.MaxOuter
	best.Capped = true
	return finish(best), nil
}

func sigOf(ds []diff) string {
	h := sha256.New()
	for _, d := range ds {
		h.Write([]byte(d.sig))
		h.Write([]byte{0})
	}
	return string(h.Sum(nil))
}

func clone(p Params) Params {
	q := p
	q.Beta = append([]float64(nil), p.Beta...)
	q.Tau = append([]float64(nil), p.Tau...)
	q.Pi = append([]float64(nil), p.Pi...)
	q.Lapse = append([]float64(nil), p.Lapse...)
	return q
}

// Clone returns a deep copy of p.
func (p Params) Clone() Params { return clone(p) }

// lapse returns ℓ_j (0 when Lapse is unset).
func (p Params) lapse(j int) float64 {
	if j < len(p.Lapse) {
		return p.Lapse[j]
	}
	return 0
}

// LapseOf is the exported form of lapse.
func (p Params) LapseOf(j int) float64 { return p.lapse(j) }

// NegLogLik is the negative log-likelihood at the given parameters (no penalty; matching solved at p.Theta;
// working independence; no filters or weights).
func NegLogLik(trips []Triplet, js []Judgment, p Params) (float64, error) {
	ll, err := LogLik(trips, js, p)
	if err != nil {
		return 0, err
	}
	s := 0.0
	for _, v := range ll {
		s -= v
	}
	return s, nil
}

// LogLik returns the per-judgment log-likelihood at p (working independence; matching solved at p.Theta).
func LogLik(trips []Triplet, js []Judgment, p Params) ([]float64, error) {
	ds, err := features(trips, p.Theta)
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(js))
	for i, k := range js {
		D := ds[k.Triplet].value(p.Theta) + p.ThetaNeg*trips[k.Triplet].NegDiff
		z := p.Beta[k.Judge]*D + p.Pi[k.Judge]*k.Order + p.Gamma*trips[k.Triplet].DeltaCoh()
		ll, _, _, _ := mixLogLik(k.Answer, z, p.Tau[k.Judge], p.lapse(k.Judge))
		out[i] = ll
	}
	return out, nil
}

// NegLogLikMixed is the marginal negative log-likelihood of the random-intercept model at p (no penalty).
func NegLogLikMixed(trips []Triplet, js []Judgment, p Params, nodes int) (float64, error) {
	ds, err := features(trips, p.Theta)
	if err != nil {
		return 0, err
	}
	opts := Options{Judges: len(p.Beta), Quadrature: nodes, FitNeg: true, Gamma: GammaFree}
	p.Sigma = math.Max(p.Sigma, 1e-3) // the same floor pack applies, so the centring and the prior agree
	l := layout{j: opts.Judges}
	o := newObjective(l, ds, trips, js, opts, 0, true, p)
	o.eval(l.pack(p, true), nil)
	return o.nllRaw, nil
}

// ExclusionCounts counts, per flag in the effective exclusion list, the triplets it removes ("excluded and counted",
// decisions #15), plus the Δcoh ≠ 0 count when CohZeroOnly is set.
func ExclusionCounts(trips []Triplet, opts Options) map[string]int {
	excl := opts.Exclude
	if excl == nil && !opts.IncludeAll {
		excl = DefaultExclude
	}
	if opts.IncludeAll {
		excl = nil
	}
	out := map[string]int{}
	for _, f := range excl {
		out[f] = 0
	}
	for _, t := range trips {
		for _, f := range excl {
			if t.HasFlag(f) {
				out[f]++
			}
		}
		if opts.CohZeroOnly && math.Abs(t.DeltaCoh()) > 1e-12 {
			out["Δcoh≠0"]++
		}
	}
	return out
}

// RandomStarts draws n starting cost vectors uniformly on [0.1, 2] (the stand-in for the CMA-ES check of §11.3).
func RandomStarts(n int, seed uint64) []ged.Theta {
	rng := rand.New(rand.NewPCG(seed, 17))
	out := make([]ged.Theta, n)
	for i := range out {
		for p := range ged.NumParams {
			out[i][p] = 0.1 + 1.9*rng.Float64()
		}
	}
	return out
}

func equalFold(a, b string) bool { return strings.EqualFold(a, b) }

// Filter applies the triplet and judgment filters of opts and materialises Options.Weights into Triplet.Weight.
// It returns the kept triplets (renumbered), their judgments, and the original index of every kept triplet.
// With no flags, families or weights set, it is the identity.
func Filter(trips []Triplet, js []Judgment, opts Options) ([]Triplet, []Judgment, []int) {
	excl := opts.Exclude
	if excl == nil && !opts.IncludeAll {
		excl = DefaultExclude
	}
	if opts.IncludeAll {
		excl = nil
	}
	keep := make([]bool, len(trips))
	kept := make([]int, 0, len(trips))
	pos := make([]int, len(trips))
	out := make([]Triplet, 0, len(trips))
	for i, t := range trips {
		ok := true
		for _, f := range excl {
			if t.HasFlag(f) {
				ok = false
				break
			}
		}
		if opts.CohZeroOnly && math.Abs(t.DeltaCoh()) > 1e-12 {
			ok = false
		}
		keep[i] = ok
		if !ok {
			pos[i] = -1
			continue
		}
		if opts.Weights != nil {
			t.Weight = opts.Weights[i]
		}
		pos[i] = len(out)
		out = append(out, t)
		kept = append(kept, i)
	}
	oj := make([]Judgment, 0, len(js))
	for _, k := range js {
		if k.Triplet < 0 || k.Triplet >= len(trips) || !keep[k.Triplet] {
			continue
		}
		if opts.ExcludeSameFamily && k.Family != "" && trips[k.Triplet].VerifierFamily != "" &&
			strings.EqualFold(k.Family, trips[k.Triplet].VerifierFamily) {
			continue
		}
		k.Triplet = pos[k.Triplet]
		oj = append(oj, k)
	}
	return out, oj, kept
}
