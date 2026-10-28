package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/stats"
)

// ---------------------------------------------------------------------------------------------------------------
// Decision table (decisions-stats §2, §3, §6, §16 #5, #19)
// ---------------------------------------------------------------------------------------------------------------

// fitSummary is one co-primary fit with its bootstrap replicates (anchored scale).
type fitSummary struct {
	Name string
	Res  fit.Result
	Boot [][]float64 // [rep] → ThetaVec (θ_0..θ_10, θ_neg)
	Meta []fit.RepMeta
}

// decisionInput gathers what the §3 rule needs.
type decisionInput struct {
	Theta0 ged.Theta
	M      float64
	U      []int // nil → fit.DefaultU
	Indep  fitSummary
	Mixed  *fitSummary // nil → single-fit labels (reported as such)
	// NInf[p] is the number of triplets with Δφ_p ≠ 0 at θ₀ (index fit.NormEdgeExtra = edge-extra); nil → no floor.
	NInf *[ged.NumParams + 1]int
	// MinInf maps a normalized index to its §15.3 minimum (nil → no count floor; defaultMinInformative() for the final analysis).
	MinInf  map[int]int
	Truth   *ged.Theta
	TrueNeg *float64
	Tie     map[int]int
	// DiagCA holds the diagnostic free-fit estimates (normalized) of the CA pairs with CIs, if the sensitivity fit was run.
	DiagCA *diagCA
}

// diagCA is the diagnostic free fit of §12b (descriptive).
type diagCA struct {
	RACA, CAMA     stats.ParamDecision
	ContrastRACA   stats.ContrastResult // θ_RA↔CA − θ_type
	ContrastCAMA   stats.ContrastResult
	FeasibleRACA   int
	FeasibleCAMA   int
	CountsOnlyRACA bool
	CountsOnlyCAMA bool
}

// defaultMinInformative are the §15.3 minimum informative counts at 3,000 triplets (normalized indices).
func defaultMinInformative() map[int]int {
	return map[int]int{
		ged.PNodeMissing: 650, ged.PNodeExtra: 650, ged.PEdgeMissing: 650, fit.NormEdgeExtra: 650,
		ged.PTypeRAMA: 450, ged.PDirection: 650, fit.NormNeg: 300,
	}
}

// paramRow is one line of the decision table.
type paramRow struct {
	Index    int
	Name     string
	Indep    stats.ParamDecision
	Mixed    *stats.ParamDecision
	Combined stats.Label
	// Margin is true for the six margin-judged parameters; knots and θ_neg get intervals only.
	Margin bool
	// Anchored estimate and ŝ are kept for the descriptive table.
	Anchored float64
}

// decide computes the per-parameter decisions on the normalized scale for both fits.
func decide(in decisionInput) ([]paramRow, float64, []float64) {
	U := in.U
	if U == nil {
		U = fit.DefaultU
	}
	estN, sHat := fit.NormalizeParams(in.Indep.Res.Params, U)
	bootN, sBoot := fit.NormalizeRows(in.Indep.Boot, U)
	sharesI := fit.Summarise(in.Indep.Meta)
	var estM []float64
	var bootM [][]float64
	var sharesM fit.Shares
	if in.Mixed != nil {
		estM, _ = fit.NormalizeParams(in.Mixed.Res.Params, U)
		bootM, _ = fit.NormalizeRows(in.Mixed.Boot, U)
		sharesM = fit.Summarise(in.Mixed.Meta)
	}
	th0 := fit.Theta0Extended(in.Theta0)
	margin := map[int]bool{}
	for _, p := range fit.MarginJudged {
		margin[p] = true
	}
	var rows []paramRow
	for i := range fit.NormLen {
		if _, tied := in.Tie[i]; tied {
			continue // reported in the "extrapolated" rows
		}
		row := paramRow{Index: i, Name: fit.NormName(i), Margin: margin[i]}
		if i < ged.NumParams {
			row.Anchored = in.Indep.Res.Params.Theta[i]
		} else if i == fit.NormEdgeExtra {
			row.Anchored = ged.EdgeExtraCost
		} else {
			row.Anchored = in.Indep.Res.Params.ThetaNeg
		}
		mk := func(est []float64, boot [][]float64, anchored [][]float64, sh fit.Shares) stats.ParamDecision {
			di := stats.DecisionInput{Est: est[i], Boot: fit.Column(boot, i), Theta0: th0[i], M: in.M}
			if i != fit.NormEdgeExtra {
				ai := i
				if i == fit.NormNeg {
					ai = ged.NumParams // ThetaVec layout: θ_neg follows the 11 costs
				}
				col := fit.Column(anchored, ai)
				di.AnchoredIQR = stats.Quantile(col, 0.75) - stats.Quantile(col, 0.25)
				di.CheckAnchored = true
			}
			if in.NInf != nil && i <= fit.NormEdgeExtra {
				di.NInformative = in.NInf[i]
			} else if in.NInf != nil && i == fit.NormNeg {
				di.NInformative = -1
			}
			if in.MinInf != nil {
				di.MinInformative = in.MinInf[i]
			}
			if i < ged.NumParams {
				di.BoundaryShare, di.DivergentShare = sh.Boundary[i], sh.Divergent[i]
			}
			if di.NInformative < 0 {
				di.NInformative, di.MinInformative = 0, 0
			}
			return stats.Decide(di)
		}
		row.Indep = mk(estN, bootN, in.Indep.Boot, sharesI)
		row.Combined = row.Indep.Label
		if in.Mixed != nil {
			d := mk(estM, bootM, in.Mixed.Boot, sharesM)
			row.Mixed = &d
			row.Combined = stats.Combine(row.Indep.Label, d.Label)
		}
		rows = append(rows, row)
	}
	return rows, sHat, sBoot
}

func ci(iv stats.Interval) string { return fmt.Sprintf("[%.2f, %.2f]", iv.Lo, iv.Hi) }

func intervalCell(d stats.ParamDecision) string {
	if d.OneSided {
		return fmt.Sprintf("片側 90%% 上限 %.2f", d.Upper90)
	}
	return ci(d.CI90)
}

