package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/simulate"
)

// Flags of the simulation only (registered here so that main.go stays unchanged).
var (
	simReps       = flag.Int("reps", 1, "合成: seed を変えて繰り返す回数（P1 の再実行は 20 以上）。seed, seed+1, … を使う")
	simFitReps    = flag.Int("fit-reps", -1, "合成: 推定まで行う繰り返しの数（-1: すべて。1 回目だけブートストラップと交差検証も行う）")
	simCountsOnly = flag.Bool("counts-only", false, "合成: 推定をせず、セルごとの情報を持つ三つ組の数だけを数える")
	simScenario   = flag.String("scenario", "reference", "合成: シナリオ reference | misspec（文脈ごとのコストのばらつき + 5% のラベル誤り）")
	simRandom     = flag.Float64("random", -1, "合成: 無作為に答える確率（負: シナリオの既定値）")
	simHetero     = flag.Float64("hetero", -1, "合成: 元の議論ごとのコストのばらつき（対数の SD。負: シナリオの既定値）")
	simLabelNoise = flag.Float64("label-noise", -1, "合成: ラベル誤りの確率（負: シナリオの既定値）")
	simShape      = flag.String("shape", simulate.ShapeSpec, "合成: 元の議論の形 spec（dataset-spec §1.2）| poc")
)

type simOptions struct {
	sources, boot int
	seed          uint64
	lambda, m     float64
	sigma         float64
	null          bool
	out           string
}

func simConfig(o simOptions) (simulate.Config, error) {
	var cfg simulate.Config
	switch *simScenario {
	case "reference":
		cfg = simulate.Default()
	case "misspec":
		cfg = simulate.Misspecified()
	default:
		return cfg, fmt.Errorf("unknown -scenario %q", *simScenario)
	}
	cfg.Sources, cfg.Seed, cfg.Sigma, cfg.Shape = o.sources, o.seed, o.sigma, *simShape
	if o.null {
		cfg.True = ged.Current() // a world where the current values are correct (checks whether "keep" can be concluded)
	}
	if *simRandom >= 0 {
		cfg.Random = *simRandom
	}
	if *simHetero >= 0 {
		cfg.Hetero = *simHetero
	}
	if *simLabelNoise >= 0 {
		cfg.LabelNoise = *simLabelNoise
	}
	return cfg, nil
}

// repFit is one replicate's fit for the across-replicate table.
type repFit struct {
	theta ged.Theta
	neg   float64
}

