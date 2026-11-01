package fit_test

import (
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/simulate"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/stats"
)

// TestReferenceJudgesPassQC: the reference judge configuration of the P1 simulator (σ = 1, 10% random answers)
// passes the mechanical judge QC (gate + tie share; α and order consistency are descriptive), and Confirmatory with
// the default QC settings does not abort on it.
func TestReferenceJudgesPassQC(t *testing.T) {
	cfg := simulate.Default()
	cfg.Sources, cfg.Seed = 150, 7
	trips, js, _, err := simulate.Generate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"ref 1", "ref 2", "ref 3"}
	qc := fit.JudgeQCStats(trips, js, names, fit.QCOptions{})
	for j, ex := range qc.Excluded {
		if ex {
			t.Errorf("reference judge %d excluded: %s (gate r = %.3f upper %.3f, tie %.3f, α %v)", j, qc.Reasons[j], qc.Gates[j].R, qc.Gates[j].Upper, qc.TieShare[j], qc.Alpha[j])
		}
	}
	t.Logf("n_T = %d, pairwise α = %v", qc.NT, qc.Alpha)
	// Only the size knobs are reduced so the test stays short; the QC path itself runs with the defaults.
	res, err := fit.Confirmatory(trips, js, fit.Options{Judges: 3, MaxOuter: 20}, fit.ConfirmOptions{B: 8, DReps: 2, DRepeats: 1, SkipLOJO: true, SkipMixed: true, JudgeNames: names, Seed: 1})
	if err != nil {
		t.Fatalf("Confirmatory aborted: %v", err)
	}
	if len(res.ExcludedJudges) != 0 {
		t.Fatalf("excluded %v", res.ExcludedJudges)
	}
	if res.Verdict == stats.Validated || len(res.Deviations) == 0 {
		t.Fatalf("reduced settings must be listed as deviations and cap the verdict: %v / %v", res.Verdict, res.Deviations)
	}
}
