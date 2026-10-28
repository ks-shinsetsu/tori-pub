// Package stats holds the decision rule, the confirmatory tests and the judge QC statistics of
// decisions-stats.md §3, §4, §7, §9 and §9.1. It depends on nothing in the fit package: inputs are plain numbers.
package stats

import (
	"math"
	"slices"
)

// Label is the per-parameter decision of decisions §3.
type Label int

const (
	NotEstimable Label = iota
	Keep
	Change
	UndecidedDifferent
	UndecidedImprecise
	UndecidedModelSensitive
)

func (l Label) String() string {
	return [...]string{"not estimable", "keep", "change", "undecided (different, size unclear)", "undecided (imprecise)", "undecided (model-sensitive)"}[l]
}

// JA is the Japanese report label.
func (l Label) JA() string {
	return [...]string{"推定不能", "維持", "変更", "未決（異なるが大きさ不明）", "未決（精度不足）", "未決（モデル依存）"}[l]
}

// Undecided reports whether l is one of the undecided labels.
func (l Label) Undecided() bool { return l >= UndecidedDifferent }

// Interval is a two-sided interval.
type Interval struct{ Lo, Hi float64 }

// Width is Hi − Lo.
func (i Interval) Width() float64 { return i.Hi - i.Lo }

// DecisionInput is what the rule needs for one parameter (all on the normalized scale).
type DecisionInput struct {
	Est       float64
	Boot      []float64 // bootstrap replicates (any order; NaN entries are dropped)
	Theta0, M float64
	// NInformative is n_p (triplets with Δφ_p ≠ 0 at θ₀); MinInformative the §15 floor (0 disables the floor).
	NInformative, MinInformative int
	// BoundaryShare is the share of replicates with the anchored θ_p < 10⁻³; DivergentShare the share at the upper bound.
	BoundaryShare, DivergentShare float64
	// AnchoredIQR is the IQR of the parameter's own anchored replicates (before the sum normalization). The
	// normalization spreads the other costs' variation onto a frozen coordinate, so the information floor must be
	// checked on the anchored column too. It is used only when CheckAnchored is true.
	AnchoredIQR   float64
	CheckAnchored bool
}

// ParamDecision is the result of Decide.
type ParamDecision struct {
	Label  Label
	Est    float64
	Theta0 float64
	M      float64
	CI90   Interval
	CI95   Interval
	// Upper90 is the one-sided 90% upper bound (q₀.₉₀); it replaces the interval when OneSided.
	Upper90  float64
	OneSided bool
	// SD = (q₀.₈₄₁₃ − q₀.₁₅₈₇)/2; Bias = mean(replicates) − Est; IQR = q₀.₇₅ − q₀.₂₅.
	SD, Bias, IQR  float64
	BiasFlag       bool // |Bias| > 0.5·SD
	N, MinN        int
	BoundaryShare  float64
	DivergentShare float64
	Reps           int
	Reason         string
}

// Thresholds of decisions §3 and §6.
const (
	IQRFloor           = 1e-6
	BoundaryOneSided   = 0.10
	BoundaryNotEstim   = 0.50
	DivergentReport    = 0.02
	BiasFlagMultiple   = 0.5
	zeroWidthTolerance = 1e-9
)

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