// decisionReport writes the §3 decision table (normalized scale), the anchored descriptive table, the bootstrap
// diagnostics and the two "extrapolated" rows (§12b). It returns the rows for the verdict and the LOJO table.
func decisionReport(w io.Writer, in decisionInput) []paramRow {
	rows, sHat, sBoot := decide(in)
	fmt.Fprintln(w, "\n## パラメータの判断（決定文書 §2–§3、和で正規化した尺度）")
	fmt.Fprintf(w, "\n判断の幅 m = ±%.2f。尺度は U = {%s} の 6 費用の和が現行表の和（%.2f）に一致するよう正規化した θ̃ = θ̂ / ŝ（辺の余分は 1/ŝ）。", in.M, uNames(in.U), sumU(in.Theta0, in.U))
	fmt.Fprintf(w, " ŝ = %.3f", sHat)
	if col := sortedClean(sBoot); len(col) > 0 {
		fmt.Fprintf(w, "（90%% 区間 [%.3f, %.3f]）", stats.Quantile(col, 0.05), stats.Quantile(col, 0.95))
	}
	fmt.Fprintf(w, "。区間はクラスタブートストラップ %d 回のパーセンタイル（生成モデルで層化、元の議論を単位）。", len(in.Indep.Boot))
	fmt.Fprint(w, "SD = (84% 点 − 16% 点)/2。規則: 90% 区間が [θ₀ − m, θ₀ + m] に入れば「維持」（TOST）、全体が外なら「変更」、推定に情報がなければ「推定不能」（n_p が下限未満、固定尺度の反復の IQR < 1e-6、境界率 > 50%）、それ以外は「未決」。境界率 10–50% は片側 90% 上限のみ。")
	if in.Mixed != nil {
		fmt.Fprint(w, "共主要の 2 つの当てはめ（作業独立・ランダム切片）のラベルが一致したときだけ判断が立つ。\n")
	} else {
		fmt.Fprint(w, "**注意: 作業独立の当てはめだけで判断している（ランダム切片の当てはめなし）。**\n")
	}
	head := "| パラメータ | θ₀ |"
	sep := "| --- | --: |"
	if in.Truth != nil {
		head += " 真の値（正規化） |"
		sep += " --: |"
	}
	head += " θ̃ 推定値 | SD | 90% 区間 | 95% 区間 | n_p | 境界率 | 判断"
	sep += " --: | --: | --- | --- | --: | --: | ---"
	if in.Mixed != nil {
		head += " | θ̃（切片） | 90%（切片） | 判断（切片） | 判断（共通）"
		sep += " | --: | --- | --- | ---"
	}
	fmt.Fprintln(w, "\n"+head+" |")
	fmt.Fprintln(w, sep+" |")
	th0 := fit.Theta0Extended(in.Theta0)
	var truthN []float64
	if in.Truth != nil {
		v := append(slices.Clone(in.Truth[:]), 0)
		if in.TrueNeg != nil {
			v[ged.NumParams] = *in.TrueNeg
		}
		truthN, _ = fit.Normalize(v, in.U)
	}
	for _, r := range rows {
		d := r.Indep
		label := d.Label.JA()
		if !r.Margin {
			label = "—（区間のみ）"
		}
		line := fmt.Sprintf("| %s | %s |", r.Name, num(th0[r.Index]))
		if truthN != nil {
			line += fmt.Sprintf(" %.2f |", truthN[r.Index])
		}
		nCell := "—"
		if d.N > 0 || d.MinN > 0 {
			nCell = fmt.Sprintf("%d", d.N)
			if d.MinN > 0 {
				nCell += fmt.Sprintf(" (≥%d)", d.MinN)
			}
		}
		line += fmt.Sprintf(" %.2f | %.3f | %s | %s | %s | %.0f%% | %s", d.Est, d.SD, intervalCell(d), ci(d.CI95), nCell, 100*d.BoundaryShare, label)
		if r.Mixed != nil {
			m := *r.Mixed
			ml, cl := m.Label.JA(), r.Combined.JA()
			if !r.Margin {
				ml, cl = "—", "—"
			}
			line += fmt.Sprintf(" | %.2f | %s | %s | %s", m.Est, intervalCell(m), ml, cl)
		}
		fmt.Fprintln(w, line+" |")
	}
	// Extrapolated rows (§12b).
	if len(in.Tie) > 0 {
		fmt.Fprintln(w, "\n判断なしの行（決定文書 §12b）:")
		fmt.Fprintln(w, "\n| 種別の組 | 扱い | 診断用の自由推定（正規化） | θ_type との差（95% 区間） | 実現可能な編集数 |")
		fmt.Fprintln(w, "| --- | --- | --- | --- | --: |")
		for p, master := range in.Tie {
			name := ged.ParamNames[p]
			diag, contrast, feas, countsOnly := "—", "—", "—", false
			if in.DiagCA != nil {
				var d stats.ParamDecision
				var c stats.ContrastResult
				switch p {
				case ged.PTypeRACA:
					d, c, feas, countsOnly = in.DiagCA.RACA, in.DiagCA.ContrastRACA, fmt.Sprint(in.DiagCA.FeasibleRACA), in.DiagCA.CountsOnlyRACA
				case ged.PTypeCAMA:
					d, c, feas, countsOnly = in.DiagCA.CAMA, in.DiagCA.ContrastCAMA, fmt.Sprint(in.DiagCA.FeasibleCAMA), in.DiagCA.CountsOnlyCAMA
				}
				if !countsOnly && d.Reps > 0 {
					diag = fmt.Sprintf("%.2f %s", d.Est, ci(d.CI90))
					contrast = fmt.Sprintf("%.2f %s", c.Est, ci(c.CI95))
				} else if countsOnly {
					diag = "件数のみ（実現不能率 > 60%）"
				}
			}
			fmt.Fprintf(w, "| %s | **%s から外挿、較正なし** | %s | %s | %s |\n", name, ged.ParamNames[master], diag, contrast, feas)
		}
		fmt.Fprintln(w, "\n使途の 81 件の種別誤りのうち 14 件（17%）は CA を含み、仮定で値を置いている。診断セルは実現可能な（弱い関係の）付け替えだけなので、一般の符号反転の費用ではない。")
	}
	// Anchored descriptive table and bootstrap diagnostics.
	fmt.Fprintln(w, "\n### 固定尺度（辺の余分 = 1）の推定値とブートストラップの診断")
	fmt.Fprintln(w, "\n| パラメータ | 固定尺度の推定値 | ブートストラップの偏り（正規化） | 偏りの警告 | 境界率 | 発散率 | IQR |")
	fmt.Fprintln(w, "| --- | --: | --: | --- | --: | --: | --: |")
	for _, r := range rows {
		d := r.Indep
		flag := ""
		if d.BiasFlag {
			flag = "|偏り| > 0.5 SD"
		}
		fmt.Fprintf(w, "| %s | %.3f | %+.3f | %s | %.1f%% | %.1f%% | %.2e |\n", r.Name, r.Anchored, d.Bias, flag, 100*d.BoundaryShare, 100*d.DivergentShare, d.IQR)
	}
	sh := fit.Summarise(in.Indep.Meta)
	fmt.Fprintf(w, "\n反復の内訳（作業独立）: 循環 %.1f%%、上限到達 %.1f%%、非収束 %.1f%%、コールドスタート %d 回。", 100*sh.Cycled, 100*sh.Capped, 100*sh.NotConverged, sh.Cold)
	if in.Mixed != nil {
		shm := fit.Summarise(in.Mixed.Meta)
		fmt.Fprintf(w, " ランダム切片: 循環 %.1f%%、上限到達 %.1f%%、非収束 %.1f%%、σ̂ = %.3f。", 100*shm.Cycled, 100*shm.Capped, 100*shm.NotConverged, in.Mixed.Res.Params.Sigma)
	}
	fmt.Fprintln(w)
	for _, r := range rows {
		if r.Indep.DivergentShare > stats.DivergentReport {
			fmt.Fprintf(w, "- %s: 発散した反復が %.1f%% > 2%%。発散を除いた区間: %s\n", r.Name, 100*r.Indep.DivergentShare, ci(intervalWithout(in, r.Index)))
		}
	}
	if s := in.Indep.Res; len(s.Starts) > 0 {
		fmt.Fprintf(w, "- 多点開始（%d 点）の最良の開始点からの最大のずれ: %.3f", len(s.Starts), s.StartSpread)
		if s.StartSpread > 0.05 {
			fmt.Fprint(w, "（**0.05 を超える: 開始点で結果が変わる**）")
		}
		fmt.Fprintln(w)
		for _, st := range s.Starts {
			fmt.Fprintf(w, "  - %s: 目的関数 %.2f（収束 %v、循環 %v）\n", st.Name, st.Objective, st.Converged, st.Cycled)
		}
	}
	return rows
}

