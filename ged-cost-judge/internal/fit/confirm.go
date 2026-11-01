package fit

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/stats"
)

// Confirmatory is the single entry point of the pre-registered analysis (decisions-stats §2–§4, §6, §7, §9, §9.1,
// §11, §12b; review-fit-impl B1). It runs, in this order: the mechanical judge QC on the QC set (every triplet but
// pilot and qt30, before any θ is fitted), the triplet/judgment filters, the informative counts, the multi-start working-independence
// and random-intercept fits, the paired stratified bootstrap with its metadata, the sum normalization, the §3
// decision rule with every safeguard (information floor on the anchored replicates, replicate floor, boundary rule,
// scale identification, model agreement), the confirmatory contrasts with Holm, the nested-bootstrap D, the verdict,
// the leave-one-judge-out table and the matching-stability share.
//
// The report functions format a ConfirmResult only; no other path produces §3 labels. Callers (simulate, pilot, the
// final analysis) must use this function and report.WriteConfirmatory, not the pieces.
func Confirmatory(trips []Triplet, js []Judgment, opts Options, co ConfirmOptions) (ConfirmResult, error) {
	co = co.withDefaults(opts)
	res := ConfirmResult{Options: co}
	if opts.Judges <= 0 {
		return res, errors.New("confirmatory: Judges must be positive")
	}
	if err := checkWeights(trips, opts); err != nil {
		return res, err
	}
	// Primary-fit settings are forced: tie on, λ = 0, θ_neg fitted (decisions §5, §12b).
	if opts.Tie == nil && !co.Untied {
		opts.Tie = TypeTie()
	}
	opts.Lambda = 0
	opts.FitNeg = true
	opts.Theta0 = co.Theta0
	if len(co.JudgeNames) != opts.Judges {
		co.JudgeNames = make([]string, opts.Judges)
		for j := range co.JudgeNames {
			co.JudgeNames[j] = fmt.Sprintf("judge %d", j+1)
		}
		res.Options.JudgeNames = co.JudgeNames
	}

	res.Deviations = Preregistered(co, opts)
	res.Used = summarise(co, opts)

	// 1. Judge QC before θ (decisions §9, §9.1, §13 item 5) on the QC set: every triplet but pilot and qt30 (re-review
	// N3), fixed regardless of opts so that a sensitivity fit's exclusions cannot move the gate, the tie share or α.
	qcT, qcJ := qcSet(trips, js)
	res.QC = JudgeQCStats(qcT, qcJ, co.JudgeNames, QCOptions{GateDelta: co.GateDelta, RMax: co.RMax, Conf: co.GateConf})
	names := co.JudgeNames
	if co.ApplyExclusions {
		for j := opts.Judges - 1; j >= 0; j-- {
			if res.QC.Excluded[j] {
				js = DropJudge(js, j)
				names = slices.Delete(slices.Clone(names), j, j+1)
				res.ExcludedJudges = append(res.ExcludedJudges, co.JudgeNames[j])
			}
		}
		opts.Judges = len(names)
		if opts.Judges == 0 {
			return res, errors.New("confirmatory: every judge failed the QC")
		}
	}
	res.JudgeNames = names

	// 2. Filters, applied once (review-fit-impl M7); everything below runs on the estimation set.
	res.Exclusions = ExclusionCounts(trips, opts)
	nTripsBefore := len(trips)
	keepT, keepJ := filterMasks(trips, js, opts)
	for i, k := range js {
		if k.Triplet >= 0 && k.Triplet < len(keepT) && keepT[k.Triplet] && !keepJ[i] {
			res.JudgmentsDropped++ // dropped by the family-coupling filter, not with their triplet
		}
	}
	var kept []int
	trips, js, kept = Filter(trips, js, opts)
	opts.Weights, opts.IncludeAll, opts.Exclude, opts.ExcludeSameFamily, opts.CohZeroOnly = nil, true, nil, false, false
	res.Triplets, res.Judgments = len(trips), len(js)
	if len(trips) == 0 || len(js) == 0 {
		return res, errors.New("confirmatory: no data after filtering")
	}
	labels := co.StratumLabels
	if labels != nil {
		if len(labels) != nTripsBefore {
			return res, errors.New("confirmatory: StratumLabels must have one entry per (unfiltered) triplet")
		}
		lab := make([]string, len(trips))
		for i, o := range kept {
			lab[i] = labels[o]
		}
		labels = lab
	} else {
		var err error
		labels, err = DefaultStrata(trips, co.Theta0)
		if err != nil {
			return res, err
		}
	}
	res.StratumLabels = labels
	baselines := make([]RuleDelta, 0, len(co.Baselines))
	for _, b := range co.Baselines {
		d := make([]float64, len(trips))
		for i, o := range kept {
			if o < len(b.Delta) {
				d[i] = b.Delta[o]
			}
		}
		baselines = append(baselines, RuleDelta{ID: b.ID, Name: b.Name, Delta: d})
	}

	// 3. Informative counts at θ₀ (decisions §15.3).
	var err error
	res.Informative, err = Informative(trips, co.Theta0, opts.Tie)
	if err != nil {
		return res, err
	}
	for _, t := range trips {
		if t.NegDiff != 0 {
			res.NegInformative++
		}
	}

	res.Quadrature = res.Used.Quadrature

	// 4. Multi-start fits (decisions §11.3; random starts stand in for the CMA-ES check).
	starts, startNames := DefaultStarts(co.Theta0)
	if co.RandomStarts > 0 {
		for i, th := range RandomStarts(co.RandomStarts, co.Seed+7) {
			starts = append(starts, th)
			startNames = append(startNames, fmt.Sprintf("random %d", i+1))
		}
	}
	fo := opts
	fo.Starts, fo.StartNames = starts, startNames
	res.Indep, err = Fit(trips, js, fo, nil)
	if err != nil {
		return res, fmt.Errorf("confirmatory: independence fit: %w", err)
	}
	var mixedInit *Params
	if !co.SkipMixed {
		res.Mixed, err = FitMixed(trips, js, fo, nil)
		if err != nil {
			return res, fmt.Errorf("confirmatory: mixed fit: %w", err)
		}
		mixedInit = &res.Mixed.Params
		if res.Mixed.Params.Sigma > co.HighNodesSigma {
			// Grid check: refit from the estimate at the finer resolution co.HighNodes (80 → spacing ≤ 0.15 logit) and
			// report the largest change (review-fit-impl M3).
			ho := opts
			ho.Quadrature = co.HighNodes
			hr, err := FitMixed(trips, js, ho, mixedInit)
			if err != nil {
				return res, fmt.Errorf("confirmatory: high-node refit: %w", err)
			}
			res.MixedCheck = &hr
			for p := range ged.NumParams {
				res.MixedCheckMaxDiff = math.Max(res.MixedCheckMaxDiff, math.Abs(hr.Params.Theta[p]-res.Mixed.Params.Theta[p]))
			}
			res.MixedCheckSigmaDiff = hr.Params.Sigma - res.Mixed.Params.Sigma
		}
	}

	// 5. Paired bootstrap (decisions §6).
	res.Boot, err = BootstrapOpts(trips, js, opts, res.Indep.Params, BootOptions{Reps: co.B, Seed: co.Seed + 100, Mixed: !co.SkipMixed, MixedInit: mixedInit, Workers: co.Workers})
	if err != nil {
		return res, fmt.Errorf("confirmatory: bootstrap: %w", err)
	}

	// 6. Decisions on the normalized scale (decisions §2, §3).
	res.Params, res.SHat, res.SHatCI, res.ScaleIdentified = decisions(res.Indep, res.Boot.Theta, res.Boot.Meta, mixedOrNil(res), res.Boot.MixedTheta, res.Boot.MixedMeta, res.Informative, res.NegInformative, co, opts.Tie)
	for p, m := range opts.Tie {
		res.Extrapolated = append(res.Extrapolated, ExtrapolatedRow{Index: p, Master: m})
	}
	sort.Slice(res.Extrapolated, func(i, j int) bool { return res.Extrapolated[i].Index < res.Extrapolated[j].Index })

	// 7. Confirmatory contrasts (decisions §4).
	res.Hypotheses, res.C4Missing, err = hypotheses(trips, js, res, baselines, co)
	if err != nil {
		return res, err
	}
	if len(res.Hypotheses) != HolmFamily {
		return res, fmt.Errorf("confirmatory: Holm family has %d members, want %d (decisions §4; re-review N5)", len(res.Hypotheses), HolmFamily)
	}
	res.HolmFamily = HolmFamily
	if res.C4Missing {
		res.Notes = append(res.Notes, "C4 baseline (B1, whole-text embedding) not supplied: C4 enters the Holm family of 6 with p = 1")
	}
	if !res.ScaleIdentified {
		// Only the contrasts on the normalized scale lose their meaning; the C-family compares agreement rates.
		for i := range res.Hypotheses {
			if h := &res.Hypotheses[i]; h.ID == "H1" || h.ID == "H2" || h.ID == "H4a" || h.ID == "H4b" {
				h.NotEvaluable, h.Supported = true, false
			}
		}
	}

	// 8. D criterion (decisions §7).
	if !co.SkipD {
		d, err := NestedD(trips, js, opts, co.Theta0, labels, DOptions{Reps: co.DReps, Seed: co.Seed + 200, K: 5, Repeats: co.DRepeats, Workers: co.Workers})
		if err != nil {
			return res, fmt.Errorf("confirmatory: nested D: %w", err)
		}
		res.D = &d
	}

	// 9. Verdict (decisions §3), before and after the deviation cap.
	res.VerdictUncapped, res.Verdict, res.Missing = verdict(res, co)

	// 10. Leave-one-judge-out (decisions §9).
	if !co.SkipLOJO && opts.Judges >= 2 {
		for j := range opts.Judges {
			o := opts
			o.Judges--
			dj := DropJudge(js, j)
			fo := o
			fo.Starts, fo.StartNames = starts, startNames
			ind, err := Fit(trips, dj, fo, nil)
			if err != nil {
				return res, fmt.Errorf("confirmatory: LOJO %s: %w", names[j], err)
			}
			var mix *Result
			var mInit *Params
			if !co.SkipMixed {
				m, err := FitMixed(trips, dj, fo, nil)
				if err != nil {
					return res, fmt.Errorf("confirmatory: LOJO %s mixed: %w", names[j], err)
				}
				mix, mInit = &m, &m.Params
			}
			b, err := BootstrapOpts(trips, dj, o, ind.Params, BootOptions{Reps: co.LOJOReps, Seed: co.Seed + 300 + uint64(j), Mixed: !co.SkipMixed, MixedInit: mInit, Workers: co.Workers})
			if err != nil {
				return res, fmt.Errorf("confirmatory: LOJO %s bootstrap: %w", names[j], err)
			}
			rows, _, _, _ := decisions(ind, b.Theta, b.Meta, mix, b.MixedTheta, b.MixedMeta, res.Informative, res.NegInformative, co, opts.Tie)
			res.LOJO = append(res.LOJO, LOJOResult{Judge: names[j], Params: rows})
		}
	}

	// 11. Matching stability in θ̂ ± 3·SD (decisions §11.6).
	var half [ged.NumParams]float64
	for p := range half {
		col := Column(res.Boot.Theta, p)
		half[p] = 3 * (Quantile(col, 0.8413) - Quantile(col, 0.1587)) / 2
	}
	res.Stability, err = MatchingStability(trips, res.Indep.Params.Theta, half, 64, co.Seed+400, opts.Tie)
	if err != nil {
		return res, err
	}
	return res, nil
}