func runSimulate(o simOptions) error {
	cfg, err := simConfig(o)
	if err != nil {
		return err
	}
	if *simScenario != "reference" {
		o.out = strings.TrimSuffix(o.out, ".md") + "_" + *simScenario + ".md"
	}
	reps := max(1, *simReps)
	fitReps := reps
	if *simFitReps >= 0 {
		fitReps = min(reps, *simFitReps)
	}
	if *simCountsOnly {
		fitReps = 0
	}

	var b bytes.Buffer
	title := "推定はコストを取り戻せるか（θ* を現行値からずらした世界）"
	if o.null {
		title = "現行値が正しい世界で「維持」と判断できるか（θ* = θ₀）"
	}
	fmt.Fprintln(&b, "# シミュレーション（API 不使用）: "+title)
	fmt.Fprintf(&b, "\n- シナリオ %s、元の議論の形 %s、繰り返し %d 回（seed %d–%d）、うち推定 %d 回\n",
		*simScenario, cfg.Shape, reps, o.seed, o.seed+uint64(reps)-1, fitReps)
	fmt.Fprintf(&b, "- 1 回あたり三つ組 %d（元の議論 %d × %d）、判定者 %d × 提示順 2\n", cfg.Sources*cfg.PerSrc, cfg.Sources, cfg.PerSrc, len(cfg.Judges))
	fmt.Fprintf(&b, "- 共有のずれ σ = %.2f、無作為に答える確率 %.0f%%、文脈ごとのコストのばらつき %.2f、ラベル誤り %.0f%%\n",
		cfg.Sigma, 100*cfg.Random, cfg.Hetero, 100*cfg.LabelNoise)
	if reps < 20 {
		fmt.Fprintf(&b, "- 注意: P1 の再実行は 1 シナリオにつき 20 seed 以上（decisions-stats §16 #14）。この実行は %d seed\n", reps)
	}

	var counts []simulate.Counts
	var fits []repFit
	var body bytes.Buffer
	for r := range reps {
		c := cfg
		c.Seed = o.seed + uint64(r)
		trips, js, metas, err := simulate.Generate(c)
		if err != nil {
			return err
		}
		cnt, err := simulate.Count(trips, metas, ged.Current())
		if err != nil {
			return err
		}
		counts = append(counts, cnt)
		fmt.Fprintf(os.Stderr, "rep %d (seed %d): %d triplets (%d in the fit set), %d judgments\n", r+1, c.Seed, len(trips), cnt.FitSet, len(js))
		if r >= fitReps {
			continue
		}
		// The fit uses the default triplet filter (decisions-stats §16 #16): no X-spk, no diagnostic CA relabels.
		ft, fj, fm := simulate.Subset(trips, js, metas, simulate.Meta.InFit)
		if r == 0 {
			p, err := fullRecovery(&body, o, c, ft, fj, fm)
			if err != nil {
				return err
			}
			fits = append(fits, p)
			continue
		}
		opts := fit.Options{Judges: len(c.Judges), Theta0: ged.Current(), Lambda: o.lambda, FitNeg: true, MaxOuter: 20}
		res, err := fit.Fit(ft, fj, opts, nil)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "  fit: outer=%d converged=%v nll=%.1f\n", res.Outer, res.Converged, res.NegLogLik)
		fits = append(fits, repFit{res.Params.Theta, res.Params.ThetaNeg})
	}

	countsReport(&b, counts, cfg.Sources*cfg.PerSrc)
	if len(fits) > 1 {
		acrossReps(&b, cfg, fits)
	}
	b.Write(body.Bytes())
	if err := os.WriteFile(o.out, b.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Print(b.String())
	return nil
}

// fullRecovery is the single-data-set report (fit, bootstrap, CV, agreement, judge diagnostics) on the fit set.
func fullRecovery(b io.Writer, o simOptions, cfg simulate.Config, trips []fit.Triplet, js []fit.Judgment, metas []simulate.Meta) (repFit, error) {
	names := make([]string, len(cfg.Judges))
	truth := make([]judgeTruth, len(cfg.Judges))
	for j, jd := range cfg.Judges {
		names[j] = fmt.Sprintf("合成 %d", j+1)
		truth[j] = judgeTruth{jd.Beta, jd.Tau, jd.Pi}
	}
	opts := fit.Options{Judges: len(cfg.Judges), Theta0: ged.Current(), Lambda: o.lambda, FitNeg: true, MaxOuter: 20}
	res, err := fit.Fit(trips, js, opts, nil)
	if err != nil {
		return repFit{}, err
	}
	fmt.Fprintf(os.Stderr, "  fit: outer=%d converged=%v nll=%.1f\n", res.Outer, res.Converged, res.NegLogLik)
	boot, err := fit.Bootstrap(trips, js, opts, res.Params, o.boot, cfg.Seed+100)
	if err != nil {
		return repFit{}, err
	}
	fmt.Fprintln(os.Stderr, "  bootstrap done")
	cv, err := fit.CrossValidate(trips, js, opts, 5, cfg.Seed)
	if err != nil {
		return repFit{}, err
	}
	d0, err := fit.Delta(trips, ged.Current())
	if err != nil {
		return repFit{}, err
	}
	dEst, err := fit.Delta(trips, res.Params.Theta)
	if err != nil {
		return repFit{}, err
	}
	dTrue, err := fit.Delta(trips, cfg.True)
	if err != nil {
		return repFit{}, err
	}
	types := make([]perturb.TripletType, len(trips))
	sign := make([]int, len(trips))
	neg := make([]bool, len(trips))
	for i, m := range metas {
		types[i] = m.Type
		switch {
		case m.TrueDelta > 1e-9:
			sign[i] = 1
		case m.TrueDelta < -1e-9:
			sign[i] = -1
		}
		neg[i] = trips[i].NegDiff != 0
	}
	fmt.Fprintf(b, "\n# パラメータの回収（seed %d、推定に使う三つ組 %d、判定 %d）\n", cfg.Seed, len(trips), len(js))
	fmt.Fprintf(b, "\n- 推定から除いた三つ組: X-spk を含むもの、診断用の CA 種別変更を含むもの（decisions-stats §16 #16 の既定）\n")
	fmt.Fprintf(b, "- 正則化 λ = %.2f（θ₀ を中心）、交互最適化 %d 回（収束: %v、循環: %v）\n", o.lambda, res.Outer, res.Converged, res.Cycled)
	paramTable(b, ged.Current(), &cfg.True, &cfg.TrueNeg, res.Params, boot, o.m)
	auxTable(b, names, res.Params, truth)
	agreementTable(b, []agreeRow{
		{"R0 現行のコスト表", d0},
		{"R2 学習したコスト（全データ、学習に使ったもの）", dEst},
		{"R2 学習したコスト（5 分割交差検証）", cv},
		{"真のコスト θ*（極性反転は見えない）", dTrue},
	}, js, neg)
	judgeDiagnostics(b, names, types, sign, js)
	return repFit{res.Params.Theta, res.Params.ThetaNeg}, nil
}