// intervalWithout recomputes the 90% interval of normalized index i without the divergent replicates.
func intervalWithout(in decisionInput, i int) stats.Interval {
	var rows [][]float64
	for r, m := range in.Indep.Meta {
		div := false
		for p := range ged.NumParams {
			div = div || m.Divergent[p]
		}
		if !div && r < len(in.Indep.Boot) {
			rows = append(rows, in.Indep.Boot[r])
		}
	}
	n, _ := fit.NormalizeRows(rows, in.U)
	col := fit.Column(n, i)
	return stats.Interval{Lo: stats.Quantile(col, 0.05), Hi: stats.Quantile(col, 0.95)}
}

func uNames(U []int) string {
	if U == nil {
		U = fit.DefaultU
	}
	var names []string
	for _, p := range U {
		names = append(names, fit.NormName(p))
	}
	return strings.Join(names, ", ")
}

func sumU(th0 ged.Theta, U []int) float64 {
	if U == nil {
		U = fit.DefaultU
	}
	ext := fit.Theta0Extended(th0)
	s := 0.0
	for _, p := range U {
		s += ext[p]
	}
	return s
}

func sortedClean(v []float64) []float64 {
	var out []float64
	for _, x := range v {
		if !math.IsNaN(x) && !math.IsInf(x, 0) {
			out = append(out, x)
		}
	}
	slices.Sort(out)
	return out
}

// paramTable keeps the first-version entry point: the §3 rule on the normalized scale with the independence fit only
// (no information floor, no mixed fit). The pilot and the simulation call it; the final analysis uses decisionReport.
func paramTable(w io.Writer, th0 ged.Theta, truth *ged.Theta, trueNeg *float64, est fit.Params, boot [][]float64, m float64) {
	in := decisionInput{Theta0: th0, M: m, Indep: fitSummary{Name: "作業独立", Res: fit.Result{Params: est}, Boot: boot}, Truth: truth, TrueNeg: trueNeg}
	decisionReport(w, in)
}

// ---------------------------------------------------------------------------------------------------------------
// Confirmatory hypotheses (§4) and verdict (§3)
// ---------------------------------------------------------------------------------------------------------------

// ruleContrast is a rule comparison for C1/C4: Δ of the two rules on every triplet.
type ruleContrast struct {
	ID, Name string
	D0, D1   []float64 // D0 = R0; D1 = the comparator (R1 or B1)
}

// hypothesesInput gathers the inputs of the six confirmatory tests.
type hypothesesInput struct {
	M     float64
	U     []int
	Indep fitSummary
	Mixed *fitSummary
	// Trips/Js for the C1/C4 clustered McNemar; Contrasts lists C1 (R0 vs R1) and C4 (R0 vs B1).
	Trips     []fit.Triplet
	Js        []fit.Judgment
	Contrasts []ruleContrast
	Reps      int
	Seed      uint64
}

type hypothesisResult struct {
	ID, Name  string
	Indep     stats.ContrastResult
	Mixed     *stats.ContrastResult
	Supported bool // under both fits when the mixed fit is present
	PHolm     float64
	Reject    bool
}

// contrastBoot computes a contrast f on the estimate and on every normalized replicate.
func contrastBoot(s fitSummary, U []int, f func(v []float64) float64) (float64, []float64) {
	est, _ := fit.NormalizeParams(s.Res.Params, U)
	rows, _ := fit.NormalizeRows(s.Boot, U)
	var b []float64
	for _, r := range rows {
		if r != nil {
			b = append(b, f(r))
		}
	}
	return f(est), b
}

// hypothesesTable runs H1, H2, H4a, H4b, C1, C4 with their stated null values, Holm over the six, and writes the table.
func hypothesesTable(w io.Writer, in hypothesesInput) []hypothesisResult {
	type spec struct {
		id, name string
		f        func(v []float64) float64
		null     float64
		side     stats.Side
	}
	specs := []spec{
		{"H1", "θ̃_type − (θ̃_辺の欠落 + θ̃_辺の余分) < 0", func(v []float64) float64 { return v[ged.PTypeRAMA] - (v[ged.PEdgeMissing] + v[fit.NormEdgeExtra]) }, 0, stats.Less},
		{"H2", "θ̃_向き − θ̃_辺の欠落 ≥ −m（非劣性）", func(v []float64) float64 { return v[ged.PDirection] - v[ged.PEdgeMissing] }, -in.M, stats.Greater},
		{"H4a", "θ̃_I欠落 − θ̃_I余分 ≠ 0", func(v []float64) float64 { return v[ged.PNodeMissing] - v[ged.PNodeExtra] }, 0, stats.TwoSided},
		{"H4b", "θ̃_辺の欠落 − θ̃_辺の余分 ≠ 0", func(v []float64) float64 { return v[ged.PEdgeMissing] - v[fit.NormEdgeExtra] }, 0, stats.TwoSided},
	}
	var res []hypothesisResult
	for _, s := range specs {
		est, b := contrastBoot(in.Indep, in.U, s.f)
		h := hypothesisResult{ID: s.id, Name: s.name, Indep: stats.BootstrapContrast(est, b, s.null, s.side)}
		h.Supported = h.Indep.Supported
		if in.Mixed != nil {
			estM, bM := contrastBoot(*in.Mixed, in.U, s.f)
			m := stats.BootstrapContrast(estM, bM, s.null, s.side)
			h.Mixed = &m
			h.Supported = h.Supported && m.Supported
		}
		res = append(res, h)
	}
	nonNeg := make([]bool, len(in.Trips))
	cluster := make([]int, len(in.Trips))
	strata := make([]string, len(in.Trips))
	for i, t := range in.Trips {
		nonNeg[i], cluster[i], strata[i] = t.NegDiff == 0, t.Source, t.Generator
	}
	for _, c := range in.Contrasts {
		r0, elig := fit.Correct(c.D0, in.Js, nonNeg)
		r1, _ := fit.Correct(c.D1, in.Js, nonNeg)
		cr := stats.ClusteredMcNemar(r0, r1, elig, cluster, strata, in.Reps, in.Seed)
		b, cc := stats.DiscordantCounts(r0, r1, elig)
		h := hypothesisResult{ID: c.ID, Name: fmt.Sprintf("agree(R0) − agree(%s) > 0（不一致 %d / %d、正確 McNemar 片側 p = %.3f）", c.Name, b, cc, stats.McNemarExact(b, cc, true)), Indep: cr, Supported: cr.Supported}
		res = append(res, h)
	}
	ps := make([]float64, len(res))
	for i, h := range res {
		ps[i] = h.Indep.P
		if h.Mixed != nil {
			ps[i] = math.Max(ps[i], h.Mixed.P)
		}
		if math.IsNaN(ps[i]) {
			ps[i] = 1
		}
	}
	adj, rej := stats.Holm(ps, 0.05)
	for i := range res {
		res[i].PHolm, res[i].Reject = adj[i], rej[i]
	}
	fmt.Fprintln(w, "\n## 確認的仮説（決定文書 §4、正規化した尺度、ブートストラップ対比）")
	fmt.Fprintf(w, "\n帰無値を検定ごとに明示。片側 p は帰無値の反対側にある反復の割合、両側は 2 倍。族（6 検定）は Holm で α = 0.05。H1・H2 は判定には無調整の α で入る（交差–和集合検定）。H3 は計画 v4 からの逸脱として取り下げ（RA↔CA は内容を変えずに実現できない）。B = %d。\n", len(in.Indep.Boot))
	fmt.Fprintln(w, "\n| ID | 対比 | 帰無値 | 推定値 | 95% 区間 | p | p（Holm） | 支持 |")
	fmt.Fprintln(w, "| --- | --- | --: | --: | --- | --: | --: | --- |")
	for _, h := range res {
		sup := "いいえ"
		if h.Supported {
			sup = "はい"
		}
		cell := fmt.Sprintf("%.3f", h.Indep.Est)
		ciCell := ci(h.Indep.CI95)
		if h.Mixed != nil {
			cell += fmt.Sprintf(" / %.3f", h.Mixed.Est)
			ciCell += " / " + ci(h.Mixed.CI95)
		}
		fmt.Fprintf(w, "| %s | %s | %.2f | %s | %s | %.3f | %.3f | %s |\n", h.ID, h.Name, h.Indep.Null, cell, ciCell, h.Indep.P, h.PHolm, sup)
	}
	if in.Mixed != nil {
		fmt.Fprintln(w, "\n推定値と区間は「作業独立 / ランダム切片」。「支持」は両方の当てはめで支持されたときだけ「はい」。")
	}
	fmt.Fprintln(w, "\nH4 は非対称性の主張であり表の妥当性の主張ではない: 「H4b 支持」と「辺の欠落: 維持」は両立しうる。")
	return res
}