// QCGateExclude are the flags kept out of the judge QC set (decisions §13, §15.9; re-review N3): the pilot and the
// QT30 transfer triplets. Nothing else is excluded, whatever the fit's own filters are.
var QCGateExclude = []string{"pilot", "qt30"}

// qcSet is the judge QC set: every triplet but QCGateExclude, with all judgments.
func qcSet(trips []Triplet, js []Judgment) ([]Triplet, []Judgment) {
	t, j, _ := Filter(trips, js, Options{Exclude: QCGateExclude})
	return t, j
}

// HolmFamily is the size of the Holm family of decisions §4: H1, H2, H4a, H4b, C1, C4 (C4 with p = 1 when its
// baseline is missing; re-review N5).
const HolmFamily = 6

func mixedOrNil(r ConfirmResult) *Result {
	if r.Mixed.Params.Beta == nil {
		return nil
	}
	m := r.Mixed
	return &m
}

// ConfirmOptions configures Confirmatory. Zero values take the pre-registered defaults.
type ConfirmOptions struct {
	Theta0 ged.Theta // reference table (zero → ged.Current())
	M      float64   // equivalence margin (0 → 0.25)
	U      []int     // normalization set (nil → DefaultU)
	B      int       // bootstrap replicates (0 → 1000)
	DReps  int       // nested-D replicates (0 → 200)
	// DRepeats is the number of CV repeats averaged for the point D (0 → 4).
	DRepeats int
	// LOJOReps is the bootstrap size of each leave-one-judge-out refit (0 → B).
	LOJOReps int
	Seed     uint64
	// MinInformative maps a normalized index to its §15.3 floor; nil → DefaultMinInformative(); an empty map disables the floor.
	MinInformative map[int]int
	// RandomStarts adds random starts to θ₀ / R1 / all-0.5 (0 → 2; < 0 → none).
	RandomStarts int
	// Baselines are extra rules for the C-family (each becomes agree(R0) − agree(rule)); Delta is indexed by the
	// unfiltered triplets. R1 (C1) is always included; pass {ID: "C4", …} for B1.
	Baselines []RuleDelta
	// StratumLabels gives a D stratum per unfiltered triplet (nil → DefaultStrata).
	StratumLabels []string
	JudgeNames    []string
	// ApplyExclusions drops judges that fail the mechanical QC before θ is fitted (default true; set SkipExclusions to keep them).
	ApplyExclusions bool
	SkipExclusions  bool
	// SkipMixed / SkipD / SkipLOJO omit parts for quick runs; every skip is a deviation (the verdict then cannot be "validated").
	SkipMixed, SkipD, SkipLOJO bool
	// Untied keeps the three type costs free (diagnostic fit of §12b); the primary analysis is tied.
	Untied  bool
	Workers int
	// HighNodes and HighNodesSigma control the grid check: when σ̂ > HighNodesSigma (0 → 2) the mixed fit is refitted
	// at the finer grid resolution HighNodes (0 → 80, i.e. spacing ≤ 0.15 logit; Options.Quadrature) and the change
	// in θ̂ and σ̂ is reported.
	HighNodes      int
	HighNodesSigma float64
	// Gate parameters of §9.1 (0 → 2.0, 0.20, 0.90); MinGateTriplets is the n_T below which the gate is not evaluable
	// and "validated" is blocked (0 → 100; < 0 → no floor).
	GateDelta, RMax, GateConf float64
	MinGateTriplets           int
	// Truth (simulation only) adds the true values to the table.
	Truth   *ged.Theta
	TrueNeg *float64
}