// countsReport prints the realized per-cell counts over the replicates against dataset-spec §2.3 and decisions-stats §15.3.
func countsReport(w io.Writer, cs []simulate.Counts, n int) {
	scale := float64(n) / 3000
	fitN := make([]float64, len(cs))
	for i, c := range cs {
		fitN[i] = float64(c.FitSet)
	}
	fmt.Fprintln(w, "\n## セルごとの情報を持つ三つ組の数（dataset-spec §2.3、decisions-stats §15.3）")
	fmt.Fprintf(w, "\nΔφ_p ≠ 0（θ₀ の対応付け）の三つ組を数える。期待値と最低数は 3,000 三つ組の値を %d 三つ組に比例換算（×%.2f）。", n, scale)
	fmt.Fprintf(w, "範囲は %d 回の最小–最大。「fit」は推定に入る三つ組（平均 %.0f）、「all」はすべて、「draws」は編集の抽選数。\n", len(cs), mean(fitN))
	fmt.Fprintln(w, "判定: 全回で最低数以上 → OK、平均は最低数以上だが下回る回がある → thin、平均が最低数未満 → **SHORT**。")
	fmt.Fprintln(w, "「全三つ組」列は fit のセルを除外分（X-spk、診断用の CA 種別変更）も含めて数えた平均（§2.3 の Monte Carlo は除外をしていない）。")
	fmt.Fprintln(w, "\n| セル | 範囲 | 期待値 §2.3 | 最低数 §15.3 | 実現（平均） | 範囲 | 判定 | 全三つ組（平均） | 備考 |")
	fmt.Fprintln(w, "| --- | --- | --: | --: | --: | --- | --- | --: | --- |")
	for _, spec := range simulate.Cells {
		xs := make([]float64, len(cs))
		for i, c := range cs {
			xs[i] = float64(c.Cells[spec.Name])
		}
		lo, hi := slices.Min(xs), slices.Max(xs)
		exp, minimum := spec.Expected*scale, spec.Minimum*scale
		status := "—"
		if spec.Minimum > 0 {
			switch {
			case lo >= minimum:
				status = "OK"
			case mean(xs) >= minimum:
				status = "thin"
			default:
				status = "**SHORT**"
			}
		}
		if spec.Name == simulate.CellCooccur && hi > 0 {
			status = "**ERROR**"
		}
		all := "—"
		if spec.Scope == "fit" {
			ys := make([]float64, len(cs))
			for i, c := range cs {
				ys[i] = float64(c.AllCells[spec.Name])
			}
			all = fmt.Sprintf("%.0f", mean(ys))
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %.0f | %.0f–%.0f | %s | %s | %s |\n",
			spec.Name, spec.Scope, num0(exp), num0(minimum), mean(xs), lo, hi, status, all, spec.Note)
	}

	// edit-kind shares in T3 against the §2.2 weights
	fmt.Fprintln(w, "\n### T3 の編集の種類の実現割合（dataset-spec §2.2 の重みと比較）")
	fmt.Fprintln(w, "\n実行できない種類は引き直すため、実現割合は重みからずれうる。t3_ident 層の抽選も含む。")
	fmt.Fprintln(w, "\n| 種類 | 重み §2.2 | 実現割合 | 抽選数（平均） | 実行できず引き直した数（全型、平均） |")
	fmt.Fprintln(w, "| --- | --: | --: | --: | --: |")
	tot := 0.0
	for _, c := range cs {
		for _, v := range c.T3Kinds {
			tot += float64(v)
		}
	}
	for _, kw := range perturb.KindWeights() {
		k, inf := 0.0, 0.0
		for _, c := range cs {
			k += float64(c.T3Kinds[kw.Kind])
			inf += float64(c.Infeasible[kw.Kind])
		}
		fmt.Fprintf(w, "| %s | %.2f | %.3f | %.0f | %.0f |\n", kw.Kind, kw.W, k/tot, k/float64(len(cs)), inf/float64(len(cs)))
	}
	var add [3]float64
	for _, c := range cs {
		for k := range 3 {
			add[k] += float64(c.AddStrata[k])
		}
	}
	at := add[0] + add[1] + add[2]
	fmt.Fprintf(w, "\nR-add の発話距離の層（目標 0.45 / 0.45 / 0.10）: ≤1 %.3f、2–4 %.3f、5–8 %.3f（抽選 %.0f 件/回）\n",
		add[0]/at, add[1]/at, add[2]/at, at/float64(len(cs)))
	dirs := map[string]float64{}
	for _, c := range cs {
		for k, v := range c.TypeDirs {
			dirs[k] += float64(v)
		}
	}
	var parts []string
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range dirs {
			if !yield(k) {
				return
			}
		}
	}) {
		parts = append(parts, fmt.Sprintf("%s %.0f", k, dirs[k]/float64(len(cs))))
	}
	fmt.Fprintf(w, "\n種別変更の向きごとの抽選数（平均/回）: %s\n", strings.Join(parts, "、"))
}

