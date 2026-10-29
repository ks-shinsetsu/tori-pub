package main

import (
	"encoding/json"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/simulate"
)

// TestSimJSONLRecord runs the per-seed analysis on a tiny data set and checks the JSONL line: the prefix the resume
// check of scripts/a5_batch.sh greps for, NaN written as null, and the fields scripts/a5_summarise.py reads.
func TestSimJSONLRecord(t *testing.T) {
	defer func(d, b int) { *simDReps, *simDiagBoot = d, b }(*simDReps, *simDiagBoot)
	*simDReps, *simDiagBoot = 2, 2
	w, err := simulate.WorldByName(simulate.WorldEdgeN)
	if err != nil {
		t.Fatal(err)
	}
	cfg := simulate.Default().WithWorld(w)
	cfg.Sources, cfg.Seed = 16, 7
	trips, js, metas, err := simulate.Generate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cnt, err := simulate.Count(trips, metas, ged.Current())
	if err != nil {
		t.Fatal(err)
	}
	o := simOptions{sources: cfg.Sources, boot: 4, seed: cfg.Seed, m: 0.25, sigma: 1}
	rec, _, err := fitSeed(io.Discard, o, cfg, trips, js, metas, cnt, false)
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(line), `{"world":"edgeN","seed":7,`) {
		t.Fatalf("record prefix %.60s", line)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"params", "verdict", "verdict_missing", "d", "hypotheses", "fits", "boot_shares", "informative",
		"min_informative", "neg_informative", "diag", "secs", "s_true", "s_hat_indep", "sigma_hat", "true_norm", "exclusions",
		"excluded_judges", "qc_fail", "stability", "scale_identified", "s_hat_ci90"} {
		if _, ok := m[k]; !ok {
			t.Errorf("record lacks %q", k)
		}
	}
	params := m["params"].([]any)
	margin := 0
	for _, p := range params {
		pm := p.(map[string]any)
		if pm["margin"].(bool) {
			margin++
		}
		if pm["mixed"] == nil {
			t.Errorf("%v: no random-intercept decision", pm["name"])
		}
		for _, k := range []string{"est", "sd", "lo90", "hi90", "lo95", "hi95", "upper95", "one_sided", "label", "reason"} {
			if _, ok := pm["indep"].(map[string]any)[k]; !ok {
				t.Errorf("%v: indep lacks %q", pm["name"], k)
			}
		}
	}
	if margin != 6 || len(params) != 11 { // 13 normalized entries minus the two tied CA pairs
		t.Fatalf("%d params, %d margin-judged", len(params), margin)
	}
	if rec.Verdict == "" || rec.D == nil || rec.D.Reps != 2 || rec.Diag == nil || len(rec.Diag.Pairs) != 2 {
		t.Fatalf("verdict %q, D %+v, diag %+v", rec.Verdict, rec.D, rec.Diag)
	}
	if !(float64(rec.D.Lower) <= float64(rec.D.Upper)) {
		t.Fatalf("D bounds %v > %v", rec.D.Lower, rec.D.Upper)
	}
	// The floors are fit.DefaultMinInformative scaled to 64 triplets (650 → 14, 450 → 10, θ_neg 300 → 7).
	if rec.MinInformative["I 欠落"] != 14 || rec.MinInformative["種別 RA↔MA"] != 10 || rec.MinInformative["θ_neg（極性反転）"] != 7 {
		t.Fatalf("scaled floors %v", rec.MinInformative)
	}
	// Without -apply-qc the QC is recorded but no judge is dropped (at 16 sources every judge fails the T1 gate).
	if len(rec.ExcludedJudges) != 0 || len(rec.QCFail) == 0 {
		t.Fatalf("excluded %v, QC failures %v", rec.ExcludedJudges, rec.QCFail)
	}
	if len(rec.Hypotheses) == 0 || rec.Hypotheses[0].ID == "" {
		t.Fatalf("hypotheses %+v", rec.Hypotheses)
	}
	if got := float64(rec.STrue); math.Abs(got-4.0/3) > 1e-9 {
		t.Fatalf("ŝ_true %v", got)
	}
	if rec.FitTriplets != cnt.FitSet || rec.Triplets != len(trips) {
		t.Fatalf("triplets %d / %d", rec.FitTriplets, rec.Triplets)
	}
	b, _ := json.Marshal(struct{ X, Y jf }{jf(math.NaN()), jf(math.Inf(1))})
	if string(b) != `{"X":null,"Y":null}` {
		t.Fatalf("NaN marshalled as %s", b)
	}
}

func TestSimWorldName(t *testing.T) {
	defer func(w string) { *simWorld = w }(*simWorld)
	for _, c := range []struct {
		world string
		null  bool
		want  string
		err   bool
	}{{"", false, "alt", false}, {"", true, "null", false}, {"edgeN", false, "edgeN", false}, {"null", true, "null", false}, {"mild", true, "", true}} {
		*simWorld = c.world
		got, err := simWorldName(c.null)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("-world %q -null %v: %q, %v", c.world, c.null, got, err)
		}
	}
}