func (co ConfirmOptions) withDefaults(opts Options) ConfirmOptions {
	var zero ged.Theta
	if co.Theta0 == zero {
		co.Theta0 = ged.Current()
		if opts.Theta0 != zero {
			co.Theta0 = opts.Theta0
		}
	}
	if co.M <= 0 {
		co.M = 0.25
	}
	if co.U == nil {
		co.U = DefaultU
	}
	if co.B <= 0 {
		co.B = 1000
	}
	if co.DReps <= 0 {
		co.DReps = 200
	}
	if co.DRepeats <= 0 {
		co.DRepeats = 4
	}
	if co.LOJOReps <= 0 {
		co.LOJOReps = co.B
	}
	if co.MinInformative == nil {
		co.MinInformative = DefaultMinInformative()
	}
	if co.RandomStarts == 0 {
		co.RandomStarts = 2
	}
	co.ApplyExclusions = !co.SkipExclusions
	if co.HighNodes <= 0 {
		co.HighNodes = 80
	}
	if co.HighNodesSigma <= 0 {
		co.HighNodesSigma = 2
	}
	if co.GateDelta <= 0 {
		co.GateDelta = 2
	}
	if co.RMax <= 0 {
		co.RMax = stats.GateRMax
	}
	if co.GateConf <= 0 {
		co.GateConf = stats.GateConf
	}
	if co.MinGateTriplets == 0 {
		co.MinGateTriplets = 100
	}
	return co
}