// acrossReps summarizes the point estimates over the replicates.
func acrossReps(w io.Writer, cfg simulate.Config, fits []repFit) {
	fmt.Fprintf(w, "\n## 繰り返し %d 回の推定値（点推定、推定に使う三つ組のみ）\n", len(fits))
	fmt.Fprintln(w, "\n| パラメータ | 現行値 θ₀ | 真の値 θ* | 平均 | 回の間の SD | 偏り | RMSE |")
	fmt.Fprintln(w, "| --- | --: | --: | --: | --: | --: | --: |")
	th0 := ged.Current()
	for i := 0; i <= ged.NumParams; i++ {
		xs := make([]float64, len(fits))
		name, cur, tv := "θ_neg（極性反転）", math.NaN(), cfg.TrueNeg
		for k, f := range fits {
			if i < ged.NumParams {
				xs[k] = f.theta[i]
			} else {
				xs[k] = f.neg
			}
		}
		if i < ged.NumParams {
			name, cur, tv = ged.ParamNames[i], th0[i], cfg.True[i]
		}
		m := mean(xs)
		var ss, se float64
		for _, x := range xs {
			ss += (x - m) * (x - m)
			se += (x - tv) * (x - tv)
		}
		sd := math.Sqrt(ss / float64(max(1, len(xs)-1)))
		fmt.Fprintf(w, "| %s | %s | %.2f | %.3f | %.3f | %+.3f | %.3f |\n", name, num(cur), tv, m, sd, m-tv, math.Sqrt(se/float64(len(xs))))
	}
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func num0(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f", v)
}