// verdictReport writes the verdict of §3.
func verdictReport(w io.Writer, rows []paramRow, hyps []hypothesisResult, d *fit.DResult) stats.VerdictLabel {
	var labels []stats.Label
	var names []string
	for _, r := range rows {
		if r.Margin {
			labels = append(labels, r.Combined)
			names = append(names, r.Name)
		}
	}
	in := stats.VerdictInput{Labels: labels, Names: names, DLower95: math.NaN(), DUpper95: math.NaN()}
	for _, h := range hyps {
		switch h.ID {
		case "H1":
			in.H1 = h.Supported
		case "H2":
			in.H2 = h.Supported
		}
	}
	if d != nil && len(d.AgreeBoot) > 0 {
		col := sortedClean(d.AgreeBoot)
		in.DLower95, in.DUpper95 = stats.Quantile(col, 0.025), stats.Quantile(col, 0.975)
	}
	v, missing := stats.Verdict(in)
	fmt.Fprintln(w, "\n## 現行表の判定（決定文書 §3）")
	fmt.Fprintf(w, "\n**%s（%s）**\n", v.JA(), v)
	if d != nil {
		fmt.Fprintf(w, "\n- D = agree(R2, OOF) − agree(R0) = %.2f pt、95%% 区間 [%.2f, %.2f]（元の議論のネストしたブートストラップ %d 回、各反復で 5 分割 CV を再学習）。妥当の条件は上限 < 2 pt。対数損失の形: %.4f nats/判定（正なら R2 が良い）。\n", d.Agree, in.DLower95, in.DUpper95, len(d.AgreeBoot), d.LogLoss)
		if len(d.Strata) > 0 {
			fmt.Fprintln(w, "\n| 層 | D（pt） | 95% 区間 |")
			fmt.Fprintln(w, "| --- | --: | --- |")
			keys := make([]string, 0, len(d.Strata))
			for k := range d.Strata {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				col := sortedClean(d.StrataBoot[k])
				fmt.Fprintf(w, "| %s | %.2f | [%.2f, %.2f] |\n", k, d.Strata[k], stats.Quantile(col, 0.025), stats.Quantile(col, 0.975))
			}
		}
	}
	if len(missing) > 0 {
		fmt.Fprintln(w, "\n欠けている要素:")
		for _, m := range missing {
			fmt.Fprintf(w, "- %s\n", m)
		}
	}
	fmt.Fprintln(w, "\n「否定されず」は現行表を続ける許可ではない: データが決めなかったという意味。")
	return v
}

// ---------------------------------------------------------------------------------------------------------------
// Leave-one-judge-out (§9)
// ---------------------------------------------------------------------------------------------------------------

// leaveOneJudgeOut refits (and bootstraps) with each judge removed and returns the decision rows per dropped judge.
func leaveOneJudgeOut(trips []fit.Triplet, js []fit.Judgment, opts fit.Options, bo fit.BootOptions, in decisionInput) ([][]paramRow, error) {
	var out [][]paramRow
	for j := range opts.Judges {
		o := opts
		o.Judges--
		if o.Judges == 0 {
			return nil, fmt.Errorf("leave-one-judge-out needs at least 2 judges")
		}
		dj := fit.DropJudge(js, j)
		res, err := fit.FitMulti(trips, dj, o, nil)
		if err != nil {
			return nil, err
		}
		b := bo
		var mixedRes fit.Result
		if in.Mixed != nil {
			mixedRes, err = fit.FitMixed(trips, dj, o, nil)
			if err != nil {
				return nil, err
			}
			b.Mixed, b.MixedInit = true, &mixedRes.Params
		}
		br, err := fit.BootstrapOpts(trips, dj, o, res.Params, b)
		if err != nil {
			return nil, err
		}
		sub := in
		sub.Indep = fitSummary{Name: in.Indep.Name, Res: res, Boot: br.Theta, Meta: br.Meta}
		if in.Mixed != nil {
			sub.Mixed = &fitSummary{Name: in.Mixed.Name, Res: mixedRes, Boot: br.MixedTheta, Meta: br.MixedMeta}
		}
		rows, _, _ := decide(sub)
		out = append(out, rows)
	}
	return out, nil
}

