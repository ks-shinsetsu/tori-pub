package fit

import (
	"strings"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
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
	d := Preregistered(co, o)
	joined := strings.Join(d, "\n")
	for _, want := range []string{"B = 24", "leave-one-judge-out skipped", "minimum informative", "type tie off", "weighted", "γ", "family-coupling", "λ = 0.5", "lapse mixture off"} {
		if !strings.Contains(joined, want) {
			t.Errorf("deviation %q missing in %v", want, d)
		}
	}
}