// OptionsUsed summarises the fit options actually applied by Confirmatory.
type OptionsUsed struct {
	Lambda            float64
	Tie               bool
	Exclude           []string
	Weighted          bool
	Gamma             GammaMode
	ExcludeSameFamily bool
	CohZeroOnly       bool
	NoLapse           bool
	// Quadrature is the grid resolution of the random-intercept integration (Options.Quadrature; 20 → spacing ≤ 0.6).
	Quadrature int
	Judges     int
}

func summarise(co ConfirmOptions, opts Options) OptionsUsed {
	excl := opts.Exclude
	if excl == nil && !opts.IncludeAll {
		excl = DefaultExclude
	}
	if opts.IncludeAll {
		excl = nil
	}
	q := opts.Quadrature
	if q <= 0 {
		q = 20
	}
	return OptionsUsed{Lambda: opts.Lambda, Tie: !co.Untied, Exclude: slices.Clone(excl), Weighted: opts.Weighted, Gamma: opts.Gamma,
		ExcludeSameFamily: opts.ExcludeSameFamily, CohZeroOnly: opts.CohZeroOnly, NoLapse: opts.NoLapse, Quadrature: q, Judges: opts.Judges}
}

// Preregistered lists every departure of (co, opts) from the pre-registered primary settings of decisions-stats
// (§1 #1, #5, #6, #7, #9, #12, #15; §3; §9; §11; §12b; §12c; §16 #16). Confirmatory records the list and caps the
// verdict at "not contradicted" when it is not empty; the §12c sensitivity fits are expected to appear here.
func Preregistered(co ConfirmOptions, opts Options) []string {
	co = co.withDefaults(opts)
	var d []string
	add := func(format string, a ...any) { d = append(d, fmt.Sprintf(format, a...)) }
	if co.Theta0 != ged.Current() {
		add("θ₀ is not the current table")
	}
	if co.M != 0.25 {
		add("m = %g (pre-registered 0.25)", co.M)
	}
	if !slices.Equal(co.U, DefaultU) {
		add("normalization set U differs from the pre-registered six unit-type costs")
	}
	if co.B != 1000 {
		add("B = %d (pre-registered 1000)", co.B)
	}
	if co.DReps != 200 {
		add("nested-D replicates = %d (pre-registered 200)", co.DReps)
	}
	if co.DRepeats != 4 {
		add("CV repeats = %d (pre-registered 4)", co.DRepeats)
	}
	if co.LOJOReps != co.B {
		add("leave-one-judge-out bootstrap B = %d ≠ B", co.LOJOReps)
	}
	if !equalIntMap(co.MinInformative, DefaultMinInformative()) {
		add("minimum informative counts differ from §15.3 (or the floor is off)")
	}
	if co.RandomStarts != 2 {
		add("random starts = %d (pre-registered 2)", co.RandomStarts)
	}
	if co.SkipExclusions {
		add("judge QC exclusions not applied")
	}
	if co.SkipMixed {
		add("random-intercept fit skipped")
	}
	if co.SkipD {
		add("D criterion skipped")
	}
	if co.SkipLOJO {
		add("leave-one-judge-out skipped")
	}
	if co.Untied {
		add("type tie off (diagnostic fit)")
	}
	if co.HighNodes != 80 || co.HighNodesSigma != 2 {
		add("grid check settings differ from resolution 80 at σ̂ > 2")
	}
	if co.GateDelta != 2 || co.RMax != stats.GateRMax || co.GateConf != stats.GateConf || co.MinGateTriplets != 100 {
		add("T1 gate settings differ from §9.1")
	}
	if opts.Lambda != 0 {
		// Cannot fire from Confirmatory (it forces λ = 0 first); kept for callers that list their own options.
		add("λ = %g (pre-registered 0)", opts.Lambda)
	}
	if len(opts.Fixed) > 0 {
		add("fixed cost coordinates")
	}
	if opts.FixTheta {
		add("θ fixed")
	}
	if opts.MaxOuter != 0 && opts.MaxOuter != 20 {
		add("MaxOuter = %d (pre-registered 20)", opts.MaxOuter)
	}
	if opts.Tie != nil && !equalIntMap(opts.Tie, TypeTie()) {
		add("custom cost tie (pre-registered: the three type costs tied to RA↔MA)")
	}
	excl := opts.Exclude
	if excl == nil && !opts.IncludeAll {
		excl = DefaultExclude
	}
	if opts.IncludeAll || !sameSet(excl, DefaultExclude) {
		add("triplet exclusion list differs from the default (x_spk, diag_ca_type, qt30, del_add_cooccur, pilot)")
	}
	if opts.Weighted || opts.Weights != nil {
		add("importance-weighted likelihood (§12c sensitivity)")
	}
	if opts.Gamma != GammaOff {
		add("coherence covariate γ in the model (§10 sensitivity)")
	}
	if opts.ExcludeSameFamily {
		add("family-coupling filter on (§12c sensitivity)")
	}
	if opts.CohZeroOnly {
		add("restricted to Δcoh = 0 (§10 sensitivity b)")
	}
	if opts.NoLapse {
		add("lapse mixture off (pre-2026-10-29 model)")
	}
	if opts.Quadrature != 0 && opts.Quadrature != 20 {
		add("integration grid resolution = %d (pre-registered 20: spacing ≤ 0.6 logit)", opts.Quadrature)
	}
	if opts.AuxRidge != 0 && opts.AuxRidge != 1e-4 {
		add("AuxRidge = %g (pre-registered 1e-4)", opts.AuxRidge)
	}
	return d
}