// lojoTable writes the leave-one-judge-out decision table and flags labels that change.
func lojoTable(w io.Writer, names []string, full []paramRow, dropped [][]paramRow) {
	fmt.Fprintln(w, "\n## 判定者を 1 人ずつ除いた判断（決定文書 §9）")
	head := "| パラメータ | 全員 |"
	sep := "| --- | --- |"
	for _, n := range names {
		head += fmt.Sprintf(" %s を除く |", n)
		sep += " --- |"
	}
	fmt.Fprintln(w, "\n"+head)
	fmt.Fprintln(w, sep)
	changed := 0
	for i, r := range full {
		if !r.Margin {
			continue
		}
		line := fmt.Sprintf("| %s | %s |", r.Name, r.Combined.JA())
		for _, d := range dropped {
			if i >= len(d) {
				line += " — |"
				continue
			}
			cell := d[i].Combined.JA()
			if d[i].Combined != r.Combined {
				cell = "**" + cell + "**"
				changed++
			}
			line += fmt.Sprintf(" %s（%.2f） |", cell, d[i].Indep.Est)
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "\n太字 = 全員での判断と異なる（%d 箇所）。\n", changed)
}

// ---------------------------------------------------------------------------------------------------------------
// Judge QC (§9, §9.1, §16 #10)
// ---------------------------------------------------------------------------------------------------------------

// judgeQCInput is the material for the mechanical judge QC, evaluated before θ is fitted.
type judgeQCInput struct {
	Names []string
	Trips []fit.Triplet
	Js    []fit.Judgment
	// GateDelta is the Δ(θ₀) floor of the gate stratum (0 → 2); RMax, Conf the gate (0 → 0.20, 0.90).
	GateDelta, RMax, Conf float64
}

// judgeQC writes the T1 reversal gate, the reversal rate by Δ(θ₀) stratum, order consistency, tie share and
// Krippendorff's α per judge pair, and returns the gate results.
func judgeQC(w io.Writer, in judgeQCInput) []stats.GateResult {
	gd := in.GateDelta
	if gd <= 0 {
		gd = 2
	}
	J := len(in.Names)
	isT1 := func(t fit.Triplet) bool { return t.Type == "T1" || t.HasFlag("t1_gate") }
	stratumOf := func(d float64) string {
		switch {
		case d <= 0:
			return "≤ 0（除外）"
		case d < 0.5:
			return "(0, 0.5)"
		case d < 1:
			return "[0.5, 1)"
		case d < 2:
			return "[1, 2)"
		}
		return "≥ 2（ゲート）"
	}
	type cnt struct{ rev, tie, n int }
	gate := make([]cnt, J)
	byStratum := make([]map[string]*cnt, J)
	tieAll := make([]cnt, J)
	nT := 0
	seenGate := map[int]bool{}
	pairs := make([]map[int][2]int, J)
	for j := range J {
		byStratum[j] = map[string]*cnt{}
		pairs[j] = map[int][2]int{}
	}
	for _, k := range in.Js {
		if k.Judge >= J {
			continue
		}
		t := in.Trips[k.Triplet]
		c := &tieAll[k.Judge]
		c.n++
		if k.Answer == fit.AnswerTie {
			c.tie++
		}
		p := pairs[k.Judge][k.Triplet]
		if k.Order > 0 {
			p[0] = int(k.Answer) + 1
		} else {
			p[1] = int(k.Answer) + 1
		}
		pairs[k.Judge][k.Triplet] = p
		if !isT1(t) {
			continue
		}
		s := stratumOf(t.T1Delta0)
		sc := byStratum[k.Judge][s]
		if sc == nil {
			sc = &cnt{}
			byStratum[k.Judge][s] = sc
		}
		sc.n++
		if k.Answer == fit.AnswerB {
			sc.rev++
		}
		if k.Answer == fit.AnswerTie {
			sc.tie++
		}
		if t.T1Delta0 >= gd {
			g := &gate[k.Judge]
			g.n++
			if k.Answer == fit.AnswerB {
				g.rev++
			}
			if k.Answer == fit.AnswerTie {
				g.tie++
			}
			if !seenGate[k.Triplet] {
				seenGate[k.Triplet] = true
				nT++
			}
		}
	}
	fmt.Fprintln(w, "\n## 判定者の品質管理（決定文書 §9・§9.1、θ の当てはめ前に機械的に評価）")
	fmt.Fprintf(w, "\nT1 反転率 r_j = 「上位集合 B が近い」と答えた判定 / ゲート三つ組（Δ(θ₀) ≥ %.1f、n_T = %d）での判定。「同程度」は分母のみ。二項の参照は k_eff = r_j·n_T、n_eff = n_T（設計効果 2）。片側 90%% Clopper–Pearson 上限が r_max = %.2f を超えると不合格。\n", gd, nT, defaultF(in.RMax, stats.GateRMax))
	fmt.Fprintln(w, "\n| 判定者 | 判定数 | 同程度の割合 | 提示順の一致 | ゲート判定数 | 反転 | r_j | 上限（90%） | ゲート |")
	fmt.Fprintln(w, "| --- | --: | --: | --: | --: | --: | --: | --: | --- |")
	var out []stats.GateResult
	for j, name := range in.Names {
		g := stats.ReversalGate(gate[j].rev, gate[j].tie, gate[j].n, nT, in.RMax, in.Conf)
		out = append(out, g)
		var pp [][2]int
		for _, p := range pairs[j] {
			if p[0] > 0 && p[1] > 0 {
				pp = append(pp, p)
			}
		}
		oc, on := stats.OrderConsistency(pp)
		verdict := "合格"
		if g.Fail {
			verdict = "**不合格**"
		}
		if gate[j].n == 0 {
			verdict = "—（ゲート三つ組なし）"
		}
		fmt.Fprintf(w, "| %s | %d | %.1f%% | %s | %d | %d | %s | %s | %s |\n", name, tieAll[j].n, pct(tieAll[j].tie, tieAll[j].n), ratioF(oc, on), gate[j].n, gate[j].rev, num(g.R), num(g.Upper), verdict)
	}
	// By Δ stratum (descriptive).
	strata := map[string]bool{}
	for j := range J {
		for s := range byStratum[j] {
			strata[s] = true
		}
	}
	if len(strata) > 0 {
		keys := make([]string, 0, len(strata))
		for s := range strata {
			keys = append(keys, s)
		}
		sort.Strings(keys)
		fmt.Fprintln(w, "\n反転率の Δ(θ₀) 層別（記述）:")
		head := "| 判定者 |"
		sep := "| --- |"
		for _, s := range keys {
			head += " " + s + " |"
			sep += " --- |"
		}
		fmt.Fprintln(w, "\n"+head)
		fmt.Fprintln(w, sep)
		for j, name := range in.Names {
			line := "| " + name + " |"
			for _, s := range keys {
				c := byStratum[j][s]
				if c == nil || c.n == 0 {
					line += " — |"
				} else {
					line += fmt.Sprintf(" %.1f%%（%d/%d） |", pct(c.rev, c.n), c.rev, c.n)
				}
			}
			fmt.Fprintln(w, line)
		}
	}
	// Krippendorff's α per pair (units = triplet × order).
	if J >= 2 {
		fmt.Fprintln(w, "\nKrippendorff の α（名義、単位 = 三つ組 × 提示順）、基準 ≥ 0.4:")
		fmt.Fprintln(w, "\n| 判定者 | "+strings.Join(in.Names, " | ")+" |")
		fmt.Fprintln(w, "| --- |"+strings.Repeat(" --: |", J))
		for a := range J {
			line := "| " + in.Names[a] + " |"
			for b := range J {
				if a == b {
					line += " — |"
					continue
				}
				line += fmt.Sprintf(" %.3f |", pairAlpha(in.Js, len(in.Trips), a, b))
			}
			fmt.Fprintln(w, line)
		}
	}
	fmt.Fprintln(w, "\n編集種別ごとの反転率は三つ組の記録（dataset-spec §6）から別途集計する（fit.Triplet は編集種別を持たない）。")
	return out
}

// pairAlpha is Krippendorff's α between judges a and b over (triplet, order) units.
func pairAlpha(js []fit.Judgment, nTrips int, a, b int) float64 {
	vals := make([][]int, 2*nTrips)
	for i := range vals {
		vals[i] = []int{-1, -1}
	}
	for _, k := range js {
		u := 2 * k.Triplet
		if k.Order < 0 {
			u++
		}
		switch k.Judge {
		case a:
			vals[u][0] = int(k.Answer)
		case b:
			vals[u][1] = int(k.Answer)
		}
	}
	return stats.KrippendorffAlpha(vals)
}

func defaultF(v, d float64) float64 {
	if v <= 0 {
		return d
	}
	return v
}

func ratioF(v float64, n int) string {
	if n == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%（n=%d）", 100*v, n)
}

// judgeDiagnostics keeps the first-version entry point (pilot and simulation): the T1 column is now the reversal rate
// with the §9.1 gate on all T1 triplets with a known correct side (the simulation has no Δ(θ₀) strata), and the
// accuracy statistic is retired (§16 #10).
func judgeDiagnostics(w io.Writer, names []string, types []perturb.TripletType, trueSign []int, js []fit.Judgment) {
	fmt.Fprintln(w, "\n## 判定者の診断（決定文書 §9.1）")
	fmt.Fprintln(w, "\n| 判定者 | 判定数 | 同程度の割合 | 提示順を入れ替えても同じ | T1 反転率 | 上限（90%） | ゲート（r_max 0.20） |")
	fmt.Fprintln(w, "| --- | --: | --: | --: | --: | --: | --- |")
	for j, name := range names {
		var n, tie int
		byT := map[int][]fit.Answer{}
		t1n, t1rev, t1tie := 0, 0, 0
		t1Trips := map[int]bool{}
		for _, k := range js {
			if k.Judge != j {
				continue
			}
			n++
			if k.Answer == fit.AnswerTie {
				tie++
			}
			byT[k.Triplet] = append(byT[k.Triplet], k.Answer)
			if types[k.Triplet] == perturb.T1Monotone && trueSign[k.Triplet] != 0 {
				t1n++
				t1Trips[k.Triplet] = true
				wrong := fit.AnswerB
				if trueSign[k.Triplet] < 0 {
					wrong = fit.AnswerA
				}
				switch k.Answer {
				case wrong:
					t1rev++
				case fit.AnswerTie:
					t1tie++
				}
			}
		}
		same, pairs := 0, 0
		for _, as := range byT {
			if len(as) == 2 {
				pairs++
				if as[0] == as[1] {
					same++
				}
			}
		}
		g := stats.ReversalGate(t1rev, t1tie, t1n, len(t1Trips), 0, 0)
		gate := "—"
		if t1n > 0 {
			gate = "合格"
			if g.Fail {
				gate = "**不合格**"
			}
		}
		fmt.Fprintf(w, "| %s | %d | %.1f%% | %s | %s | %s | %s |\n", name, n, pct(tie, n), ratio(same, pairs), ratio(t1rev, t1n), num(g.Upper), gate)
	}
}

func pct(a, b int) float64 {
	if b == 0 {
		return math.NaN()
	}
	return 100 * float64(a) / float64(b)
}

func ratio(a, b int) string {
	if b == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%（%d/%d）", pct(a, b), a, b)
}

func num(v float64) string {
	if math.IsNaN(v) {
		return "—"
	}
	return fmt.Sprintf("%.2f", v)
}

// ---------------------------------------------------------------------------------------------------------------
// Auxiliary parameters, agreement, ceiling
// ---------------------------------------------------------------------------------------------------------------

// auxTable is the per-judge auxiliary parameters.
func auxTable(w io.Writer, names []string, est fit.Params, truth []judgeTruth) {
	fmt.Fprintln(w, "\n## 判定者の補助パラメータ")
	if truth != nil {
		fmt.Fprintln(w, "\n| 判定者 | β（真） | β | τ（真） | τ | π（真） | π |")
		fmt.Fprintln(w, "| --- | --: | --: | --: | --: | --: | --: |")
		for j, n := range names {
			fmt.Fprintf(w, "| %s | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f |\n", n, truth[j].beta, est.Beta[j], truth[j].tau, est.Tau[j], truth[j].pi, est.Pi[j])
		}
		fmt.Fprintln(w, "\n（合成の判定には三つ組ごとの共有のずれ ε_t が入るので、作業独立の推定の β は真の β より小さく出るのが正常。）")
		return
	}
	fmt.Fprintln(w, "\n| 判定者 | β 鋭さ | τ 同程度の幅 | π 位置バイアス |")
	fmt.Fprintln(w, "| --- | --: | --: | --: |")
	for j, n := range names {
		fmt.Fprintf(w, "| %s | %.2f | %.2f | %.2f |\n", n, est.Beta[j], est.Tau[j], est.Pi[j])
	}
	if est.Sigma > 0 {
		fmt.Fprintf(w, "\nランダム切片の SD σ̂ = %.3f\n", est.Sigma)
	}
}

type judgeTruth struct{ beta, tau, pi float64 }

// agreementTable is the agreement rate per rule (plan §7.3).
func agreementTable(w io.Writer, rows []agreeRow, js []fit.Judgment, negMask []bool) {
	fmt.Fprintln(w, "\n## 規則ごとの一致率（計画 §7.3）")
	fmt.Fprintln(w, "\nΔ の符号が判定の多数（A / B）と一致する割合。多数が「同程度」の三つ組は除く。")
	fmt.Fprintln(w, "\n| 規則 | 全体 | 極性反転なし | 極性反転あり |")
	fmt.Fprintln(w, "| --- | --- | --- | --- |")
	noNeg := make([]bool, len(negMask))
	for i, v := range negMask {
		noNeg[i] = !v
	}
	for _, r := range rows {
		a, n := fit.Agreement(r.delta, js, nil)
		b, nb := fit.Agreement(r.delta, js, noNeg)
		c, nc := fit.Agreement(r.delta, js, negMask)
		fmt.Fprintf(w, "| %s | %s | %s | %s |\n", r.name, agr(a, n), agr(b, nb), agr(c, nc))
	}
}

type agreeRow struct {
	name  string
	delta []float64
}

func agr(v float64, n int) string {
	if n == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%（n=%d）", 100*v, n)
}

// ceilingReport writes the leave-one-judge-out noise ceiling of §7 (descriptive) and the ratios for R0.
func ceilingReport(w io.Writer, names []string, nTrips int, js []fit.Judgment, r0 float64) {
	answers := make([][][]int, len(names))
	for j := range names {
		answers[j] = make([][]int, nTrips)
	}
	for _, k := range js {
		if k.Judge < len(names) {
			answers[k.Judge][k.Triplet] = append(answers[k.Judge][k.Triplet], int(k.Answer))
		}
	}
	c, per, n := stats.NoiseCeiling(answers)
	fmt.Fprintln(w, "\n## 雑音の上限（決定文書 §7、記述のみ・判定には使わない）")
	fmt.Fprintln(w, "\n判定者 j のラベル（2 提示順の多数）が、他の判定者の 4 判定の多数と一致する確率。")
	fmt.Fprintln(w, "\n| 判定者 | 一致率 | n |")
	fmt.Fprintln(w, "| --- | --: | --: |")
	for j, name := range names {
		fmt.Fprintf(w, "| %s | %s | %d |\n", name, num(per[j]), n[j])
	}
	ratio, excess := stats.CeilingRatios(r0, c)
	fmt.Fprintf(w, "\n上限 = %.3f、R0/上限 = %.3f、(R0 − 0.5)/(上限 − 0.5) = %.3f。上限と R2 の差が「加法的 GED が取りこぼす量」の要約（RQ4）。\n", c, ratio, excess)
}

// ---------------------------------------------------------------------------------------------------------------
// P2 report mode (§16 #12): no θ estimate
// ---------------------------------------------------------------------------------------------------------------

// p2Input is the material of the P2 pilot report. Everything is computed without fitting θ.
type p2Input struct {
	Names []string
	Trips []fit.Triplet
	Js    []fit.Judgment
	// Aux is the auxiliary fit at θ₀ (FixTheta) and Mixed its random-intercept counterpart (σ̂).
	Aux   fit.Params
	Mixed *fit.Params
	// Tie is the type tie used to count informative triplets; MinInf the §15.3 floor.
	Tie    map[int]int
	MinInf map[int]int
	// Discards are free-form rows (edit kind × generator → rate) supplied by the pipeline.
	Discards [][]string
	// StabilityHalfWidth is the half-width of the box around θ₀ for the matching-stability share (0 → 0.25).
	StabilityHalfWidth float64
	// GateTriplets is n_T assumed for the simulated pass probabilities (0 → 100).
	GateTriplets int
}

// writeP2Report prints aux parameters, σ̂, φ coverage, QC, simulated T1 pass probabilities, discard rates,
// ICC by exemplar pair and domain, and the matching-stability share, with no θ estimate.
func writeP2Report(w io.Writer, in p2Input) error {
	fmt.Fprintln(w, "\n# P2 報告（θ の推定なし、決定文書 §13 項目 2）")
	auxTable(w, in.Names, in.Aux, nil)
	if in.Mixed != nil {
		fmt.Fprintf(w, "\nランダム切片の当てはめ（θ = θ₀ 固定）: σ̂ = %.3f、β = %s\n", in.Mixed.Sigma, fmtSlice(in.Mixed.Beta))
	}
	// φ coverage per parameter.
	nInf, err := fit.Informative(in.Trips, ged.Current(), in.Tie)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "\n## φ の情報量（Δφ_p ≠ 0 の三つ組数、θ₀ で）")
	fmt.Fprintln(w, "\n| パラメータ | 三つ組数 | 3,000 組換算 | §15.3 の下限 |")
	fmt.Fprintln(w, "| --- | --: | --: | --: |")
	for p := 0; p <= fit.NormEdgeExtra; p++ {
		if _, tied := in.Tie[p]; tied {
			continue
		}
		scaled := float64(nInf[p]) * 3000 / math.Max(1, float64(len(in.Trips)))
		minCell := "—"
		if m, ok := in.MinInf[p]; ok {
			minCell = fmt.Sprint(m)
		}
		fmt.Fprintf(w, "| %s | %d | %.0f | %s |\n", fit.NormName(p), nInf[p], scaled, minCell)
	}
	negN := 0
	for _, t := range in.Trips {
		if t.NegDiff != 0 {
			negN++
		}
	}
	fmt.Fprintf(w, "| θ_neg | %d | %.0f | %d |\n", negN, float64(negN)*3000/math.Max(1, float64(len(in.Trips))), in.MinInf[fit.NormNeg])
	// cos intervals of the substitution function.
	dp, err := fit.DeltaPhi(in.Trips, ged.Current())
	if err != nil {
		return err
	}
	var knot [4]int
	for _, d := range dp {
		for k := range 4 {
			if math.Abs(d[ged.PSub09+k]) > 1e-12 {
				knot[k]++
			}
		}
	}
	fmt.Fprintf(w, "\n置換関数の節点ごとの情報（Δφ ≠ 0 の三つ組）: cos0.9 %d、cos0.7 %d、cos0.5 %d、cos0.3 %d（§15.3 は各区間 600 置換）。\n", knot[0], knot[1], knot[2], knot[3])
	// QC.
	gates := judgeQC(w, judgeQCInput{Names: in.Names, Trips: in.Trips, Js: in.Js})
	_ = gates
	// Simulated T1 pass probabilities from the fitted aux parameters.
	nT := in.GateTriplets
	if nT <= 0 {
		nT = 100
	}
	sigma := 0.0
	if in.Mixed != nil {
		sigma = in.Mixed.Sigma
	}
	fmt.Fprintln(w, "\n## §9.1 のゲートの合格確率（当てはめた補助パラメータからの模擬）")
	fmt.Fprintf(w, "\nΔ(θ₀) = 2（層の下端、保守的）で z = β_j·2 + π_j·o + u、u ~ N(0, σ̂²)、σ̂ = %.2f。無作為回答率は推定していない（反転率に +lapse/3 が乗る）。n_T = %d。\n", sigma, nT)
	fmt.Fprintln(w, "\n| 判定者 | 模擬の反転率 | 合格確率 |")
	fmt.Fprintln(w, "| --- | --: | --: |")
	for j, name := range in.Names {
		r := simulatedReversal(in.Aux.Beta[j], in.Aux.Tau[j], in.Aux.Pi[j], sigma, 2)
		fmt.Fprintf(w, "| %s | %.3f | %.3f |\n", name, r, stats.GatePassProbability(r, nT, 0, 0))
	}
	// Discard rates.
	if len(in.Discards) > 0 {
		fmt.Fprintln(w, "\n## 破棄率")
		fmt.Fprintln(w, "\n| "+strings.Join(in.Discards[0], " | ")+" |")
		fmt.Fprintln(w, "|"+strings.Repeat(" --- |", len(in.Discards[0])))
		for _, r := range in.Discards[1:] {
			fmt.Fprintln(w, "| "+strings.Join(r, " | ")+" |")
		}
	}
	// ICC by exemplar pair and domain.
	resid, err := scoreResiduals(in.Trips, in.Js, in.Aux)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "\n## 残差の級内相関（決定文書 §6; 0.05 を超えればその水準のブートストラップを感度分析に加える）")
	fmt.Fprintln(w, "\n| 水準 | 群数 | ICC(1) |")
	fmt.Fprintln(w, "| --- | --: | --: |")
	for _, lv := range []struct {
		name string
		key  func(fit.Triplet) string
	}{{"例示の組", func(t fit.Triplet) string { return t.ExemplarPair }}, {"領域", func(t fit.Triplet) string { return t.Domain }}, {"生成モデル", func(t fit.Triplet) string { return t.Generator }}} {
		groups := map[string][]float64{}
		for i, r := range resid {
			k := lv.key(in.Trips[i])
			if k == "" {
				continue
			}
			groups[k] = append(groups[k], r)
		}
		fmt.Fprintf(w, "| %s | %d | %s |\n", lv.name, len(groups), num(icc1(groups)))
	}
	// Matching stability.
	hw := in.StabilityHalfWidth
	if hw <= 0 {
		hw = 0.25
	}
	var box [ged.NumParams]float64
	for p := range box {
		box[p] = hw
	}
	share, err := matchingStability(in.Trips, ged.Current(), box)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\n## 対応付けの安定性（決定文書 §11.6）\n\nθ₀ ± %.2f の箱（各パラメータを 1 つずつ動かす）で対応付けが変わる三つ組の割合: %.1f%%。\n", hw, 100*share)
	return nil
}

