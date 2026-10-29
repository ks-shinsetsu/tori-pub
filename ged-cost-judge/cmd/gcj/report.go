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
// Confirmatory report (decisions-stats §2–§4, §6, §7, §9, §9.1, §12b, §16 #5, #10, #13, #19)
//
// WriteConfirmatory formats a fit.ConfirmResult. It is the only place that prints §3 labels, and
// fit.Confirmatory is the only function that produces them. simulate.go and pilot.go must call
// fit.Confirmatory + WriteConfirmatory for any decision output; paramTable prints intervals only.
// ---------------------------------------------------------------------------------------------------------------

// WriteConfirmatory writes the complete confirmatory report.
func WriteConfirmatory(w io.Writer, r fit.ConfirmResult, prov provenance) {
	co := r.Options
	fmt.Fprintln(w, "\n# 確認的分析（決定文書 §2–§7、`fit.Confirmatory` の出力）")
	fmt.Fprintln(w, "\nこの節の判断は `fit.Confirmatory` だけが作る。`simulate` / `pilot` の他の表は参考値であり、§3 の規則ではない。")
	writeProvenance(w, prov)

	// Judge QC first (it ran before θ).
	writeJudgeQC(w, r.QC, co)
	if len(r.ExcludedJudges) > 0 {
		fmt.Fprintf(w, "\n**除外した判定者: %s。θ は残りの判定者で当てはめた（§9）。**\n", strings.Join(r.ExcludedJudges, "、"))
	}

	// Filters.
	fmt.Fprintln(w, "\n## 推定集合（決定文書 #15、§12b、§13）")
	fmt.Fprintf(w, "\n三つ組 %d、判定 %d（判定者 %s）。", r.Triplets, r.Judgments, strings.Join(r.JudgeNames, "、"))
	if r.JudgmentsDropped > 0 {
		fmt.Fprintf(w, " 判定者の系列 = 検証者の系列で除いた判定 %d。", r.JudgmentsDropped)
	}
	fmt.Fprintln(w, "\n\n除外して数えた三つ組:")
	fmt.Fprintln(w, "\n| フラグ | 除外数 |")
	fmt.Fprintln(w, "| --- | --: |")
	for _, kv := range sortedKV(r.Exclusions) {
		fmt.Fprintf(w, "| %s | %s |\n", kv[0], kv[1])
	}

	// Information.
	fmt.Fprintln(w, "\n## 情報量（Δφ_p ≠ 0 の三つ組数、θ₀ で、決定文書 §15.3）")
	fmt.Fprintln(w, "\n| パラメータ | n_p | 下限 |")
	fmt.Fprintln(w, "| --- | --: | --: |")
	for p := 0; p <= fit.NormEdgeExtra; p++ {
		if isTied(r, p) {
			continue
		}
		fmt.Fprintf(w, "| %s | %d | %s |\n", fit.NormName(p), r.Informative[p], minCell(co.MinInformative, p))
	}
	fmt.Fprintf(w, "| θ_neg | %d | %s |\n", r.NegInformative, minCell(co.MinInformative, fit.NormNeg))

	// Fits.
	fmt.Fprintln(w, "\n## 当てはめ（決定文書 §11）")
	fmt.Fprintf(w, "\n- 作業独立: 交互最適化 %d 回（収束 %v、循環 %v、上限 %v、内側の収束 %v）、NLL = %.2f、多点開始 %d 点の最大のずれ %.3f%s\n",
		r.Indep.Outer, r.Indep.Converged, r.Indep.Cycled, r.Indep.Capped, r.Indep.InnerConverged, r.Indep.NegLogLik, len(r.Indep.Starts), r.Indep.StartSpread, spreadFlag(r.Indep.StartSpread))
	for _, st := range r.Indep.Starts {
		fmt.Fprintf(w, "  - %s: 目的関数 %.2f（収束 %v、循環 %v）\n", st.Name, st.Objective, st.Converged, st.Cycled)
	}
	if r.Mixed.Params.Beta != nil {
		fmt.Fprintf(w, "- ランダム切片（適応ガウス–エルミート %d 点）: %d 回（収束 %v、循環 %v）、NLL = %.2f、σ̂ = %.3f、多点開始の最大のずれ %.3f%s\n",
			defaultI(co.HighNodes/4, 20), r.Mixed.Outer, r.Mixed.Converged, r.Mixed.Cycled, r.Mixed.NegLogLik, r.Mixed.Params.Sigma, r.Mixed.StartSpread, spreadFlag(r.Mixed.StartSpread))
		if r.MixedCheck != nil {
			fmt.Fprintf(w, "  - σ̂ > %.1f なので %d 点で再当てはめ: θ の最大の変化 %.4f、σ̂ の変化 %+.4f\n", co.HighNodesSigma, co.HighNodes, r.MixedCheckMaxDiff, r.MixedCheckSigmaDiff)
		}
	} else {
		fmt.Fprintln(w, "- **ランダム切片の当てはめは省略（共主要の一致が取れないので「妥当」にはならない）**")
	}
	fmt.Fprintf(w, "- 対応付けの安定性: θ̂ ± 3 SD の箱（軸上 22 点 + 無作為な角 64 点）で対応付けが変わる三つ組 %.1f%%（§11.6）\n", 100*r.Stability)

	// Decision table.
	fmt.Fprintln(w, "\n## パラメータの判断（決定文書 §2–§3、和で正規化した尺度）")
	fmt.Fprintf(w, "\n判断の幅 m = ±%.2f。尺度は U = {%s} の 6 費用の和が現行表の和（%.2f）に一致するよう正規化した θ̃ = θ̂ / ŝ（辺の余分は 1/ŝ）。ŝ = %.3f（90%% 区間 [%.3f, %.3f]）。",
		co.M, uNames(co.U), sumU(co.Theta0, co.U), r.SHat, r.SHatCI[0], r.SHatCI[1])
	fmt.Fprintf(w, " 区間は生成モデルで層化したクラスタブートストラップ B = %d（失敗 %d）のパーセンタイル。SD = (84%% 点 − 16%% 点)/2。\n", co.B, r.Boot.Failed)
	fmt.Fprintln(w, "\n規則: 90% 区間が [θ₀ − m, θ₀ + m] に入れば「維持」（TOST）、全体が外なら「変更」、「推定不能」は n_p が下限未満・固定尺度の反復の IQR < 1e-6・有効な反復 < 95%・境界率 > 50% のいずれか、それ以外は「未決」。境界率 10–50% は片側 95% 上限のみ（「変更」か「未決（精度不足）」）。共主要の 2 つの当てはめのラベルが一致したときだけ判断が立つ。U の費用が 1 つでも推定不能なら ŝ が同定されず、正規化した判断はすべて「推定不能」。")
	if !r.ScaleIdentified {
		fmt.Fprintln(w, "\n**正規化の尺度が同定できない（U の費用に推定不能がある）。以下のラベルはすべて「推定不能」。固定尺度の表を参照。**")
	}
	head := "| パラメータ | θ₀ |"
	sep := "| --- | --: |"
	if co.Truth != nil {
		head += " 真の値（正規化） |"
		sep += " --: |"
	}
	head += " θ̃ | SD | 90% 区間 | 95% 区間 | n_p | 境界率 | 判断（独立）"
	sep += " --: | --: | --- | --- | --: | --: | ---"
	if r.Mixed.Params.Beta != nil {
		head += " | θ̃（切片） | 90%（切片） | 判断（切片） | **判断**"
		sep += " | --: | --- | --- | ---"
	}
	fmt.Fprintln(w, "\n"+head+" |")
	fmt.Fprintln(w, sep+" |")
	for _, p := range r.Params {
		d := p.Indep
		label := d.Label.JA()
		if !p.Margin {
			label = "—（区間のみ）"
		}
		line := fmt.Sprintf("| %s | %s |", p.Name, num(p.Theta0))
		if co.Truth != nil {
			line += fmt.Sprintf(" %s |", num(p.Truth))
		}
		line += fmt.Sprintf(" %.2f | %.3f | %s | %s | %s | %.0f%% | %s", d.Est, d.SD, intervalCell(d), ci(d.CI95), nCell(d), 100*d.BoundaryShare, label)
		if p.Mixed != nil {
			m := *p.Mixed
			ml, cl := m.Label.JA(), p.Combined.JA()
			if !p.Margin {
				ml, cl = "—", "—"
			}
			line += fmt.Sprintf(" | %.2f | %s | %s | **%s** |", m.Est, intervalCell(m), ml, cl)
		} else {
			line += " |"
		}
		fmt.Fprintln(w, line)
	}
	if len(r.Extrapolated) > 0 {
		fmt.Fprintln(w, "\n判断なしの行（決定文書 §12b）:")
		fmt.Fprintln(w, "\n| 種別の組 | 扱い |")
		fmt.Fprintln(w, "| --- | --- |")
		for _, e := range r.Extrapolated {
			fmt.Fprintf(w, "| %s | **%s から外挿、較正なし** |\n", ged.ParamNames[e.Index], ged.ParamNames[e.Master])
		}
		fmt.Fprintln(w, "\n使途の 81 件の種別誤りのうち 14 件（17%）は CA を含み、仮定で値を置いている。診断セルの自由推定（§12b）は別の感度分析として報告する。")
	}

	// Anchored table and bootstrap diagnostics.
	fmt.Fprintln(w, "\n### 固定尺度（辺の余分 = 1）の推定値とブートストラップの診断")
	fmt.Fprintln(w, "\n| パラメータ | 固定尺度の推定値 | 偏り（正規化） | 偏りの警告 | 境界率 | 発散率 | IQR | 有効な反復 | ウォーム 5–95% | コールド 5–95% |")
	fmt.Fprintln(w, "| --- | --: | --: | --- | --: | --: | --: | --: | --- | --- |")
	for _, p := range r.Params {
		d := p.Indep
		flag := ""
		if d.BiasFlag {
			flag = "|偏り| > 0.5 SD"
		}
		fmt.Fprintf(w, "| %s | %.3f | %+.3f | %s | %.1f%% | %.1f%% | %.2e | %d/%d | [%s, %s] | [%s, %s] |\n", p.Name, p.Anchored, d.Bias, flag,
			100*d.BoundaryShare, 100*d.DivergentShare, d.IQR, d.Reps, d.TotalReps, num(p.WarmCI[0]), num(p.WarmCI[1]), num(p.ColdCI[0]), num(p.ColdCI[1]))
	}
	sh := fit.Summarise(r.Boot.Meta)
	fmt.Fprintf(w, "\n反復の内訳（作業独立）: 循環 %.1f%%、上限到達 %.1f%%、非収束 %.1f%%、コールドスタート %d 回（θ₀ と全 0.5 から、ウォームの推定値は使わない）。", 100*sh.Cycled, 100*sh.Capped, 100*sh.NotConverged, sh.Cold)
	if r.Boot.MixedMeta != nil {
		shm := fit.Summarise(r.Boot.MixedMeta)
		fmt.Fprintf(w, " ランダム切片: 循環 %.1f%%、上限到達 %.1f%%、非収束 %.1f%%。", 100*shm.Cycled, 100*shm.Capped, 100*shm.NotConverged)
	}
	fmt.Fprintln(w)
	for _, p := range r.Params {
		if p.Indep.DivergentShare > stats.DivergentReport {
			fmt.Fprintf(w, "- %s: 発散した反復が %.1f%% > 2%%（発散を除いた区間は固定尺度の列から再計算すること）\n", p.Name, 100*p.Indep.DivergentShare)
		}
	}

	// Hypotheses.
	fmt.Fprintln(w, "\n## 確認的仮説（決定文書 §4、正規化した尺度、ブートストラップ対比）")
	fmt.Fprintf(w, "\n帰無値を検定ごとに明示。片側 p は帰無値の反対側にある反復の割合、両側は 2 倍。族は Holm で α = 0.05（2 つの当てはめのうち大きい p を使う）。「支持」は両方の当てはめで支持されたときだけ。H1・H2 は判定には無調整の α で入る（交差–和集合検定）。H3 は計画 v4 からの逸脱として取り下げ。C 族は Δ = 0 を ½ 点とするクラスタ版 McNemar、正確 McNemar は両規則で Δ ≠ 0 の三つ組のみ。B = %d。\n", co.B)
	fmt.Fprintln(w, "\n| ID | 対比 | 帰無値 | 推定値 | 95% 区間 | p | p（Holm） | 支持 | 備考 |")
	fmt.Fprintln(w, "| --- | --- | --: | --: | --- | --: | --: | --- | --- |")
	for _, h := range r.Hypotheses {
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
		note := ""
		if h.Eligible > 0 {
			note = fmt.Sprintf("不一致 %d / %d、正確 p = %.3f、Δ = 0 で除外 %d / %d", h.Discordant[0], h.Discordant[1], h.ExactP, h.ZeroDropped, h.Eligible)
		}
		fmt.Fprintf(w, "| %s | %s | %.2f | %s | %s | %.3f | %.3f | %s | %s |\n", h.ID, h.Name, h.Indep.Null, cell, ciCell, h.Indep.P, h.PHolm, sup, note)
	}
	fmt.Fprintln(w, "\nH4 は非対称性の主張であり表の妥当性の主張ではない: 「H4b 支持」と「辺の欠落: 維持」は両立しうる。")

	// D and verdict.
	fmt.Fprintln(w, "\n## 現行表の判定（決定文書 §3・§7）")
	if r.D != nil {
		lo, hi := r.D.DBounds()
		fmt.Fprintf(w, "\n- D = agree(R2, OOF) − agree(R0) = %.2f pt（CV %d 回の平均、対象 %d 組）、片側 95%% 下限 %.2f、上限 %.2f（元の議論のネストしたブートストラップ %d 回、各反復で 5 分割 CV を再学習）。妥当の条件は上限 < 2 pt、否定は下限 ≥ 2 pt。対数損失の形: %.4f nats/判定（正なら R2 が良い）。両方とも推定集合（三つ組 %d、判定 %d）で計算。\n",
			r.D.Agree, co.DRepeats, r.D.N, lo, hi, len(r.D.AgreeBoot), r.D.LogLoss, r.D.Triplets, r.D.Judgments)
		if len(r.D.Strata) > 0 {
			fmt.Fprintln(w, "\n| 層 | D（pt） | 片側 95% 下限 / 上限 |")
			fmt.Fprintln(w, "| --- | --: | --- |")
			keys := make([]string, 0, len(r.D.Strata))
			for k := range r.D.Strata {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				col := sortedClean(r.D.StrataBoot[k])
				fmt.Fprintf(w, "| %s | %s | %s / %s |\n", k, num(r.D.Strata[k]), num(stats.Quantile(col, 0.05)), num(stats.Quantile(col, 0.95)))
			}
		}
	} else {
		fmt.Fprintln(w, "\n- **D は省略（「妥当」にはならない）**")
	}
	fmt.Fprintf(w, "\n**判定: %s（%s）**\n", r.Verdict.JA(), r.Verdict)
	if len(r.Missing) > 0 {
		fmt.Fprintln(w, "\n欠けている要素:")
		for _, m := range r.Missing {
			fmt.Fprintf(w, "- %s\n", m)
		}
	}
	fmt.Fprintln(w, "\n「否定されず」は現行表を続ける許可ではない: データが決めなかったという意味。")

	// LOJO.
	if len(r.LOJO) > 0 {
		fmt.Fprintln(w, "\n## 判定者を 1 人ずつ除いた判断（決定文書 §9）")
		head := "| パラメータ | 全員 |"
		sep := "| --- | --- |"
		for _, l := range r.LOJO {
			head += fmt.Sprintf(" %s を除く |", l.Judge)
			sep += " --- |"
		}
		fmt.Fprintln(w, "\n"+head)
		fmt.Fprintln(w, sep)
		changed := 0
		for i, p := range r.Params {
			if !p.Margin {
				continue
			}
			line := fmt.Sprintf("| %s | %s |", p.Name, p.Combined.JA())
			for _, l := range r.LOJO {
				if i >= len(l.Params) {
					line += " — |"
					continue
				}
				cell := l.Params[i].Combined.JA()
				if l.Params[i].Combined != p.Combined {
					cell = "**" + cell + "**"
					changed++
				}
				line += fmt.Sprintf(" %s（%.2f） |", cell, l.Params[i].Indep.Est)
			}
			fmt.Fprintln(w, line)
		}
		fmt.Fprintf(w, "\n太字 = 全員での判断と異なる（%d 箇所、各 B = %d）。\n", changed, co.LOJOReps)
	}
}

