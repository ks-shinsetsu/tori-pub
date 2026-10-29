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
// §11, §12b; review-fit-impl B1). It runs, in this order: the mechanical judge QC on the unfiltered data
// (before any θ is fitted), the triplet/judgment filters, the informative counts, the multi-start working-independence
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

	// 1. Judge QC before θ (decisions §9, §9.1, §13 item 5).
	res.QC = JudgeQCStats(trips, js, co.JudgeNames, QCOptions{GateDelta: co.GateDelta, RMax: co.RMax, Conf: co.GateConf})
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
			// Quadrature check: refit from the estimate with more nodes and report the largest change (review-fit-impl M3).
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
	res.Hypotheses, err = hypotheses(trips, js, res, baselines, co)
	if err != nil {
		return res, err
	}

	// 8. D criterion (decisions §7).
	if !co.SkipD {
		d, err := NestedD(trips, js, opts, co.Theta0, labels, DOptions{Reps: co.DReps, Seed: co.Seed + 200, K: 5, Repeats: co.DRepeats, Workers: co.Workers})
		if err != nil {
			return res, fmt.Errorf("confirmatory: nested D: %w", err)
		}
		res.D = &d
	}

	// 9. Verdict (decisions §3).
	res.Verdict, res.Missing = verdict(res, co)

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
	res.Stability, err = MatchingStability(trips, res.Indep.Params.Theta, half, 64, co.Seed+400)
	if err != nil {
		return res, err
	}
	return res, nil
}

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
	// SkipMixed / SkipD / SkipLOJO omit parts for quick runs; the verdict then cannot be "validated".
	SkipMixed, SkipD, SkipLOJO bool
	// Untied keeps the three type costs free (diagnostic fit of §12b); the primary analysis is tied.
	Untied  bool
	Workers int
	// HighNodes and HighNodesSigma control the quadrature check: when σ̂ > HighNodesSigma (0 → 2) the mixed fit is
	// refitted with HighNodes nodes (0 → 80) and the change is reported.
	HighNodes      int
	HighNodesSigma float64
	// Gate parameters of §9.1 (0 → 2.0, 0.20, 0.90).
	GateDelta, RMax, GateConf float64
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
	return co
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
	// Judge QC (on the unfiltered data, before θ) and the judges dropped by it.
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
	Verdict         stats.VerdictLabel
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
		mk := func(est []float64, rows [][]float64, anchored [][]float64, s Shares, total int) stats.ParamDecision {
			di := stats.DecisionInput{Est: est[i], Boot: Column(rows, i), Theta0: th0[i], M: co.M, TotalReps: total}
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
		row.Indep = mk(estN, bootN, boot, sh, len(boot))
		row.Combined = row.Indep.Label
		if mix != nil {
			d := mk(estM, bootM, mboot, shM, len(mboot))
			row.Mixed = &d
			row.Combined = stats.Combine(row.Indep.Label, d.Label)
		}
		row.WarmCI, row.ColdCI = br.WarmCold(bootN, meta, i)
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
func hypotheses(trips []Triplet, js []Judgment, res ConfirmResult, baselines []RuleDelta, co ConfirmOptions) ([]HypothesisResult, error) {
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
		return nil, err
	}
	d1, err := Delta(trips, R1())
	if err != nil {
		return nil, err
	}
	rules := append([]RuleDelta{{ID: "C1", Name: "R1 (naive uniform costs)", Delta: d1}}, baselines...)
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
	return out, nil
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

func verdict(res ConfirmResult, co ConfirmOptions) (stats.VerdictLabel, []string) {
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
	if co.SkipMixed {
		missing = append(missing, "random-intercept fit skipped: no co-primary agreement")
		if v == stats.Validated {
			v = stats.NotContradicted
		}
	}
	if !res.ScaleIdentified {
		missing = append(missing, "normalization scale not identified")
	}
	return v, missing
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
func MatchingStability(trips []Triplet, center ged.Theta, halfWidth [ged.NumParams]float64, corners int, seed uint64) (float64, error) {
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