func equalIntMap(a, b map[int]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

// DefaultMinInformative are the §15.3 minimum informative counts at 3,000 triplets (normalized indices).
func DefaultMinInformative() map[int]int {
	return map[int]int{
		ged.PNodeMissing: 650, ged.PNodeExtra: 650, ged.PEdgeMissing: 650, NormEdgeExtra: 650,
		ged.PTypeRAMA: 450, ged.PDirection: 650, NormNeg: 300,
	}
}

// RuleDelta is a fixed rule's Δ per triplet, for the C-family comparisons.
type RuleDelta struct {
	ID, Name string
	Delta    []float64
}

// ConfirmResult is everything the confirmatory report prints.
type ConfirmResult struct {
	Options ConfirmOptions
	// Used summarises the fit options actually applied; Deviations lists every departure from the pre-registered
	// settings (non-empty caps the verdict at "not contradicted"); Notes are non-capping remarks.
	Used       OptionsUsed
	Deviations []string
	Notes      []string
	// Quadrature is the grid resolution of the mixed fit's integration (OptionsUsed.Quadrature).
	Quadrature int
	// HolmFamily is the size of the Holm family (the constant HolmFamily; C4 is kept with p = 1 when its baseline is missing, C4Missing).
	HolmFamily int
	C4Missing  bool
	// Judge QC (on the QC set: every triplet but pilot and qt30, before θ) and the judges dropped by it.
	QC             JudgeQCResult
	ExcludedJudges []string
	JudgeNames     []string
	// Filtering: per-flag exclusion counts, size of the estimation set, judgments dropped by the family filter.
	Exclusions          map[string]int
	Triplets, Judgments int
	JudgmentsDropped    int
	StratumLabels       []string
	// Information: n_p per normalized index (edge-extra at NormEdgeExtra) and the NegDiff ≠ 0 count.
	Informative    [ged.NumParams + 1]int
	NegInformative int
	// Fits and bootstrap.
	Indep, Mixed        Result
	MixedCheck          *Result
	MixedCheckMaxDiff   float64
	MixedCheckSigmaDiff float64
	Boot                BootResult
	// Normalization and decisions.
	SHat            float64
	SHatCI          [2]float64
	ScaleIdentified bool
	Params          []ParamResult
	Extrapolated    []ExtrapolatedRow
	Hypotheses      []HypothesisResult
	D               *DResult
	// Verdict is the §3 verdict after every cap; VerdictUncapped is the same verdict before the deviation cap (every
	// data-dependent element applied: labels, H1/H2, D, the gate floor, valid primary fits, the co-primary fit), so a
	// run with non-pre-registered settings (the simulations) still shows what the data say. Missing lists the reasons.
	Verdict         stats.VerdictLabel
	VerdictUncapped stats.VerdictLabel
	Missing         []string
	LOJO            []LOJOResult
	// Stability is the share of triplets whose matching changes anywhere in the θ̂ ± 3·SD box.
	Stability float64
}

// ParamResult is one line of the decision table.
type ParamResult struct {
	Index    int
	Name     string
	Margin   bool // one of the six margin-judged parameters
	Theta0   float64
	Anchored float64
	Truth    float64 // NaN unless a simulation truth was given
	Indep    stats.ParamDecision
	Mixed    *stats.ParamDecision
	Combined stats.Label
	// WarmCI / ColdCI are the 5%/95% quantiles of the warm- and cold-started replicates (independence, normalized).
	WarmCI, ColdCI [2]float64
	// CI90NoDivergent is the 90% interval without the divergent replicates (independence), set when the divergent
	// share exceeds 2% (decisions §6).
	CI90NoDivergent *stats.Interval
}

// ExtrapolatedRow is a tied type cost reported "extrapolated from Master, not calibrated" (decisions §12b).
type ExtrapolatedRow struct{ Index, Master int }

// HypothesisResult is one confirmatory test.
type HypothesisResult struct {
	ID, Name  string
	Indep     stats.ContrastResult
	Mixed     *stats.ContrastResult
	Supported bool // under both fits when the mixed fit is present (decisions §4 note)
	PHolm     float64
	Reject    bool
	// C-family extras: discordant counts over units with Δ ≠ 0 under both rules, the exact one-sided McNemar p, and
	// the number of eligible units dropped from the exact test because a rule had Δ = 0.
	Discordant  [2]int
	ExactP      float64
	ZeroDropped int
	Eligible    int
	// Missing marks a placeholder (C4 without a baseline: p = 1); NotEvaluable marks a contrast on an unidentified scale.
	Missing      bool
	NotEvaluable bool
}

// LOJOResult is the decision table with one judge removed.
type LOJOResult struct {
	Judge  string
	Params []ParamResult
}

// decisions applies the §3 rule to every normalized index (tied indices excluded) for both fits and returns the rows,
// ŝ with its 90% interval, and whether the scale is identified (every member of U estimable in both fits).
func decisions(ind Result, boot [][]float64, meta []RepMeta, mix *Result, mboot [][]float64, mmeta []RepMeta,
	nInf [ged.NumParams + 1]int, negInf int, co ConfirmOptions, tie map[int]int) ([]ParamResult, float64, [2]float64, bool) {
	estN, sHat := NormalizeParams(ind.Params, co.U)
	bootN, sBoot := NormalizeRows(boot, co.U)
	sh := Summarise(meta)
	var estM []float64
	var bootM [][]float64
	var shM Shares
	if mix != nil {
		estM, _ = NormalizeParams(mix.Params, co.U)
		bootM, _ = NormalizeRows(mboot, co.U)
		shM = Summarise(mmeta)
	}
	th0 := Theta0Extended(co.Theta0)
	margin := map[int]bool{}
	for _, p := range MarginJudged {
		margin[p] = true
	}
	var truthN []float64
	if co.Truth != nil {
		v := append(slices.Clone(co.Truth[:]), 0)
		if co.TrueNeg != nil {
			v[ged.NumParams] = *co.TrueNeg
		}
		truthN, _ = Normalize(v, co.U)
	}
	br := BootResult{}
	var rows []ParamResult
	for i := range NormLen {
		if _, tied := tie[i]; tied {
			continue
		}
		row := ParamResult{Index: i, Name: NormName(i), Margin: margin[i], Theta0: th0[i], Truth: math.NaN()}
		switch {
		case i < ged.NumParams:
			row.Anchored = ind.Params.Theta[i]
		case i == NormEdgeExtra:
			row.Anchored = ged.EdgeExtraCost
		default:
			row.Anchored = ind.Params.ThetaNeg
		}
		if truthN != nil {
			row.Truth = truthN[i]
		}
		mk := func(est []float64, rows [][]float64, anchored [][]float64, s Shares, ms []RepMeta) stats.ParamDecision {
			di := stats.DecisionInput{Est: est[i], Boot: columnOf(rows, i), Valid: validMask(ms), Theta0: th0[i], M: co.M, TotalReps: len(rows)}
			switch {
			case i == NormNeg:
				di.NInformative = negInf
			case i <= NormEdgeExtra:
				di.NInformative = nInf[i]
			}
			di.MinInformative = co.MinInformative[i]
			if i < ged.NumParams {
				di.BoundaryShare, di.DivergentShare = s.Boundary[i], s.Divergent[i]
			}
			if i != NormEdgeExtra {
				ai := i
				if i == NormNeg {
					ai = ged.NumParams // ThetaVec layout: θ_neg follows the 11 costs
				}
				col := Column(anchored, ai)
				di.AnchoredIQR = Quantile(col, 0.75) - Quantile(col, 0.25)
				di.CheckAnchored = true
			}
			return stats.Decide(di)
		}
		row.Indep = mk(estN, bootN, boot, sh, meta)
		row.Combined = row.Indep.Label
		if mix != nil {
			d := mk(estM, bootM, mboot, shM, mmeta)
			row.Mixed = &d
			row.Combined = stats.Combine(row.Indep.Label, d.Label)
		}
		row.WarmCI, row.ColdCI = br.WarmCold(bootN, meta, i)
		if row.Indep.DivergentShare > stats.DivergentReport {
			var keep [][]float64
			for r, m := range meta {
				div := false
				for p := range ged.NumParams {
					div = div || m.Divergent[p]
				}
				if !div && r < len(bootN) {
					keep = append(keep, bootN[r])
				}
			}
			col := Column(keep, i)
			row.CI90NoDivergent = &stats.Interval{Lo: Quantile(col, 0.05), Hi: Quantile(col, 0.95)}
		}
		rows = append(rows, row)
	}
	// Scale identification (decisions §2/§3 as revised): if any member of U is not estimable, ŝ is not identified
	// and no normalized decision stands.
	identified := true
	inU := map[int]bool{}
	for _, p := range co.U {
		inU[p] = true
	}
	for _, r := range rows {
		if inU[r.Index] && r.Combined == stats.NotEstimable {
			identified = false
		}
	}
	if !identified {
		for k := range rows {
			rows[k].Combined = stats.NotEstimable
			rows[k].Indep.Label = stats.NotEstimable
			rows[k].Indep.Reason = "scale not identified: a member of U is not estimable"
			if rows[k].Mixed != nil {
				rows[k].Mixed.Label = stats.NotEstimable
				rows[k].Mixed.Reason = rows[k].Indep.Reason
			}
		}
	}
	col := make([]float64, 0, len(sBoot))
	for _, v := range sBoot {
		if !math.IsNaN(v) {
			col = append(col, v)
		}
	}
	slices.Sort(col)
	return rows, sHat, [2]float64{Quantile(col, 0.05), Quantile(col, 0.95)}, identified
}

// columnOf is column p of rows in replicate order (NaN where a replicate has no row), so a per-replicate mask aligns with it.
func columnOf(rows [][]float64, p int) []float64 {
	col := make([]float64, len(rows))
	for r, row := range rows {
		col[r] = math.NaN()
		if p < len(row) {
			col[r] = row[p]
		}
	}
	return col
}

// validMask marks, per replicate, whether its iterate is valid (decisions §11.2, §6).
func validMask(meta []RepMeta) []bool {
	mask := make([]bool, len(meta))
	for r, m := range meta {
		mask[r] = m.Valid()
	}
	return mask
}

// contrastBoot computes a contrast f on the estimate and on every normalized replicate.
func contrastBoot(p Params, boot [][]float64, U []int, f func(v []float64) float64) (float64, []float64) {
	est, _ := NormalizeParams(p, U)
	rows, _ := NormalizeRows(boot, U)
	var b []float64
	for _, r := range rows {
		if r != nil {
			b = append(b, f(r))
		}
	}
	return f(est), b
}

// hypotheses runs H1, H2, H4a, H4b and the C-family with their stated null values and Holm over the family.
func hypotheses(trips []Triplet, js []Judgment, res ConfirmResult, baselines []RuleDelta, co ConfirmOptions) ([]HypothesisResult, bool, error) {
	type spec struct {
		id, name string
		f        func(v []float64) float64
		null     float64
		side     stats.Side
	}
	specs := []spec{
		{"H1", "θ̃_type − (θ̃_edge-missing + θ̃_edge-extra) < 0", func(v []float64) float64 { return v[ged.PTypeRAMA] - (v[ged.PEdgeMissing] + v[NormEdgeExtra]) }, 0, stats.Less},
		{"H2", "θ̃_direction − θ̃_edge-missing ≥ −m (non-inferiority)", func(v []float64) float64 { return v[ged.PDirection] - v[ged.PEdgeMissing] }, -co.M, stats.Greater},
		{"H4a", "θ̃_I-missing − θ̃_I-extra ≠ 0", func(v []float64) float64 { return v[ged.PNodeMissing] - v[ged.PNodeExtra] }, 0, stats.TwoSided},
		{"H4b", "θ̃_edge-missing − θ̃_edge-extra ≠ 0", func(v []float64) float64 { return v[ged.PEdgeMissing] - v[NormEdgeExtra] }, 0, stats.TwoSided},
	}
	var out []HypothesisResult
	for _, s := range specs {
		est, b := contrastBoot(res.Indep.Params, res.Boot.Theta, co.U, s.f)
		h := HypothesisResult{ID: s.id, Name: s.name, Indep: stats.BootstrapContrast(est, b, s.null, s.side)}
		h.Supported = h.Indep.Supported
		if res.Mixed.Params.Beta != nil {
			estM, bM := contrastBoot(res.Mixed.Params, res.Boot.MixedTheta, co.U, s.f)
			m := stats.BootstrapContrast(estM, bM, s.null, s.side)
			h.Mixed = &m
			h.Supported = h.Supported && m.Supported
		}
		out = append(out, h)
	}
	// C-family: R0 against R1 (C1) and the supplied baselines (C4 = B1).
	d0, err := Delta(trips, co.Theta0)
	if err != nil {
		return nil, false, err
	}
	d1, err := Delta(trips, R1())
	if err != nil {
		return nil, false, err
	}
	rules := append([]RuleDelta{{ID: "C1", Name: "R1 (naive uniform costs)", Delta: d1}}, baselines...)
	c4 := false
	for _, b := range baselines {
		if b.ID == "C4" {
			c4 = true
		}
	}
	nonNeg := make([]bool, len(trips))
	cluster := make([]int, len(trips))
	strata := make([]string, len(trips))
	for i, t := range trips {
		nonNeg[i], cluster[i], strata[i] = t.NegDiff == 0, t.Source, t.Generator
	}
	for _, r := range rules {
		s0, elig, z0 := Score(d0, js, nonNeg)
		s1, _, z1 := Score(r.Delta, js, nonNeg)
		cr := stats.ClusteredMcNemar(s0, s1, elig, cluster, strata, co.B, co.Seed+500)
		// Exact McNemar on units where both rules have Δ ≠ 0 (decisions §4 as revised).
		right0, right1, elig2 := make([]bool, len(trips)), make([]bool, len(trips)), make([]bool, len(trips))
		dropped, n := 0, 0
		for i := range trips {
			if !elig[i] {
				continue
			}
			if z0[i] || z1[i] {
				dropped++
				continue
			}
			elig2[i] = true
			right0[i], right1[i] = s0[i] == 1, s1[i] == 1
			n++
		}
		b, c := stats.DiscordantCounts(right0, right1, elig2)
		h := HypothesisResult{ID: r.ID, Name: fmt.Sprintf("agree(R0) − agree(%s) > 0", r.Name), Indep: cr, Supported: cr.Supported,
			Discordant: [2]int{b, c}, ExactP: stats.McNemarExact(b, c, true), ZeroDropped: dropped, Eligible: n + dropped}
		out = append(out, h)
	}
	if !c4 {
		out = append(out, HypothesisResult{ID: "C4", Name: "agree(R0) − agree(B1) > 0 (baseline not supplied)", Missing: true,
			Indep: stats.ContrastResult{P: 1, Null: 0, Side: stats.Greater}})
	}
	ps := make([]float64, len(out))
	for i, h := range out {
		ps[i] = h.Indep.P
		if h.Mixed != nil {
			ps[i] = math.Max(ps[i], h.Mixed.P)
		}
		if math.IsNaN(ps[i]) {
			ps[i] = 1
		}
	}
	adj, rej := stats.Holm(ps, 0.05)
	for i := range out {
		out[i].PHolm, out[i].Reject = adj[i], rej[i]
	}
	return out, !c4, nil
}

// DBounds returns the one-sided 95% lower and upper bounds (q₀.₀₅, q₀.₉₅) of the agreement form of D (decisions §7 as revised).
func (d *DResult) DBounds() (lower, upper float64) {
	if d == nil {
		return math.NaN(), math.NaN()
	}
	col := make([]float64, 0, len(d.AgreeBoot))
	for _, v := range d.AgreeBoot {
		if !math.IsNaN(v) {
			col = append(col, v)
		}
	}
	slices.Sort(col)
	return Quantile(col, 0.05), Quantile(col, 0.95)
}

// verdict applies the §3 rule and the caps. It returns the verdict before the deviation cap (every data-dependent
// cap applied) and after it, with the reasons.
func verdict(res ConfirmResult, co ConfirmOptions) (uncapped, capped stats.VerdictLabel, missing []string) {
	in := stats.VerdictInput{DLower95: math.NaN(), DUpper95: math.NaN()}
	for _, r := range res.Params {
		if r.Margin {
			in.Labels = append(in.Labels, r.Combined)
			in.Names = append(in.Names, r.Name)
		}
	}
	for _, h := range res.Hypotheses {
		switch h.ID {
		case "H1":
			in.H1 = h.Supported
		case "H2":
			in.H2 = h.Supported
		}
	}
	in.DLower95, in.DUpper95 = res.D.DBounds()
	v, missing := stats.Verdict(in)
	block := func(reason string) {
		missing = append(missing, reason)
		if v == stats.Validated {
			v = stats.NotContradicted
		}
	}
	if res.QC.NT < co.MinGateTriplets {
		block(fmt.Sprintf("T1 gate not evaluable: n_T = %d < %d (§9.1 data requirement)", res.QC.NT, co.MinGateTriplets))
	}
	// An invalid primary iterate (decisions §11.2) leaves the estimate undetermined and blocks "validated".
	for _, f := range []struct {
		name string
		r    *Result
	}{{"independence fit", &res.Indep}, {"random-intercept fit", mixedOrNil(res)}, {"high-resolution refit", res.MixedCheck}} {
		if f.r != nil && !f.r.Valid() {
			block(fmt.Sprintf("%s is not a valid iterate (§11.2): converged %v, cycled %v (spread %.4f ≥ %g), capped %v, inner converged %v",
				f.name, f.r.Converged, f.r.Cycled, f.r.CycleSpread, CycleTol, f.r.Capped, f.r.InnerConverged))
		}
	}
	if co.SkipMixed {
		block("random-intercept fit skipped: no co-primary agreement")
	}
	uncapped = v
	if len(res.Deviations) > 0 {
		block("non-pre-registered settings (see the deviations list): the verdict is capped at \"not contradicted\"")
	}
	if !res.ScaleIdentified {
		missing = append(missing, "normalization scale not identified")
	}
	return uncapped, v, missing
}

// DefaultStrata labels every triplet for the per-stratum D: T1, T2, T4, and T3 split into "T3-near" (|Δ(θ₀)| < 0.5)
// and "T3-clear" (decisions §7). Triplets without a type are labelled by |Δ(θ₀)| alone.
func DefaultStrata(trips []Triplet, th0 ged.Theta) ([]string, error) {
	d, err := Delta(trips, th0)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(trips))
	for i, t := range trips {
		near := math.Abs(d[i]) < 0.5
		switch {
		case t.Type == "T3" || t.Type == "":
			if near {
				out[i] = "T3-near"
			} else {
				out[i] = "T3-clear"
			}
			if t.Type == "" {
				out[i] = strings.TrimPrefix(out[i], "T3-")
			}
		default:
			out[i] = t.Type
		}
	}
	return out, nil
}

