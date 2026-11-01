package fit_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/simulate"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/stats"
)

// TestReferenceJudgesPassQC: the reference judge configuration of the P1 simulator (σ = 1, 10% random answers)
// passes the mechanical judge QC (gate + tie share; α and order consistency are descriptive) at the design size,
// and Confirmatory with the default QC settings does not abort on a small data set.
func TestReferenceJudgesPassQC(t *testing.T) {
	// The gate is evaluated at the design size, where it is evaluable (n_T ≥ 100, §9.1). At 150 sources n_T ≈ 20
	// and the one-sided Clopper–Pearson bound exceeds r_max for a passing judge in about a quarter of the seeds
	// (3 of 12, before and after the cosine tie-break of ged.Compute; that tie-break changes Δ(θ₀) wherever
	// substitution costs tie below cos 0.3 and so the simulator's draws: seed 7's n_T went from 21 to 19).
	names := []string{"ref 1", "ref 2", "ref 3"}
	cfg := simulate.Default()
	cfg.Sources, cfg.Seed = 750, 7
	trips, js, _, err := simulate.Generate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	qc := fit.JudgeQCStats(trips, js, names, fit.QCOptions{})
	for j, ex := range qc.Excluded {
		if ex {
			t.Errorf("reference judge %d excluded: %s (gate r = %.3f upper %.3f, tie %.3f, α %v)", j, qc.Reasons[j], qc.Gates[j].R, qc.Gates[j].Upper, qc.TieShare[j], qc.Alpha[j])
		}
	}
	t.Logf("n_T = %d, pairwise α = %v", qc.NT, qc.Alpha)
	// Confirmatory on a small data set (QC recorded, not applied, as the A.5 batch runs it): only the size knobs are
	// reduced; the gate is below its floor there and must be reported as not evaluable rather than abort.
	cfg.Sources = 150
	trips, js, _, err = simulate.Generate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := fit.Confirmatory(trips, js, fit.Options{Judges: 3, MaxOuter: 20}, fit.ConfirmOptions{B: 8, DReps: 2, DRepeats: 1, SkipLOJO: true, SkipMixed: true, SkipExclusions: true, JudgeNames: names, Seed: 1})
	if err != nil {
		t.Fatalf("Confirmatory aborted: %v", err)
	}
	if res.QC.NT >= 100 || !slices.ContainsFunc(res.Missing, func(s string) bool { return strings.HasPrefix(s, "T1 gate not evaluable") }) {
		t.Fatalf("n_T = %d: the gate floor must be reported: %v", res.QC.NT, res.Missing)
	}
	if res.Verdict == stats.Validated || len(res.Deviations) == 0 {
		t.Fatalf("reduced settings must be listed as deviations and cap the verdict: %v / %v", res.Verdict, res.Deviations)
	}
}
