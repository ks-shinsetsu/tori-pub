package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/stats"
)

// TestConfirmatoryEndToEnd runs the single entry point on a small synthetic dataset and checks every section of the
// report and the verdict label.
func TestConfirmatoryEndToEnd(t *testing.T) {
	trips, js := fit.Synthetic(31, fit.SyntheticOptions{Sources: 60, Sigma: 0.8, Lapse: 0.05, WithNeg: true, Neg: 1})
	// One triplet per excluded flag, to be counted.
	for i, f := range fit.DefaultExclude {
		trips[i].Flags = []string{f}
	}
	opts := fit.Options{Judges: 3, Theta0: ged.Current(), MaxOuter: 20}
	co := fit.ConfirmOptions{B: 24, DReps: 3, DRepeats: 2, LOJOReps: 12, Seed: 1, MinInformative: map[int]int{}, JudgeNames: []string{"a", "b", "c"}}
	res, err := fit.Confirmatory(trips, js, opts, co)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fit.DefaultExclude {
		if res.Exclusions[f] != 1 {
			t.Errorf("flag %s excluded %d, want 1", f, res.Exclusions[f])
		}
	}
	if res.Triplets != len(trips)-len(fit.DefaultExclude) {
		t.Errorf("estimation set %d", res.Triplets)
	}
	if len(res.Params) != fit.NormLen-2 || len(res.Hypotheses) != 5 || len(res.LOJO) != 3 || res.D == nil {
		t.Fatalf("shape: params %d hyps %d lojo %d D %v", len(res.Params), len(res.Hypotheses), len(res.LOJO), res.D != nil)
	}
	if res.Mixed.Params.Sigma <= 0 {
		t.Errorf("σ̂ = %v", res.Mixed.Params.Sigma)
	}
	var b bytes.Buffer
	WriteConfirmatory(&b, res, provenance{Lambda: 0, M: 0.25, B: 24, Seeds: map[string]uint64{"seed": 1}})
	out := b.String()
	for _, h := range []string{"## 判定者の品質管理", "## 推定集合", "## 情報量", "## 当てはめ", "## パラメータの判断", "固定尺度", "## 確認的仮説", "## 現行表の判定", "## 判定者を 1 人ずつ除いた判断", "## 来歴", "外挿、較正なし", "**判定: "} {
		if !strings.Contains(out, h) {
			t.Errorf("report lacks %q", h)
		}
	}
	if !strings.Contains(out, res.Verdict.JA()) {
		t.Errorf("verdict label %q not printed", res.Verdict.JA())
	}
	t.Logf("verdict %s; missing %v; scale identified %v", res.Verdict, res.Missing, res.ScaleIdentified)
}

// TestFrozenColumnNotEstimable: with every relabel removed (type Δφ columns identically zero) and the tie off, the
// type costs come out "not estimable" and, being members of U, make every normalized decision "not estimable".
func TestFrozenColumnNotEstimable(t *testing.T) {
	trips, js := fit.Synthetic(32, fit.SyntheticOptions{Sources: 40})
	for i := range trips {
		for _, g := range []*ged.Graph{&trips[i].A, &trips[i].B, &trips[i].Ref} {
			for j := range g.Edges {
				g.Edges[j].Kind = ged.RA
			}
		}
	}
	n, _ := fit.Informative(trips, ged.Current(), nil)
	if n[ged.PTypeRACA]+n[ged.PTypeRAMA]+n[ged.PTypeCAMA] != 0 {
		t.Fatalf("type columns not zero: %v", n)
	}
	opts := fit.Options{Judges: 3, Theta0: ged.Current(), MaxOuter: 20}
	co := fit.ConfirmOptions{B: 16, Seed: 2, MinInformative: map[int]int{}, SkipD: true, SkipLOJO: true, SkipMixed: true, Untied: true, RandomStarts: -1}
	res, err := fit.Confirmatory(trips, js, opts, co)
	if err != nil {
		t.Fatal(err)
	}
	if res.ScaleIdentified {
		t.Fatalf("scale should not be identified")
	}
	for _, p := range res.Params {
		if p.Combined != stats.NotEstimable {
			t.Errorf("%s labelled %v", p.Name, p.Combined)
		}
	}
	if res.Verdict == stats.Validated {
		t.Fatalf("verdict %v", res.Verdict)
	}
	var b bytes.Buffer
	WriteConfirmatory(&b, res, provenance{})
	if !strings.Contains(b.String(), "正規化の尺度が同定できない") || strings.Contains(b.String(), "| 維持") {
		t.Fatalf("report:\n%s", b.String())
	}
}

// TestP2Report: the P2 mode prints no θ estimate and runs end to end.
func TestP2Report(t *testing.T) {
	trips, js := fit.Synthetic(33, fit.SyntheticOptions{Sources: 30, Sigma: 0.5})
	opts := fit.Options{Judges: 3, Theta0: ged.Current(), FixTheta: true, FitNeg: true, MaxOuter: 5, IncludeAll: true}
	aux, err := fit.Fit(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	mix, err := fit.FitMixed(trips, js, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := writeP2Report(&b, p2Input{Names: []string{"a", "b", "c"}, Trips: trips, Js: js, Aux: aux.Params, Mixed: &mix.Params, Tie: fit.TypeTie(), MinInf: fit.DefaultMinInformative()}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, h := range []string{"P2 報告", "φ の情報量", "合格確率", "級内相関", "対応付けの安定性"} {
		if !strings.Contains(out, h) {
			t.Errorf("P2 report lacks %q", h)
		}
	}
	if strings.Contains(out, "## パラメータの判断") {
		t.Fatalf("P2 report must not contain a θ decision table")
	}
}