// simulatedReversal is E[P(B | z)] over u ~ N(0, σ²) and both orders at Δ = delta for a judge (β, τ, π).
func simulatedReversal(beta, tau, pi, sigma, delta float64) float64 {
	const n = 41
	s := 0.0
	for i := range n {
		u := 0.0
		w := 1.0 / n
		if sigma > 0 {
			x := -4 + 8*float64(i)/float64(n-1)
			u = sigma * x
			w = math.Exp(-x*x/2) / math.Sqrt(2*math.Pi) * (8.0 / float64(n-1))
		}
		for _, o := range []float64{1, -1} {
			_, pb, _ := fit.Probs(beta*delta+pi*o+u, tau)
			s += w * pb / 2
		}
	}
	return s
}

// scoreResiduals returns per-triplet mean residuals score − E[score] with score A = +1, B = −1, same = 0 under p
// (θ at p.Theta, no shared offset); used for the ICC check.
func scoreResiduals(trips []fit.Triplet, js []fit.Judgment, p fit.Params) ([]float64, error) {
	d, err := fit.Delta(trips, p.Theta)
	if err != nil {
		return nil, err
	}
	sum := make([]float64, len(trips))
	cnt := make([]int, len(trips))
	for _, k := range js {
		t := k.Triplet
		z := p.Beta[k.Judge]*(d[t]+p.ThetaNeg*trips[t].NegDiff) + p.Pi[k.Judge]*k.Order + p.Gamma*trips[t].DeltaCoh()
		pa, pb, _ := fit.Probs(z, p.Tau[k.Judge])
		score := 0.0
		switch k.Answer {
		case fit.AnswerA:
			score = 1
		case fit.AnswerB:
			score = -1
		}
		sum[t] += score - (pa - pb)
		cnt[t]++
	}
	out := make([]float64, len(trips))
	for t := range out {
		if cnt[t] > 0 {
			out[t] = sum[t] / float64(cnt[t])
		} else {
			out[t] = math.NaN()
		}
	}
	return out, nil
}