func isTied(r fit.ConfirmResult, p int) bool {
	for _, e := range r.Extrapolated {
		if e.Index == p {
			return true
		}
	}
	return false
}

func minCell(m map[int]int, p int) string {
	if v, ok := m[p]; ok && v > 0 {
		return fmt.Sprintf("≥ %d", v)
	}
	return "—"
}

func nCell(d stats.ParamDecision) string {
	if d.N == 0 && d.MinN == 0 {
		return "—"
	}
	s := fmt.Sprintf("%d", d.N)
	if d.MinN > 0 {
		s += fmt.Sprintf(" (≥%d)", d.MinN)
	}
	return s
}

func spreadFlag(s float64) string {
	if s > 0.05 {
		return "（**0.05 を超える: 開始点で結果が変わる**）"
	}
	return ""
}

func defaultI(v, d int) int {
	if v <= 0 {
		return d
	}
	return v
}

func ci(iv stats.Interval) string { return fmt.Sprintf("[%.2f, %.2f]", iv.Lo, iv.Hi) }

func intervalCell(d stats.ParamDecision) string {
	if d.OneSided {
		return fmt.Sprintf("片側 95%% 上限 %.2f", d.Upper)
	}
	return ci(d.CI90)
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

// writeJudgeQC formats the mechanical judge QC of fit.JudgeQCStats.
func writeJudgeQC(w io.Writer, q fit.JudgeQCResult, co fit.ConfirmOptions) {
	fmt.Fprintln(w, "\n## 判定者の品質管理（決定文書 §9・§9.1、θ の当てはめ前に機械的に評価）")
	fmt.Fprintf(w, "\nT1 反転率 r_j = 「上位集合 B が近い」と答えた判定 / ゲート三つ組（Δ(θ₀) ≥ %.1f、n_T = %d、Δ(θ₀) ≤ 0 で除外 %d）での判定。「同程度」は分母のみ。二項の参照は k_eff = r_j·n_T、n_eff = n_T。片側 %.0f%% Clopper–Pearson 上限が r_max = %.2f を超えると不合格。機械的な基準: ゲート、同程度の割合 ≤ %.0f%%、他の各判定者との Krippendorff α ≥ %.1f（評価不能は数えない）。提示順の一致は記述のみ（§9 改訂: 参照判定者の値が 0.50–0.60 で、80%% の基準は想定する判定者をすべて除外する）。\n",
		co.GateDelta, q.NT, q.GateExcluded, 100*co.GateConf, co.RMax, 100*q.MaxTieShare, q.MinAlpha)
	fmt.Fprintln(w, "\n| 判定者 | 判定数 | 同程度の割合 | 提示順の一致（全体） | 提示順の一致（ゲート層） | ゲート判定数 | 反転 | r_j | 上限 | 除外？（理由） |")
	fmt.Fprintln(w, "| --- | --: | --: | --: | --: | --: | --: | --: | --: | --- |")
	for j, name := range q.Names {
		g := q.Gates[j]
		verdict := "いいえ"
		if q.Excluded[j] {
			verdict = "**はい**（" + q.Reasons[j] + "）"
		}
		fmt.Fprintf(w, "| %s | %d | %.1f%% | %s | %s | %d | %d | %s | %s | %s |\n", name, q.Judgments[j], 100*q.TieShare[j],
			ratioF(q.OrderConsistency[j], int(q.OrderN[j])), ratioF(q.GateOrderConsistency[j], int(q.GateOrderN[j])), g.Judgments, g.Reversals, num(g.R), num(g.Upper), verdict)
	}
	if len(q.Strata) > 0 {
		fmt.Fprintln(w, "\n反転率の Δ(θ₀) 層別（記述）:")
		head := "| 判定者 |"
		sep := "| --- |"
		for _, s := range q.Strata {
			head += " " + s + " |"
			sep += " --- |"
		}
		fmt.Fprintln(w, "\n"+head)
		fmt.Fprintln(w, sep)
		for j, name := range q.Names {
			line := "| " + name + " |"
			for _, s := range q.Strata {
				c := q.ByStratum[j][s]
				if c.Judgments == 0 {
					line += " — |"
				} else {
					line += fmt.Sprintf(" %.1f%%（%d/%d） |", pct(c.Reversals, c.Judgments), c.Reversals, c.Judgments)
				}
			}
			fmt.Fprintln(w, line)
		}
	}
	if len(q.Names) >= 2 {
		fmt.Fprintln(w, "\nKrippendorff の α（名義、単位 = 三つ組 × 提示順）:")
		fmt.Fprintln(w, "\n| 判定者 | "+strings.Join(q.Names, " | ")+" |")
		fmt.Fprintln(w, "| --- |"+strings.Repeat(" --: |", len(q.Names)))
		for a := range q.Names {
			line := "| " + q.Names[a] + " |"
			for b := range q.Names {
				if a == b {
					line += " — |"
				} else {
					line += fmt.Sprintf(" %s |", num3(q.Alpha[a][b]))
				}
			}
			fmt.Fprintln(w, line)
		}
	}
	fmt.Fprintln(w, "\n編集種別ごとの反転率は三つ組の記録（dataset-spec §6）から別途集計する（fit.Triplet は編集種別を持たない）。")
}

func num3(v float64) string {
	if math.IsNaN(v) {
		return "評価不能"
	}
	return fmt.Sprintf("%.3f", v)
}

func ratioF(v float64, n int) string {
	if n == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%（n=%d）", 100*v, n)
}

// ---------------------------------------------------------------------------------------------------------------
// Reference tables (not the §3 rule): kept for the simulation and the pilot
// ---------------------------------------------------------------------------------------------------------------

// paramTable prints the anchored and normalized estimates with percentile intervals and no decision labels.
// It is NOT the §3 rule: the information floor, the boundary rule, the replicate floor and the model agreement
// are applied only by fit.Confirmatory / WriteConfirmatory.
func paramTable(w io.Writer, th0 ged.Theta, truth *ged.Theta, trueNeg *float64, est fit.Params, boot [][]float64, m float64) {
	fmt.Fprintln(w, "\n## パラメータの推定（参考: 区間のみ。§3 の判断は `fit.Confirmatory` の報告だけが出す）")
	fmt.Fprintf(w, "\n判断の幅 m = ±%.2f は参考として帯を示すだけ。区間はクラスタブートストラップ %d 回のパーセンタイル、SD = (84%% 点 − 16%% 点)/2。正規化は U = {%s}。\n", m, len(boot), uNames(nil))
	estN, sHat := fit.NormalizeParams(est, nil)
	bootN, _ := fit.NormalizeRows(boot, nil)
	th0x := fit.Theta0Extended(th0)
	var truthN []float64
	if truth != nil {
		v := append(slices.Clone(truth[:]), 0)
		if trueNeg != nil {
			v[ged.NumParams] = *trueNeg
		}
		truthN, _ = fit.Normalize(v, nil)
	}
	fmt.Fprintf(w, "\nŝ = %.3f\n", sHat)
	head := "| パラメータ | θ₀ |"
	sep := "| --- | --: |"
	if truth != nil {
		head += " 真の値（正規化） |"
		sep += " --: |"
	}
	fmt.Fprintln(w, "\n"+head+" 固定尺度 θ̂ | θ̃ | SD | 90% 区間 | 95% 区間 | 帯 [θ₀−m, θ₀+m] に入るか（参考） |")
	fmt.Fprintln(w, sep+" --: | --: | --: | --- | --- | --- |")
	for i := range fit.NormLen {
		col := fit.Column(bootN, i)
		anchored := math.NaN()
		switch {
		case i < ged.NumParams:
			anchored = est.Theta[i]
		case i == fit.NormEdgeExtra:
			anchored = ged.EdgeExtraCost
		default:
			anchored = est.ThetaNeg
		}
		lo90, hi90 := stats.Quantile(col, 0.05), stats.Quantile(col, 0.95)
		lo95, hi95 := stats.Quantile(col, 0.025), stats.Quantile(col, 0.975)
		sd := (stats.Quantile(col, 0.8413) - stats.Quantile(col, 0.1587)) / 2
		band := "—"
		if slices.Contains(fit.MarginJudged, i) && len(col) > 0 {
			if lo90 >= th0x[i]-m && hi90 <= th0x[i]+m {
				band = "入る（参考）"
			} else {
				band = "入らない（参考）"
			}
		}
		line := fmt.Sprintf("| %s | %s |", fit.NormName(i), num(th0x[i]))
		if truthN != nil {
			line += fmt.Sprintf(" %.2f |", truthN[i])
		}
		fmt.Fprintf(w, "%s %.3f | %.2f | %.3f | [%.2f, %.2f] | [%.2f, %.2f] | %s |\n", line, anchored, estN[i], sd, lo90, hi90, lo95, hi95, band)
	}
}

// judgeDiagnostics keeps the first-version entry point (pilot and simulation). The T1 column is the reversal rate
// over all T1 triplets with a known correct side (all Δ strata), which is NOT the §9.1 gate statistic; the gate is
// computed by fit.JudgeQCStats on the Δ(θ₀) ≥ 2 stratum.
func judgeDiagnostics(w io.Writer, names []string, types []perturb.TripletType, trueSign []int, js []fit.Judgment) {
	fmt.Fprintln(w, "\n## 判定者の診断（参考）")
	fmt.Fprintln(w, "\n| 判定者 | 判定数 | 同程度の割合 | 提示順を入れ替えても同じ | T1 反転率（全層、ゲートではない） |")
	fmt.Fprintln(w, "| --- | --: | --: | --: | --: |")
	for j, name := range names {
		var n, tie int
		byT := map[int][]fit.Answer{}
		t1n, t1rev := 0, 0
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
				wrong := fit.AnswerB
				if trueSign[k.Triplet] < 0 {
					wrong = fit.AnswerA
				}
				if k.Answer == wrong {
					t1rev++
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
		fmt.Fprintf(w, "| %s | %d | %.1f%% | %s | %s |\n", name, n, pct(tie, n), ratio(same, pairs), ratio(t1rev, t1n))
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
	fmt.Fprintln(w, "\nΔ の符号が判定の多数（A / B、同数は除外）と一致する割合。多数が「同程度」の三つ組は除く。Δ = 0 は ½ 点。")
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
	// Aux is the auxiliary fit at θ₀ (FixTheta, working independence); Mixed its random-intercept counterpart
	// (β_j, τ_j, π_j, σ̂), which the simulated gate pass probabilities use (review-fit-impl M5).
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
	// QC options (zero → defaults).
	QC fit.QCOptions
}

// writeP2Report prints aux parameters, σ̂, φ coverage, QC, simulated T1 pass probabilities, discard rates,
// ICC by exemplar pair and domain, and the matching-stability share, with no θ estimate.
func writeP2Report(w io.Writer, in p2Input) error {
	fmt.Fprintln(w, "\n# P2 報告（θ の推定なし、決定文書 §13 項目 2）")
	auxTable(w, in.Names, in.Aux, nil)
	if in.Mixed != nil {
		fmt.Fprintf(w, "\nランダム切片の当てはめ（θ = θ₀ 固定）: σ̂ = %.3f、β = %s、τ = %s、π = %s\n", in.Mixed.Sigma, fmtSlice(in.Mixed.Beta), fmtSlice(in.Mixed.Tau), fmtSlice(in.Mixed.Pi))
	}
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
		fmt.Fprintf(w, "| %s | %d | %.0f | %s |\n", fit.NormName(p), nInf[p], scaled, minCell(in.MinInf, p))
	}
	negN := 0
	for _, t := range in.Trips {
		if t.NegDiff != 0 {
			negN++
		}
	}
	fmt.Fprintf(w, "| θ_neg | %d | %.0f | %s |\n", negN, float64(negN)*3000/math.Max(1, float64(len(in.Trips))), minCell(in.MinInf, fit.NormNeg))
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
	qc := fit.JudgeQCStats(in.Trips, in.Js, in.Names, in.QC)
	writeJudgeQC(w, qc, fit.ConfirmOptions{GateDelta: defaultF(in.QC.GateDelta, 2), RMax: defaultF(in.QC.RMax, stats.GateRMax), GateConf: defaultF(in.QC.Conf, stats.GateConf)})
	// Simulated pass probabilities from the random-intercept fit over the realized gate Δ(θ₀) (M5).
	nT := in.GateTriplets
	if nT <= 0 {
		nT = 100
	}
	var deltas []float64
	gd := defaultF(in.QC.GateDelta, 2)
	for _, t := range in.Trips {
		if (t.Type == "T1" || t.HasFlag("t1_gate")) && t.T1Delta0 >= gd {
			deltas = append(deltas, t.T1Delta0)
		}
	}
	gridNote := "実現した Δ(θ₀) ≥ 2 の分布"
	if len(deltas) == 0 {
		deltas = []float64{2, 2.5, 3, 4}
		gridNote = "ゲート三つ組がないので格子 {2, 2.5, 3, 4}"
	}
	fmt.Fprintln(w, "\n## §9.1 のゲートの合格確率（ランダム切片の当てはめからの模擬）")
	if in.Mixed == nil {
		fmt.Fprintln(w, "\nランダム切片の当てはめがないので計算しない（作業独立の β は減衰しているので σ̂ と組み合わせられない）。")
	} else {
		fmt.Fprintf(w, "\nz = β_j·Δ + π_j·o + u、u ~ N(0, σ̂²)、σ̂ = %.2f、Δ は%s（%d 個）。Rao–Kupper には無作為回答のパラメータがないので、無作為回答率 0 と 0.10（反転率に (1−ℓ)·r + ℓ/3）の 2 行。n_T = %d。\n", in.Mixed.Sigma, gridNote, len(deltas), nT)
		fmt.Fprintln(w, "\n| 判定者 | 反転率（ℓ = 0） | 合格確率 | 反転率（ℓ = 0.10） | 合格確率 |")
		fmt.Fprintln(w, "| --- | --: | --: | --: | --: |")
		for j, name := range in.Names {
			r0 := fit.SimulatedReversal(in.Mixed.Beta[j], in.Mixed.Tau[j], in.Mixed.Pi[j], in.Mixed.Sigma, 0, deltas)
			r1 := fit.SimulatedReversal(in.Mixed.Beta[j], in.Mixed.Tau[j], in.Mixed.Pi[j], in.Mixed.Sigma, 0.10, deltas)
			fmt.Fprintf(w, "| %s | %.3f | %.3f | %.3f | %.3f |\n", name, r0, stats.GatePassProbability(r0, nT, 0, 0), r1, stats.GatePassProbability(r1, nT, 0, 0))
		}
	}
	if len(in.Discards) > 0 {
		fmt.Fprintln(w, "\n## 破棄率")
		fmt.Fprintln(w, "\n| "+strings.Join(in.Discards[0], " | ")+" |")
		fmt.Fprintln(w, "|"+strings.Repeat(" --- |", len(in.Discards[0])))
		for _, r := range in.Discards[1:] {
			fmt.Fprintln(w, "| "+strings.Join(r, " | ")+" |")
		}
	}
	// ICC on source-level mean residuals (review-fit-impl m14).
	resid, err := scoreResiduals(in.Trips, in.Js, in.Aux)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "\n## 残差の級内相関（決定文書 §6; 元の議論ごとに平均した残差の一元配置 ICC(1)。0.05 を超えればその水準のブートストラップを感度分析に加える）")
	fmt.Fprintln(w, "\n| 水準 | 群数 | ICC(1) |")
	fmt.Fprintln(w, "| --- | --: | --: |")
	type srcAgg struct {
		sum float64
		n   int
		t   fit.Triplet
	}
	bySrc := map[int]*srcAgg{}
	for i, r := range resid {
		if math.IsNaN(r) {
			continue
		}
		a := bySrc[in.Trips[i].Source]
		if a == nil {
			a = &srcAgg{t: in.Trips[i]}
			bySrc[in.Trips[i].Source] = a
		}
		a.sum += r
		a.n++
	}
	for _, lv := range []struct {
		name string
		key  func(fit.Triplet) string
	}{{"例示の組", func(t fit.Triplet) string { return t.ExemplarPair }}, {"領域", func(t fit.Triplet) string { return t.Domain }}, {"生成モデル", func(t fit.Triplet) string { return t.Generator }}} {
		groups := map[string][]float64{}
		for _, a := range bySrc {
			k := lv.key(a.t)
			if k == "" || a.n == 0 {
				continue
			}
			groups[k] = append(groups[k], a.sum/float64(a.n))
		}
		fmt.Fprintf(w, "| %s | %d | %s |\n", lv.name, len(groups), num(icc1(groups)))
	}
	hw := in.StabilityHalfWidth
	if hw <= 0 {
		hw = 0.25
	}
	var box [ged.NumParams]float64
	for p := range box {
		box[p] = hw
	}
	share, err := fit.MatchingStability(in.Trips, ged.Current(), box, 64, 1)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\n## 対応付けの安定性（決定文書 §11.6）\n\nθ₀ ± %.2f の箱（軸上 22 点 + 無作為な角 64 点）で対応付けが変わる三つ組の割合: %.1f%%。\n", hw, 100*share)
	return nil
}

func defaultF(v, d float64) float64 {
	if v <= 0 {
		return d
	}
	return v
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
	n0 := (float64(N) - sumN2/float64(N)) / float64(k-1)
	msb := ssb / float64(k-1)
	msw := ssw / float64(N-k)
	return (msb - msw) / (msb + (n0-1)*msw)
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

// writeProfiles writes the log-likelihood as each cost is varied one at a time on a 0.1 grid over [0, max(3, θ̂_p + 1)].
// The filters of opts are applied once; slice fixes the other parameters at their estimates; profile refits the others
// with the bound-constrained optimizer from three starts (the estimate, θ₀, all-0.5) and keeps the best. Both
// re-solve the matching at each point. A profile with more than one local maximum (beyond 0.5 log-likelihood units)
// is flagged non-monotone.
func writeProfiles(path string, ft []fit.Triplet, js []fit.Judgment, opts fit.Options, est fit.Params) error {
	var b bytes.Buffer
	b.WriteString("param,value,slice_loglik,profile_loglik,assign_changes,flag\n")
	ft, js, _ = fit.Filter(ft, js, opts)
	opts.IncludeAll, opts.Exclude, opts.ExcludeSameFamily, opts.Weights, opts.CohZeroOnly = true, nil, false, nil, false
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
		top := math.Max(3, est.Theta[p]+1)
		for v := 0.0; v <= top+1e-9; v += 0.1 {
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