// MatchingStability is the share of triplets whose matching signature changes anywhere in the box
// center ± halfWidth: it checks the 2·11 axis points and corners random corners of the box (decisions §11.6).
func MatchingStability(trips []Triplet, center ged.Theta, halfWidth [ged.NumParams]float64, corners int, seed uint64, tie map[int]int) (float64, error) {
	if len(trips) == 0 {
		return math.NaN(), nil
	}
	base, err := Signatures(trips, center)
	if err != nil {
		return 0, err
	}
	changed := make([]bool, len(trips))
	check := func(th ged.Theta) error {
		for p := range th {
			th[p] = clampTo(th[p], 0, ThetaMax)
		}
		for p, m := range tie {
			th[p] = th[m] // tied coordinates follow their master, as unpack does
		}
		s, err := Signatures(trips, th)
		if err != nil {
			return err
		}
		for i := range s {
			if s[i] != base[i] {
				changed[i] = true
			}
		}
		return nil
	}
	for p := range ged.NumParams {
		if halfWidth[p] <= 0 {
			continue
		}
		for _, dir := range []float64{-1, 1} {
			th := center
			th[p] += dir * halfWidth[p]
			if err := check(th); err != nil {
				return 0, err
			}
		}
	}
	rng := rand.New(rand.NewPCG(seed, 5))
	for range corners {
		th := center
		for p := range ged.NumParams {
			if rng.IntN(2) == 0 {
				th[p] -= halfWidth[p]
			} else {
				th[p] += halfWidth[p]
			}
		}
		if err := check(th); err != nil {
			return 0, err
		}
	}
	n := 0
	for _, c := range changed {
		if c {
			n++
		}
	}
	return float64(n) / float64(len(trips)), nil
}

// SimulatedReversal is the expected T1 reversal rate of a judge (β, τ, π) with shared offset u ~ N(0, σ²) and
// lapse rate ℓ (a uniformly random answer with probability ℓ), averaged over both orders and the given Δ values:
// (1 − ℓ)·E[P(B | β·Δ + π·o + u, τ)] + ℓ/3 (decisions §9.1 P2 check). Use the random-intercept fit's parameters.
func SimulatedReversal(beta, tau, pi, sigma, lapse float64, deltas []float64) float64 {
	if len(deltas) == 0 {
		return math.NaN()
	}
	x, w := ghRule(40)
	s := 0.0
	for _, d := range deltas {
		for _, o := range []float64{1, -1} {
			if sigma <= 0 {
				_, pb, _ := Probs(beta*d+pi*o, tau)
				s += pb / 2
				continue
			}
			for q := range x {
				_, pb, _ := Probs(beta*d+pi*o+sigma*math.Sqrt2*x[q], tau)
				s += w[q] / math.Sqrt(math.Pi) * pb / 2
			}
		}
	}
	return (1-lapse)*s/float64(len(deltas)) + lapse/3
}
