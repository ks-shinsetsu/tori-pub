package fit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/stats"
)

// TestPreregistered: the default options produce no deviation; every knob the §12c sensitivity fits use is listed.
func TestPreregistered(t *testing.T) {
	opts := Options{Judges: 3, Theta0: ged.Current()}
	if d := Preregistered(ConfirmOptions{}, opts); len(d) != 0 {
		t.Fatalf("defaults deviate: %v", d)
	}
	co := ConfirmOptions{B: 24, SkipLOJO: true, MinInformative: map[int]int{}, Untied: true}
	o := opts
	o.Weighted, o.Gamma, o.ExcludeSameFamily, o.Lambda, o.NoLapse = true, GammaFree, true, 0.5, true
	o.MaxOuter, o.Tie, o.Quadrature = 30, map[int]int{ged.PTypeRACA: ged.PTypeRAMA}, 40
	d := Preregistered(co, o)
	joined := strings.Join(d, "\n")
	for _, want := range []string{"B = 24", "leave-one-judge-out skipped", "minimum informative", "type tie off", "weighted", "γ", "family-coupling", "λ = 0.5", "lapse mixture off", "MaxOuter = 30", "custom cost tie", "grid resolution = 40"} {
		if !strings.Contains(joined, want) {
			t.Errorf("deviation %q missing in %v", want, d)
		}
	}
}

// TestVerdictCap: the deviation cap lowers only the capped verdict; an invalid primary iterate (§11.2) blocks
// "validated" in both.
func TestVerdictCap(t *testing.T) {
	base := ConfirmResult{QC: JudgeQCResult{NT: 500}, D: &DResult{AgreeBoot: []float64{-0.5, 0, 0.5}},
		Indep: Result{Converged: true, InnerConverged: true}, Mixed: Result{Converged: true, InnerConverged: true, Params: Params{Beta: []float64{1}}},
		Hypotheses: []HypothesisResult{{ID: "H1", Supported: true}, {ID: "H2", Supported: true}}, ScaleIdentified: true}
	for i := range 6 {
		base.Params = append(base.Params, ParamResult{Margin: true, Combined: stats.Keep, Name: fmt.Sprint(i)})
	}
	co := ConfirmOptions{}.withDefaults(Options{})
	if u, c, m := verdict(base, co); u != stats.Validated || c != stats.Validated {
		t.Fatalf("clean result: uncapped %v capped %v (%v)", u, c, m)
	}
	dev := base
	dev.Deviations = []string{"B = 24"}
	if u, c, _ := verdict(dev, co); u != stats.Validated || c != stats.NotContradicted {
		t.Errorf("deviation: uncapped %v capped %v, want validated / not contradicted", u, c)
	}
	inv := base
	inv.Mixed.Converged, inv.Mixed.Capped = false, true
	if u, c, _ := verdict(inv, co); u != stats.NotContradicted || c != stats.NotContradicted {
		t.Errorf("invalid mixed fit: uncapped %v capped %v, want not contradicted for both", u, c)
	}
}
