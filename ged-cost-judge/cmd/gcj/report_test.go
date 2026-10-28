package main

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/stats"
)

// TestDecisionReportZeroColumn: a parameter whose replicates never move (a zero Δφ column) is reported
// "not estimable" and never "keep", end to end through the normalized table.
func TestDecisionReportZeroColumn(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	est := fit.Init(fit.Options{Judges: 1, Theta0: ged.Current()})
	var boot [][]float64
	for range 400 {
		v := fit.ThetaVec(est)
		for p := range ged.NumParams {
			if p == ged.PDirection {
				continue // frozen: identically θ₀ in every replicate
			}
			v[p] = max(0, v[p]+0.03*rng.NormFloat64())
		}
		boot = append(boot, v)
	}
	in := decisionInput{Theta0: ged.Current(), M: 0.25, Indep: fitSummary{Res: fit.Result{Params: est}, Boot: boot}, Tie: fit.TypeTie()}
	rows, _, _ := decide(in)
	var dir, miss paramRow
	for _, r := range rows {
		switch r.Index {
		case ged.PDirection:
			dir = r
		case ged.PNodeMissing:
			miss = r
		}
	}
	if dir.Combined != stats.NotEstimable {
		t.Fatalf("frozen direction labelled %v", dir.Combined)
	}
	if miss.Combined != stats.Keep {
		t.Fatalf("I-missing with tight replicates around θ₀ labelled %v (CI90 %+v)", miss.Combined, miss.Indep.CI90)
	}
	var b bytes.Buffer
	decisionReport(&b, in)
	out := b.String()
	if !strings.Contains(out, "推定不能") || !strings.Contains(out, "外挿、較正なし") {
		t.Fatalf("report missing labels:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "| "+ged.ParamNames[ged.PDirection]) && strings.Contains(line, "維持") {
			t.Fatalf("frozen parameter labelled keep: %s", line)
		}
	}
}

// TestVerdictGlue: all keep + H1 + H2 + D gives "validated"; a frozen column prevents it.
func TestVerdictGlue(t *testing.T) {
	rows := []paramRow{}
	for _, p := range fit.MarginJudged {
		rows = append(rows, paramRow{Index: p, Name: fit.NormName(p), Margin: true, Combined: stats.Keep})
	}
	hyps := []hypothesisResult{{ID: "H1", Supported: true}, {ID: "H2", Supported: true}}
	d := &fit.DResult{Agree: 0.5, AgreeBoot: []float64{-1, 0, 0.5, 1, 1.5}}
	var b bytes.Buffer
	if v := verdictReport(&b, rows, hyps, d); v != stats.Validated {
		t.Fatalf("verdict %v:\n%s", v, b.String())
	}
	rows[0].Combined = stats.NotEstimable
	if v := verdictReport(&b, rows, hyps, d); v != stats.NotContradicted {
		t.Fatalf("verdict %v", v)
	}
}