func cleanSorted(v []float64) []float64 {
	out := make([]float64, 0, len(v))
	for _, x := range v {
		if !math.IsNaN(x) && !math.IsInf(x, 0) {
			out = append(out, x)
		}
	}
	slices.Sort(out)
	return out
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// Summary computes the interval statistics without a label.
func Summary(in DecisionInput) ParamDecision {
	col := cleanSorted(in.Boot)
	d := ParamDecision{Est: in.Est, Theta0: in.Theta0, M: in.M, N: in.NInformative, MinN: in.MinInformative,
		BoundaryShare: in.BoundaryShare, DivergentShare: in.DivergentShare, Reps: len(col)}
	d.CI90 = Interval{Quantile(col, 0.05), Quantile(col, 0.95)}
	d.CI95 = Interval{Quantile(col, 0.025), Quantile(col, 0.975)}
	d.Upper90 = Quantile(col, 0.90)
	d.SD = (Quantile(col, 0.8413) - Quantile(col, 0.1587)) / 2
	d.IQR = Quantile(col, 0.75) - Quantile(col, 0.25)
	d.Bias = mean(col) - in.Est
	d.BiasFlag = math.Abs(d.Bias) > BiasFlagMultiple*d.SD
	return d
}

// Decide applies the rule of decisions §3 to one parameter on the normalized scale:
// not estimable (information floor: n_p below the minimum, IQR < 10⁻⁶, boundary share > 50%, or no replicates),
// keep (TOST: 90% CI inside [θ₀ − m, θ₀ + m]), change (90% CI entirely outside), otherwise undecided
// (different if the 95% CI excludes θ₀, imprecise otherwise). With boundary share in (10%, 50%] the one-sided
// 90% upper bound replaces the interval and only "change" (bound ≤ θ₀ − m) and "undecided (imprecise)" are available.
// A zero-width interval can never be "keep": it fails the IQR floor.
func Decide(in DecisionInput) ParamDecision {
	d := Summary(in)
	switch {
	case d.Reps == 0:
		d.Label, d.Reason = NotEstimable, "no bootstrap replicates"
		return d
	case in.MinInformative > 0 && in.NInformative < in.MinInformative:
		d.Label, d.Reason = NotEstimable, "informative triplets below the minimum"
		return d
	case math.IsNaN(d.IQR) || d.IQR < IQRFloor:
		d.Label, d.Reason = NotEstimable, "bootstrap IQR below 1e-6 (no information)"
		return d
	case in.CheckAnchored && (math.IsNaN(in.AnchoredIQR) || in.AnchoredIQR < IQRFloor):
		d.Label, d.Reason = NotEstimable, "anchored replicates do not move (zero Δφ column or frozen coordinate)"
		return d
	case in.BoundaryShare > BoundaryNotEstim:
		d.Label, d.Reason = NotEstimable, "boundary share above 50%"
		return d
	case d.CI90.Width() < zeroWidthTolerance:
		d.Label, d.Reason = NotEstimable, "zero-width interval"
		return d
	}
	if in.BoundaryShare > BoundaryOneSided {
		d.OneSided = true
		if d.Upper90 <= in.Theta0-in.M {
			d.Label, d.Reason = Change, "one-sided 90% bound below θ₀ − m"
		} else {
			d.Label, d.Reason = UndecidedImprecise, "boundary parameter: one-sided bound only"
		}
		return d
	}
	lo, hi := in.Theta0-in.M, in.Theta0+in.M
	switch {
	case d.CI90.Lo >= lo && d.CI90.Hi <= hi:
		d.Label, d.Reason = Keep, "90% CI inside the band (TOST)"
	case d.CI90.Lo >= hi || d.CI90.Hi <= lo:
		d.Label, d.Reason = Change, "90% CI outside the band (minimal-effects test)"
	case in.Theta0 < d.CI95.Lo || in.Theta0 > d.CI95.Hi:
		d.Label, d.Reason = UndecidedDifferent, "95% CI excludes θ₀ but the 90% CI is not outside the band"
	default:
		d.Label, d.Reason = UndecidedImprecise, "95% CI contains θ₀"
	}
	return d
}

// Combine merges the labels of the two co-primary fits (decisions §3): "not estimable" in either overrides,
// equal labels stand, and anything else is "undecided (model-sensitive)".
func Combine(a, b Label) Label {
	switch {
	case a == NotEstimable || b == NotEstimable:
		return NotEstimable
	case a == b:
		return a
	default:
		return UndecidedModelSensitive
	}
}

// VerdictLabel is the verdict on the current table (decisions §3).
type VerdictLabel int

const (
	Validated VerdictLabel = iota
	NotContradicted
	Contradicted
)

func (v VerdictLabel) String() string {
	return [...]string{"validated", "not contradicted", "contradicted"}[v]
}

// JA is the Japanese report label.
func (v VerdictLabel) JA() string {
	return [...]string{"妥当と確認", "否定されず", "否定"}[v]
}

// VerdictInput gathers the elements of the verdict.
type VerdictInput struct {
	// Labels are the combined labels of the six margin-judged parameters; Names their display names.
	Labels []Label
	Names  []string
	// H1, H2 supported at unadjusted α = 0.05.
	H1, H2 bool
	// DLower95, DUpper95 are the bounds of the D criterion in percentage points (NaN when D was not computed).
	DLower95, DUpper95 float64
	// DThreshold is the bar in points (0 → 2).
	DThreshold float64
}

// Verdict applies the intersection–union rule of decisions §3 and lists the missing elements for "not contradicted".
func Verdict(in VerdictInput) (VerdictLabel, []string) {
	thr := in.DThreshold
	if thr == 0 {
		thr = 2
	}
	var missing []string
	allKeep := true
	anyChange := false
	for i, l := range in.Labels {
		name := ""
		if i < len(in.Names) {
			name = in.Names[i]
		}
		switch l {
		case Keep:
		case Change:
			anyChange = true
			allKeep = false
			missing = append(missing, name+": change")
		default:
			allKeep = false
			missing = append(missing, name+": "+l.String())
		}
	}
	if !in.H1 {
		missing = append(missing, "H1 not supported")
	}
	if !in.H2 {
		missing = append(missing, "H2 not supported")
	}
	dOK := !math.IsNaN(in.DUpper95) && in.DUpper95 < thr
	if !dOK {
		if math.IsNaN(in.DUpper95) {
			missing = append(missing, "D not computed")
		} else {
			missing = append(missing, "upper 95% bound of D not below the bar")
		}
	}
	dContra := !math.IsNaN(in.DLower95) && in.DLower95 >= thr
	switch {
	case anyChange || dContra:
		return Contradicted, missing
	case allKeep && in.H1 && in.H2 && dOK && len(in.Labels) > 0:
		return Validated, nil
	default:
		return NotContradicted, missing
	}
}