// icc1 is the one-way ANOVA intraclass correlation ICC(1) of grouped values (NaN with < 2 groups).
func icc1(groups map[string][]float64) float64 {
	k := len(groups)
	if k < 2 {
		return math.NaN()
	}
	N := 0
	grand := 0.0
	for _, g := range groups {
		for _, v := range g {
			if !math.IsNaN(v) {
				grand += v
				N++
			}
		}
	}
	if N <= k {
		return math.NaN()
	}
	grand /= float64(N)
	ssb, ssw := 0.0, 0.0
	n0 := float64(N)
	sumN2 := 0.0
	for _, g := range groups {
		m, n := 0.0, 0
		for _, v := range g {
			if !math.IsNaN(v) {
				m += v
				n++
			}
		}
		if n == 0 {
			continue
		}
		m /= float64(n)
		ssb += float64(n) * (m - grand) * (m - grand)
		for _, v := range g {
			if !math.IsNaN(v) {
				ssw += (v - m) * (v - m)
			}
		}
		sumN2 += float64(n * n)
	}
	n0 = (float64(N) - sumN2/float64(N)) / float64(k-1)
	msb := ssb / float64(k-1)
	msw := ssw / float64(N-k)
	return (msb - msw) / (msb + (n0-1)*msw)
}

// matchingStability is the share of triplets whose matching signature changes at any corner of the per-parameter
// box center ± halfWidth[p] (decisions §11.6).
func matchingStability(trips []fit.Triplet, center ged.Theta, halfWidth [ged.NumParams]float64) (float64, error) {
	if len(trips) == 0 {
		return math.NaN(), nil
	}
	base, err := fit.Signatures(trips, center)
	if err != nil {
		return 0, err
	}
	changed := make([]bool, len(trips))
	for p := range ged.NumParams {
		for _, dir := range []float64{-1, 1} {
			th := center
			th[p] = math.Max(0, th[p]+dir*halfWidth[p])
			s, err := fit.Signatures(trips, th)
			if err != nil {
				return 0, err
			}
			for i := range s {
				if s[i] != base[i] {
					changed[i] = true
				}
			}
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

func fmtSlice(v []float64) string {
	var s []string
	for _, x := range v {
		s = append(s, fmt.Sprintf("%.2f", x))
	}
	return "(" + strings.Join(s, ", ") + ")"
}

// ---------------------------------------------------------------------------------------------------------------
// Provenance (§16 #13)
// ---------------------------------------------------------------------------------------------------------------

// provenance is the block recorded in every output.
type provenance struct {
	Seeds        map[string]uint64
	PromptHashes map[string]string
	ModelIDs     map[string]string
	Lambda, M    float64
	B            int
	U            []int
	Extra        map[string]string
}

// gitDescribe returns `git describe --tags --always --dirty` of the working tree (or "unknown").
func gitDescribe() string {
	out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// writeProvenance prints the provenance block.
func writeProvenance(w io.Writer, p provenance) {
	fmt.Fprintln(w, "\n## 来歴（決定文書 §13・§16 #13）")
	fmt.Fprintf(w, "\n- コード: git %s\n", gitDescribe())
	fmt.Fprintf(w, "- λ = %g、m = %g、B = %d、正規化集合 U = {%s}\n", p.Lambda, p.M, p.B, uNames(p.U))
	for _, kv := range sortedKV(p.Seeds) {
		fmt.Fprintf(w, "- seed %s = %s\n", kv[0], kv[1])
	}
	for _, kv := range sortedKV(p.PromptHashes) {
		fmt.Fprintf(w, "- プロンプト %s = %s\n", kv[0], kv[1])
	}
	for _, kv := range sortedKV(p.ModelIDs) {
		fmt.Fprintf(w, "- モデル %s = %s\n", kv[0], kv[1])
	}
	for _, kv := range sortedKV(p.Extra) {
		fmt.Fprintf(w, "- %s = %s\n", kv[0], kv[1])
	}
}

func sortedKV[V any](m map[string]V) [][2]string {
	var out [][2]string
	for k, v := range m {
		out = append(out, [2]string{k, fmt.Sprint(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// ---------------------------------------------------------------------------------------------------------------
// Profile likelihood (§16 #11)
// ---------------------------------------------------------------------------------------------------------------

// writeProfiles writes the log-likelihood as each cost is varied one at a time on a 0.1 grid over [0, 3].
// slice fixes the other parameters at their estimates; profile refits the others with the bound-constrained
// optimizer from three starts (the estimate, θ₀, all-0.5) and keeps the best. Both re-solve the matching at
// each point. A profile with more than one local maximum (beyond 0.5 log-likelihood units) is flagged non-monotone.
func writeProfiles(path string, ft []fit.Triplet, js []fit.Judgment, opts fit.Options, est fit.Params) error {
	var b bytes.Buffer
	b.WriteString("param,value,slice_loglik,profile_loglik,assign_changes,flag\n")
	var half ged.Theta
	for i := range half {
		half[i] = 0.5
	}
	for p := range ged.NumParams {
		if _, tied := opts.Tie[p]; tied {
			continue
		}
		var prevSig []string
		type pt struct {
			v, slice, prof float64
			changes        int
		}
		var pts []pt
		for k := 0; k <= 30; k++ {
			v := float64(k) * 0.1
			sp := est
			sp.Theta[p] = v
			for i, m := range opts.Tie {
				sp.Theta[i] = sp.Theta[m]
			}
			nll, err := fit.NegLogLik(ft, js, sp)
			if err != nil {
				return err
			}
			po := opts
			po.Fixed = map[int]float64{p: v}
			po.Starts = []ged.Theta{est.Theta, opts.Theta0, half}
			po.StartNames = []string{"est", "θ₀", "all-0.5"}
			pr, err := fit.Fit(ft, js, po, &est)
			if err != nil {
				return err
			}
			sigs, err := fit.Signatures(ft, sp.Theta)
			if err != nil {
				return err
			}
			changes := 0
			if prevSig != nil {
				for i := range sigs {
					if sigs[i] != prevSig[i] {
						changes++
					}
				}
			}
			prevSig = sigs
			pts = append(pts, pt{v, -nll, -pr.NegLogLik, changes})
		}
		flag := ""
		if peaks(pts, func(q pt) float64 { return q.prof }, 0.5) > 1 {
			flag = "non-monotone"
		}
		for _, q := range pts {
			fmt.Fprintf(&b, "%s,%.2f,%.4f,%.4f,%d,%s\n", ged.ParamNames[p], q.v, q.slice, q.prof, q.changes, flag)
		}
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// peaks counts local maxima of a sequence that stand out by more than tol from the neighbouring valleys.
func peaks[T any](pts []T, f func(T) float64, tol float64) int {
	n := len(pts)
	if n < 3 {
		return 1
	}
	count := 0
	for i := range n {
		v := f(pts[i])
		// Walk left and right until the value departs by tol; it is a peak if both sides drop first.
		dropL, dropR := i == 0, i == n-1
		for l := i - 1; l >= 0; l-- {
			if f(pts[l]) > v+1e-9 {
				break
			}
			if f(pts[l]) < v-tol {
				dropL = true
				break
			}
		}
		for r := i + 1; r < n; r++ {
			if f(pts[r]) > v+1e-9 {
				break
			}
			if f(pts[r]) < v-tol {
				dropR = true
				break
			}
		}
		if dropL && dropR && (i == 0 || f(pts[i-1]) <= v) && (i == n-1 || f(pts[i+1]) <= v) {
			count++
		}
	}
	return max(count, 1)
}
