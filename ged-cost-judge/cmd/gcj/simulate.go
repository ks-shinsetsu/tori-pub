package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/fit"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/ged"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/perturb"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/simulate"
	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/stats"
)

// Flags of the simulation only (registered here so that main.go stays unchanged).
var (
	simReps       = flag.Int("reps", 1, "合成: seed を変えて繰り返す回数（P1 の再実行は 20 以上）。seed, seed+1, … を使う")
	simFitReps    = flag.Int("fit-reps", -1, "合成: 推定（両当てはめ・ブートストラップ・D・判定）まで行う繰り返しの数（-1: すべて。詳しい表は 1 回目だけ）")
	simCountsOnly = flag.Bool("counts-only", false, "合成: 推定をせず、セルごとの情報を持つ三つ組の数だけを数える")
	simScenario   = flag.String("scenario", "reference", "合成: 判定者のシナリオ reference | misspec（文脈ごとのコストのばらつき + 5% のラベル誤り）")
	simWorld      = flag.String("world", "", "合成: 真のコストの世界 null | edgeN | alt | single | mild（decisions-stats 付録 A.5。空: alt、-null なら null）")
	simJSONL      = flag.String("jsonl", "", "合成: seed ごとの結果を 1 行の JSON で追記するファイル（A.5 の集計用）")
	simDReps      = flag.Int("d-reps", 50, "合成: D のネストしたブートストラップの回数（0: D を計算しない。本番は 200）")
	simApplyQC    = flag.Bool("apply-qc", false, "合成: 判定者の機械的 QC（§9、§9.1）で落ちた判定者を除いて推定する（既定は QC を計算・記録するだけ。参照シナリオの合成判定者は 3,000 三つ組で 3 人とも Krippendorff α < 0.4 で落ち、除くと推定できない）")
	simLOJO       = flag.Bool("lojo", false, "合成: 判定者を 1 人ずつ除いた判断の表も計算する（fit.Confirmatory の LOJO、各 B = -boot。1 seed の時間が約 4 倍）")
	simDiagBoot   = flag.Int("diag-boot", 0, "合成: 種別のタイを外した診断用の当てはめのブートストラップ回数（0: 点推定のみ）")
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

// simWorldName resolves -world and -null.
func simWorldName(null bool) (string, error) {
	w := *simWorld
	switch {
	case w == "" && null:
		return simulate.WorldNull, nil
	case w == "":
		return simulate.WorldAlt, nil
	case null && w != simulate.WorldNull:
		return "", fmt.Errorf("-null conflicts with -world %s", w)
	}
	return w, nil
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
	name, err := simWorldName(o.null)
	if err != nil {
		return cfg, err
	}
	w, err := simulate.WorldByName(name)
	if err != nil {
		return cfg, err
	}
	cfg = cfg.WithWorld(w)
	cfg.Sources, cfg.Seed, cfg.Sigma, cfg.Shape = o.sources, o.seed, o.sigma, *simShape
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

func runSimulate(o simOptions) error {
	cfg, err := simConfig(o)
	if err != nil {
		return err
	}
	world, _ := simulate.WorldByName(cfg.World)
	name := "simulate_" + cfg.World
	if *simScenario != "reference" {
		name += "_" + *simScenario
	}
	o.out = filepath.Join(filepath.Dir(o.out), name+".md")
	reps := max(1, *simReps)
	fitReps := reps
	if *simFitReps >= 0 {
		fitReps = min(reps, *simFitReps)
	}
	if *simCountsOnly {
		fitReps = 0
	}
	var jsonl *os.File
	if *simJSONL != "" && fitReps > 0 {
		if jsonl, err = os.OpenFile(*simJSONL, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err != nil {
			return err
		}
		defer jsonl.Close()
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "# シミュレーション（API 不使用）: 世界 %s\n", cfg.World)
	fmt.Fprintf(&b, "\n- 世界 %s: %s。θ_neg = %.2f\n", world.Name, world.Doc, world.TrueNeg)
	worldHeader(&b, cfg)
	fmt.Fprintf(&b, "- シナリオ %s、元の議論の形 %s、繰り返し %d 回（seed %d–%d）、うち推定 %d 回\n",
		*simScenario, cfg.Shape, reps, o.seed, o.seed+uint64(reps)-1, fitReps)
	fmt.Fprintf(&b, "- 1 回あたり三つ組 %d（元の議論 %d × %d、生成モデルの層 %s を元の議論ごとに交互）、判定者 %d × 提示順 2\n",
		cfg.Sources*cfg.PerSrc, cfg.Sources, cfg.PerSrc, strings.Join(simulate.Generators, " / "), len(cfg.Judges))
	fmt.Fprintf(&b, "- 共有のずれ σ = %.2f、無作為に答える確率 %.0f%%、文脈ごとのコストのばらつき %.2f、ラベル誤り %.0f%%\n",
		cfg.Sigma, 100*cfg.Random, cfg.Hetero, 100*cfg.LabelNoise)
	if fitReps > 0 {
		fmt.Fprintf(&b, "- 推定: `fit.Confirmatory`（判定者の QC → 既定の除外 %s → 種別のタイ（§12b、9 自由費用）→ 多点開始（θ₀、R1、全 0.5 + 無作為 2 点）の共主要 2 当てはめ（作業独立・ランダム切片）→ 同じ抽出で対にした生成モデル層化のクラスタブートストラップ B = %d → §3 の判断 → §4 の対比と Holm → D のネストしたブートストラップ %d 回 → 判定）。λ = 0（Confirmatory が固定、-lambda は無視）、m = %.2f、判定者を除く表 %v、判定者の QC による除外 %v\n",
			strings.Join(fit.DefaultExclude, "・"), o.boot, *simDReps, o.m, *simLOJO, *simApplyQC)
	}
	if reps < 20 {
		fmt.Fprintf(&b, "- 注意: P1 の再実行は 1 シナリオにつき 20 seed 以上（decisions-stats §16 #14）。この実行は %d seed\n", reps)
	}

	var counts []simulate.Counts
	var recs []seedRecord
	var fits []repFit
	var body bytes.Buffer
	for r := range reps {
		c := cfg
		c.Seed = o.seed + uint64(r)
		t0 := time.Now()
		trips, js, metas, err := simulate.Generate(c)
		if err != nil {
			return err
		}
		cnt, err := simulate.Count(trips, metas, ged.Current())
		if err != nil {
			return err
		}
		counts = append(counts, cnt)
		genSecs := time.Since(t0).Seconds()
		fmt.Fprintf(os.Stderr, "rep %d (seed %d): %d triplets (%d in the fit set), %d judgments, generated in %.1fs\n", r+1, c.Seed, len(trips), cnt.FitSet, len(js), genSecs)
		if r >= fitReps {
			continue
		}
		w := io.Writer(io.Discard)
		if r == 0 {
			w = &body
		}
		rec, rf, err := fitSeed(w, o, c, trips, js, metas, cnt, r == 0)
		if err != nil {
			return fmt.Errorf("seed %d: %w", c.Seed, err)
		}
		rec.Secs.Generate = jf(genSecs)
		rec.Secs.Total = jf(time.Since(t0).Seconds())
		fmt.Fprintf(os.Stderr, "  seed %d: verdict %s, %.0fs (confirmatory %.0fs, diagnostic %.0fs)\n",
			c.Seed, rec.Verdict, rec.Secs.Total, rec.Secs.Confirmatory, rec.Secs.Diag)
		recs = append(recs, rec)
		fits = append(fits, rf)
		if jsonl != nil {
			line, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			if _, err := jsonl.Write(append(line, '\n')); err != nil { // one write per line (O_APPEND)
				return err
			}
		}
	}

	countsReport(&b, counts, cfg.Sources*cfg.PerSrc)
	if len(recs) > 0 {
		acrossSeeds(&b, recs)
	}
	if len(fits) > 1 {
		acrossReps(&b, cfg, fits)
	}
	b.Write(body.Bytes())
	writeProvenance(&b, provenance{
		Seeds:  map[string]uint64{"data and Confirmatory (first; bootstrap +100, D +200, LOJO +300, stability +400, diagnostic bootstrap +500)": o.seed},
		Lambda: 0, M: o.m, B: o.boot, U: fit.DefaultU,
		Extra: map[string]string{"world": cfg.World, "scenario": *simScenario, "d-reps": fmt.Sprint(*simDReps), "diag-boot": fmt.Sprint(*simDiagBoot), "lojo": fmt.Sprint(*simLOJO), "apply-qc": fmt.Sprint(*simApplyQC)},
	})
	if err := os.WriteFile(o.out, b.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Print(b.String())
	return nil
}

// worldHeader prints θ* on both scales and which margin-judged costs lie outside the band on the normalized scale.
func worldHeader(w io.Writer, cfg simulate.Config) {
	th0 := fit.Theta0Extended(ged.Current())
	truth := trueVec(cfg)
	norm, s := fit.Normalize(truth, nil)
	var parts, out []string
	for _, p := range fit.MarginJudged {
		parts = append(parts, fmt.Sprintf("%s %.3f", fit.NormName(p), norm[p]))
		if math.Abs(norm[p]-th0[p]) > 0.25+1e-9 {
			out = append(out, fit.NormName(p))
		}
	}
	outS := "なし"
	if len(out) > 0 {
		outS = strings.Join(out, "、")
	}
	fmt.Fprintf(w, "- 正規化した尺度の真の値（ŝ_true = %.4f）: %s。帯 ±0.25 の外: %s。RA↔CA %.2f / CA↔MA %.2f（固定尺度）は種別のタイを外した診断用の当てはめだけが見る\n",
		s, strings.Join(parts, "、"), outS, cfg.True[ged.PTypeRACA], cfg.True[ged.PTypeCAMA])
}

func trueVec(cfg simulate.Config) []float64 {
	return append(slices.Clone(cfg.True[:]), cfg.TrueNeg)
}

// repFit is one replicate's fit for the across-replicate table.
type repFit struct {
	theta ged.Theta
	neg   float64
}

// ---------------------------------------------------------------------------------------------------------------
// One seed: the confirmatory analysis (fit.Confirmatory), the untied diagnostic fit, the per-seed JSONL record.
// ---------------------------------------------------------------------------------------------------------------

// jf is a float that marshals NaN and ±Inf as null.
type jf float64

func (f jf) MarshalJSON() ([]byte, error) {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return []byte("null"), nil
	}
	return json.Marshal(v)
}

func jfs(v []float64) []jf {
	out := make([]jf, len(v))
	for i, x := range v {
		out[i] = jf(x)
	}
	return out
}

// seedRecord is one JSONL line (the A.5 analysis input; scripts/a5_summarise.py). The line starts with
// {"world":…,"seed":…, which the resume check of scripts/a5_batch.sh relies on.
type seedRecord struct {
	World       string `json:"world"`
	Seed        uint64 `json:"seed"`
	Scenario    string `json:"scenario"`
	Sources     int    `json:"sources"`
	Triplets    int    `json:"triplets"`
	FitTriplets int    `json:"fit_triplets"`
	Judgments   int    `json:"judgments"`
	Boot        int    `json:"boot"`
	DReps       int    `json:"d_reps"`
	M           jf     `json:"m"`
	Git         string `json:"git"`
	// TrueAnchored is θ* (11 costs + θ_neg); TrueNorm the normalized truth (fit.NormLen entries); STrue its ŝ.
	TrueAnchored []jf `json:"true_anchored"`
	TrueNorm     []jf `json:"true_norm"`
	STrue        jf   `json:"s_true"`
	// Params are the normalized decision rows of fit.Confirmatory (tied costs omitted).
	Params []paramRecord `json:"params"`
	// Anchored estimates (ThetaVec) of the two fits; SHat their ŝ, SHatCI the 90% interval of the independence ŝ.
	AnchoredIndep   []jf     `json:"anchored_indep"`
	AnchoredMixed   []jf     `json:"anchored_mixed"`
	SHatIndep       jf       `json:"s_hat_indep"`
	SHatMixed       jf       `json:"s_hat_mixed"`
	SHatCI          [2]jf    `json:"s_hat_ci90"`
	ScaleIdentified bool     `json:"scale_identified"`
	Sigma           jf       `json:"sigma_hat"`
	ExcludedJudges  []string `json:"excluded_judges"`
	// QCFail are the judges that fail the mechanical QC with the reasons (excluded only with -apply-qc).
	QCFail map[string]string `json:"qc_fail"`
	Fits   struct {
		Indep fitFlags `json:"indep"`
		Mixed fitFlags `json:"mixed"`
	} `json:"fits"`
	BootShares struct {
		Indep shareRecord `json:"indep"`
		Mixed shareRecord `json:"mixed"`
	} `json:"boot_shares"`
	Hypotheses     []hypRecord `json:"hypotheses"`
	Verdict        string      `json:"verdict"`
	VerdictMissing []string    `json:"verdict_missing"`
	D              *dRecord    `json:"d"`
	// Informative are the n_p of §15.3 on the fit set at θ₀ (tied columns folded), keyed by parameter name, with the
	// floor used (fit.DefaultMinInformative scaled to the design size below 3,000 triplets) in MinInformative.
	Informative    map[string]int `json:"informative"`
	MinInformative map[string]int `json:"min_informative"`
	NegInformative int            `json:"neg_informative"`
	Exclusions     map[string]int `json:"exclusions"`
	// Stability is the matching-stability share of §11.6.
	Stability jf          `json:"stability"`
	Diag      *diagRecord `json:"diag"`
	Secs      struct {
		Generate jf `json:"generate"`
		// Confirmatory is the whole fit.Confirmatory call (fits, bootstrap, contrasts, D, LOJO if on, stability).
		Confirmatory jf `json:"confirmatory"`
		Diag         jf `json:"diag"`
		Total        jf `json:"total"`
	} `json:"secs"`
}

type decRecord struct {
	Est  jf `json:"est"`
	SD   jf `json:"sd"`
	Lo90 jf `json:"lo90"`
	Hi90 jf `json:"hi90"`
	Lo95 jf `json:"lo95"`
	Hi95 jf `json:"hi95"`
	// Upper95 is the one-sided 95% upper bound that replaces the 90% interval when OneSided (boundary share 10–50%).
	Upper95  jf     `json:"upper95"`
	OneSided bool   `json:"one_sided"`
	Label    string `json:"label"`
	Reason   string `json:"reason"`
	Boundary jf     `json:"boundary_share"`
	Diverg   jf     `json:"divergent_share"`
	Bias     jf     `json:"boot_bias"`
	Reps     int    `json:"reps"`
	Total    int    `json:"total_reps"`
}

func decRec(d stats.ParamDecision) decRecord {
	return decRecord{Est: jf(d.Est), SD: jf(d.SD), Lo90: jf(d.CI90.Lo), Hi90: jf(d.CI90.Hi), Lo95: jf(d.CI95.Lo), Hi95: jf(d.CI95.Hi),
		Upper95: jf(d.Upper), OneSided: d.OneSided, Label: d.Label.String(), Reason: d.Reason, Boundary: jf(d.BoundaryShare),
		Diverg: jf(d.DivergentShare), Bias: jf(d.Bias), Reps: d.Reps, Total: d.TotalReps}
}

type paramRecord struct {
	Index     int        `json:"index"`
	Name      string     `json:"name"`
	Margin    bool       `json:"margin"`
	Theta0    jf         `json:"theta0"`
	TrueNorm  jf         `json:"true_norm"`
	Indep     decRecord  `json:"indep"`
	Mixed     *decRecord `json:"mixed"`
	Combined  string     `json:"combined"`
	NInf      int        `json:"n_inf"`
	MinInf    int        `json:"min_inf"`
	AnchIndep jf         `json:"anchored_indep"`
}

type fitFlags struct {
	Converged      bool     `json:"converged"`
	Cycled         bool     `json:"cycled"`
	Capped         bool     `json:"capped"`
	InnerConverged bool     `json:"inner_converged"`
	Outer          int      `json:"outer"`
	Boundary       []string `json:"boundary"`
	Divergent      []string `json:"divergent"`
	StartSpread    jf       `json:"start_spread"`
	StartNLL       []jf     `json:"start_nll"`
	NLL            jf       `json:"nll"`
}

func flagsOf(r fit.Result) fitFlags {
	f := fitFlags{Converged: r.Converged, Cycled: r.Cycled, Capped: r.Capped, InnerConverged: r.InnerConverged, Outer: r.Outer,
		StartSpread: jf(r.StartSpread), NLL: jf(r.NegLogLik), Boundary: []string{}, Divergent: []string{}}
	for p := range ged.NumParams {
		if r.Free[p] && r.Boundary[p] {
			f.Boundary = append(f.Boundary, ged.ParamNames[p])
		}
		if r.Free[p] && r.Divergent[p] {
			f.Divergent = append(f.Divergent, ged.ParamNames[p])
		}
	}
	for _, s := range r.Starts {
		f.StartNLL = append(f.StartNLL, jf(s.NegLogLik))
	}
	return f
}

type shareRecord struct {
	Cycled       jf            `json:"cycled"`
	Capped       jf            `json:"capped"`
	NotConverged jf            `json:"not_converged"`
	Cold         int           `json:"cold"`
	Boundary     map[string]jf `json:"boundary"`
	Errors       int           `json:"errors"`
}

func sharesOf(meta []fit.RepMeta, free [ged.NumParams]bool) shareRecord {
	s := fit.Summarise(meta)
	r := shareRecord{Cycled: jf(s.Cycled), Capped: jf(s.Capped), NotConverged: jf(s.NotConverged), Cold: s.Cold, Boundary: map[string]jf{}}
	for p := range ged.NumParams {
		if free[p] && s.Boundary[p] > 0 {
			r.Boundary[ged.ParamNames[p]] = jf(s.Boundary[p])
		}
	}
	for _, m := range meta {
		if m.Err != "" {
			r.Errors++
		}
	}
	return r
}

type hypRecord struct {
	ID        string `json:"id"`
	Est       jf     `json:"est"`
	Lo95      jf     `json:"lo95"`
	Hi95      jf     `json:"hi95"`
	P         jf     `json:"p"`
	MixedEst  *jf    `json:"mixed_est,omitempty"`
	MixedP    *jf    `json:"mixed_p,omitempty"`
	PHolm     jf     `json:"p_holm"`
	Reject    bool   `json:"reject"`
	Supported bool   `json:"supported"`
}

// dRecord is the agreement form of D with the one-sided 95% bounds of decisions §7 (q₀.₀₅ and q₀.₉₅ of the nested
// bootstrap, so [Lower, Upper] is a two-sided 90% interval).
type dRecord struct {
	Agree   jf            `json:"agree"`
	Lower   jf            `json:"lower"`
	Upper   jf            `json:"upper"`
	SD      jf            `json:"boot_sd"`
	LogLoss jf            `json:"logloss"`
	N       int           `json:"n"`
	Reps    int           `json:"reps"`
	Strata  map[string]jf `json:"strata"`
}

type diagRecord struct {
	Pairs     map[string]diagParam `json:"pairs"`
	Converged bool                 `json:"converged"`
	Cycled    bool                 `json:"cycled"`
	Triplets  int                  `json:"triplets"`
	// TypeNorm is the untied fit's own normalized RA↔MA (its θ_type).
	TypeNorm jf `json:"type_norm"`
}

type diagParam struct {
	Anchored     jf  `json:"anchored"`
	Norm         jf  `json:"norm"`
	TrueAnchored jf  `json:"true_anchored"`
	TrueNorm     jf  `json:"true_norm"`
	Lo90         *jf `json:"lo90,omitempty"`
	Hi90         *jf `json:"hi90,omitempty"`
	// Contrast is θ̃_pair − θ̃_RA↔MA within the untied fit, with its 95% percentile interval.
	Contrast     jf  `json:"contrast"`
	ContrastLo95 *jf `json:"contrast_lo95,omitempty"`
	ContrastHi95 *jf `json:"contrast_hi95,omitempty"`
	Feasible     int `json:"feasible_relabels"`
}

func ptr(v float64) *jf { x := jf(v); return &x }

// minInformative is fit.DefaultMinInformative scaled to the design size when it is below 3,000 triplets.
func minInformative(n int) map[int]int {
	out := fit.DefaultMinInformative()
	if n < 3000 {
		for k, v := range out {
			out[k] = int(math.Ceil(float64(v) * float64(n) / 3000))
		}
	}
	return out
}

// confirmatory runs the pre-registered analysis on one simulated data set through the single entry point
// fit.Confirmatory (judge QC, default filters, type tie, multi-start co-primary fits, paired bootstrap, §3 decisions
// with every safeguard, §4 contrasts with Holm, nested D, verdict, LOJO when -lojo, matching stability) and writes
// WriteConfirmatory to w. λ is forced to 0 there; the D strata are fit.DefaultStrata, as in the real analysis.
func confirmatory(w io.Writer, o simOptions, cfg simulate.Config, trips []fit.Triplet, js []fit.Judgment) (fit.ConfirmResult, error) {
	th0 := ged.Current()
	names := make([]string, len(cfg.Judges))
	for j := range names {
		names[j] = simulate.JudgeFamily(j)
	}
	opts := fit.Options{Judges: len(cfg.Judges), Theta0: th0, Tie: fit.TypeTie(), MaxOuter: 20}
	co := fit.ConfirmOptions{Theta0: th0, M: o.m, B: o.boot, DReps: *simDReps, SkipD: *simDReps <= 0, Seed: cfg.Seed,
		MinInformative: minInformative(cfg.Sources * cfg.PerSrc), JudgeNames: names, SkipLOJO: !*simLOJO, LOJOReps: o.boot,
		SkipExclusions: !*simApplyQC, Truth: &cfg.True, TrueNeg: &cfg.TrueNeg}
	res, err := fit.Confirmatory(trips, js, opts, co)
	if err != nil {
		return res, err
	}
	fmt.Fprintf(os.Stderr, "  confirmatory: independence outer=%d converged=%v cycled=%v capped=%v spread=%.3f; random intercept outer=%d converged=%v capped=%v σ̂=%.3f; verdict %s\n",
		res.Indep.Outer, res.Indep.Converged, res.Indep.Cycled, res.Indep.Capped, res.Indep.StartSpread,
		res.Mixed.Outer, res.Mixed.Converged, res.Mixed.Capped, res.Mixed.Params.Sigma, res.Verdict)
	fmt.Fprintf(w, "\n# 1 回目の seed の詳細（seed %d）\n", cfg.Seed)
	fmt.Fprintf(w, "\nn_p の下限は §15.3 の値を三つ組 %d に比例換算（3,000 以上は換算しない）。合成データには文全体の埋め込みがないので C4（R0 対 B1）は計算しない。判定者を 1 人ずつ除く表は -lojo のときだけ。\n", cfg.Sources*cfg.PerSrc)
	if !*simApplyQC {
		fmt.Fprintf(w, "\n**判定者の QC は計算して表に出すが、除外には使わない（-apply-qc なし）。QC で落ちる判定者: %s。**\n", qcSummary(res.QC))
	}
	WriteConfirmatory(w, res, provenance{
		Seeds:  map[string]uint64{"data / Confirmatory": cfg.Seed},
		Lambda: 0, M: co.M, B: co.B, U: fit.DefaultU,
		Extra: map[string]string{"world": cfg.World, "scenario": *simScenario, "d-reps": fmt.Sprint(*simDReps), "lojo": fmt.Sprint(*simLOJO), "apply-qc": fmt.Sprint(*simApplyQC)},
	})
	return res, nil
}

// qcSummary lists the judges that fail the mechanical QC with their reasons ("なし" when none).
func qcSummary(q fit.JudgeQCResult) string {
	var parts []string
	for j, x := range q.Excluded {
		if x {
			parts = append(parts, fmt.Sprintf("%s（%s）", q.Names[j], q.Reasons[j]))
		}
	}
	if len(parts) == 0 {
		return "なし"
	}
	return strings.Join(parts, "、")
}

// fitSeed runs the full analysis on one simulated data set and returns its JSONL record. w receives the detailed
// report (io.Discard except for the first seed); detail adds the descriptive tables that cost extra fits.
func fitSeed(w io.Writer, o simOptions, cfg simulate.Config, trips []fit.Triplet, js []fit.Judgment, metas []simulate.Meta, cnt simulate.Counts, detail bool) (seedRecord, repFit, error) {
	th0 := ged.Current()
	rec := seedRecord{World: cfg.World, Seed: cfg.Seed, Scenario: *simScenario, Sources: cfg.Sources, Triplets: len(trips),
		Judgments: len(js), Boot: o.boot, DReps: *simDReps, M: jf(o.m), Git: gitDescribe()}
	truth := trueVec(cfg)
	truthN, sTrue := fit.Normalize(truth, nil)
	rec.TrueAnchored, rec.TrueNorm, rec.STrue = jfs(truth), jfs(truthN), jf(sTrue)

	// Diagnostic untied fit (§12b): tie off, diag_ca_type included (the other default exclusions kept), from θ₀.
	t := time.Now()
	diag, err := diagnosticFit(trips, js, cfg, cnt, o)
	if err != nil {
		return rec, repFit{}, fmt.Errorf("diagnostic fit: %w", err)
	}
	rec.Diag = diag
	rec.Secs.Diag = jf(time.Since(t).Seconds())

	t = time.Now()
	res, err := confirmatory(w, o, cfg, trips, js)
	if err != nil {
		return rec, repFit{}, err
	}
	rec.Secs.Confirmatory = jf(time.Since(t).Seconds())
	if res.Triplets != cnt.FitSet {
		return rec, repFit{}, fmt.Errorf("fit set %d ≠ Meta.InFit count %d", res.Triplets, cnt.FitSet)
	}
	rec.FitTriplets = res.Triplets
	indep, mixed := res.Indep, res.Mixed

	rec.Informative, rec.MinInformative = map[string]int{}, map[string]int{}
	tie := fit.TypeTie()
	for p := 0; p <= fit.NormEdgeExtra; p++ {
		if _, tied := tie[p]; !tied {
			rec.Informative[fit.NormName(p)] = res.Informative[p]
		}
	}
	for p, v := range res.Options.MinInformative {
		rec.MinInformative[fit.NormName(p)] = v
	}
	rec.NegInformative = res.NegInformative
	rec.Exclusions = res.Exclusions
	rec.ExcludedJudges = append([]string{}, res.ExcludedJudges...)
	rec.QCFail = map[string]string{}
	for j, x := range res.QC.Excluded {
		if x {
			rec.QCFail[res.QC.Names[j]] = res.QC.Reasons[j]
		}
	}
	rec.Stability = jf(res.Stability)
	_, sMixed := fit.NormalizeParams(mixed.Params, nil)
	rec.SHatIndep, rec.SHatMixed = jf(res.SHat), jf(sMixed)
	rec.SHatCI = [2]jf{jf(res.SHatCI[0]), jf(res.SHatCI[1])}
	rec.ScaleIdentified = res.ScaleIdentified
	rec.AnchoredIndep, rec.AnchoredMixed = jfs(fit.ThetaVec(indep.Params)), jfs(fit.ThetaVec(mixed.Params))
	rec.Sigma = jf(mixed.Params.Sigma)
	rec.Fits.Indep, rec.Fits.Mixed = flagsOf(indep), flagsOf(mixed)
	rec.BootShares.Indep, rec.BootShares.Mixed = sharesOf(res.Boot.Meta, indep.Free), sharesOf(res.Boot.MixedMeta, mixed.Free)
	th0n := fit.Theta0Extended(th0)
	for _, r := range res.Params {
		pr := paramRecord{Index: r.Index, Name: r.Name, Margin: r.Margin, Theta0: jf(th0n[r.Index]), TrueNorm: jf(truthN[r.Index]),
			Indep: decRec(r.Indep), Combined: r.Combined.String(), NInf: r.Indep.N, MinInf: r.Indep.MinN, AnchIndep: jf(r.Anchored)}
		if r.Mixed != nil {
			d := decRec(*r.Mixed)
			pr.Mixed = &d
		}
		rec.Params = append(rec.Params, pr)
	}
	for _, h := range res.Hypotheses {
		hr := hypRecord{ID: h.ID, Est: jf(h.Indep.Est), Lo95: jf(h.Indep.CI95.Lo), Hi95: jf(h.Indep.CI95.Hi), P: jf(h.Indep.P),
			PHolm: jf(h.PHolm), Reject: h.Reject, Supported: h.Supported}
		if h.Mixed != nil {
			hr.MixedEst, hr.MixedP = ptr(h.Mixed.Est), ptr(h.Mixed.P)
		}
		rec.Hypotheses = append(rec.Hypotheses, hr)
	}
	if d := res.D; d != nil {
		col := sortedClean(d.AgreeBoot)
		lo, hi := d.DBounds()
		dr := &dRecord{Agree: jf(d.Agree), Lower: jf(lo), Upper: jf(hi), SD: jf(fit.SD(col)), LogLoss: jf(d.LogLoss), N: d.N,
			Reps: len(col), Strata: map[string]jf{}}
		for k, v := range d.Strata {
			dr.Strata[k] = jf(v)
		}
		rec.D = dr
	}
	rec.Verdict, rec.VerdictMissing = res.Verdict.String(), append([]string{}, res.Missing...)

	diagReport(w, diag, o.m)
	if detail {
		if err := detailTables(w, cfg, trips, js, metas, indep, mixed); err != nil {
			return rec, repFit{}, err
		}
	}
	return rec, repFit{indep.Params.Theta, indep.Params.ThetaNeg}, nil
}

// diagnosticFit is the pre-registered untied fit of §12b: the type tie off and the diag_ca_type triplets included.
// It is descriptive (no §3 labels): point estimates, and percentile intervals when -diag-boot > 0.
func diagnosticFit(trips []fit.Triplet, js []fit.Judgment, cfg simulate.Config, cnt simulate.Counts, o simOptions) (*diagRecord, error) {
	excl := []string{}
	for _, f := range fit.DefaultExclude {
		if f != "diag_ca_type" {
			excl = append(excl, f)
		}
	}
	od := fit.Options{Judges: len(cfg.Judges), Theta0: ged.Current(), FitNeg: true, MaxOuter: 20, Exclude: excl}
	res, err := fit.Fit(trips, js, od, nil)
	if err != nil {
		return nil, err
	}
	est, _ := fit.NormalizeParams(res.Params, nil)
	truthN, _ := fit.Normalize(trueVec(cfg), nil)
	rec := &diagRecord{Converged: res.Converged, Cycled: res.Cycled, Triplets: res.Triplets, TypeNorm: jf(est[ged.PTypeRAMA]), Pairs: map[string]diagParam{}}
	feas := map[int]int{ged.PTypeRACA: cnt.Cells[simulate.CellDiagRACA], ged.PTypeCAMA: cnt.Cells[simulate.CellDiagCAMA]}
	var boot [][]float64
	if *simDiagBoot > 0 {
		br, err := fit.BootstrapOpts(trips, js, od, res.Params, fit.BootOptions{Reps: *simDiagBoot, Seed: cfg.Seed + 500})
		if err != nil {
			return nil, err
		}
		boot, _ = fit.NormalizeRows(br.Theta, nil)
	}
	for _, p := range []int{ged.PTypeRACA, ged.PTypeCAMA} {
		dp := diagParam{Anchored: jf(res.Params.Theta[p]), Norm: jf(est[p]), TrueAnchored: jf(cfg.True[p]), TrueNorm: jf(truthN[p]),
			Contrast: jf(est[p] - est[ged.PTypeRAMA]), Feasible: feas[p]}
		if boot != nil {
			var col, con []float64
			for _, r := range boot {
				if r != nil {
					col = append(col, r[p])
					con = append(con, r[p]-r[ged.PTypeRAMA])
				}
			}
			col, con = sortedClean(col), sortedClean(con)
			dp.Lo90, dp.Hi90 = ptr(stats.Quantile(col, 0.05)), ptr(stats.Quantile(col, 0.95))
			dp.ContrastLo95, dp.ContrastHi95 = ptr(stats.Quantile(con, 0.025)), ptr(stats.Quantile(con, 0.975))
		}
		rec.Pairs[ged.ParamNames[p]] = dp
	}
	return rec, nil
}

func diagReport(w io.Writer, d *diagRecord, m float64) {
	fmt.Fprintln(w, "\n## 種別のタイを外した診断用の当てはめ（決定文書 §12b、記述のみ）")
	fmt.Fprintf(w, "\nタイなし、diag_ca_type の三つ組を含める（他の既定の除外は残す）。三つ組 %d、収束 %v、循環 %v。自身の θ̃_RA↔MA = %.3f。\n", d.Triplets, d.Converged, d.Cycled, d.TypeNorm)
	fmt.Fprintln(w, "\n| 種別の組 | 真の値（固定尺度） | 推定（固定尺度） | 真の値（正規化） | 推定（正規化） | 90% 区間 | θ̃_RA↔MA との差 | 95% 区間 | 実現した付け替え |")
	fmt.Fprintln(w, "| --- | --: | --: | --: | --: | --- | --: | --- | --: |")
	for _, p := range []int{ged.PTypeRACA, ged.PTypeCAMA} {
		dp := d.Pairs[ged.ParamNames[p]]
		ci90, ci95 := "—", "—"
		if dp.Lo90 != nil {
			ci90 = fmt.Sprintf("[%.2f, %.2f]", *dp.Lo90, *dp.Hi90)
			ci95 = fmt.Sprintf("[%.2f, %.2f]", *dp.ContrastLo95, *dp.ContrastHi95)
		}
		fmt.Fprintf(w, "| %s | %.2f | %.3f | %.3f | %.3f | %s | %+.3f | %s | %d |\n", ged.ParamNames[p], dp.TrueAnchored, dp.Anchored, dp.TrueNorm, dp.Norm, ci90, dp.Contrast, ci95, dp.Feasible)
	}
	fmt.Fprintf(w, "\n診断セルは実現可能な（弱い関係の）付け替えだけ。区間は -diag-boot > 0 のときだけのパーセンタイル（m = %.2f の判断はしない）。\n", m)
}

// detailTables are the descriptive tables of the first seed (not §3 decisions): aux parameters against the truth,
// agreement per rule (R0, R2 full data, R2 OOF, θ*) and the judge diagnostics, on the default fit set.
func detailTables(w io.Writer, cfg simulate.Config, trips []fit.Triplet, js []fit.Judgment, metas []simulate.Meta, indep, mixed fit.Result) error {
	opts := fit.Options{Judges: len(cfg.Judges), Theta0: ged.Current(), FitNeg: true, MaxOuter: 20, Tie: fit.TypeTie()}
	ft, fj, kept := fit.Filter(trips, js, opts)
	opts.IncludeAll = true
	names := make([]string, len(cfg.Judges))
	truth := make([]judgeTruth, len(cfg.Judges))
	for j, jd := range cfg.Judges {
		names[j] = fmt.Sprintf("合成 %d", j+1)
		truth[j] = judgeTruth{jd.Beta, jd.Tau, jd.Pi}
	}
	fmt.Fprintln(w, "\n## 記述用の表（1 回目の seed、§3 の判断ではない）")
	auxTable(w, names, indep.Params, truth)
	fmt.Fprintln(w, "\nランダム切片の当てはめ:")
	auxTable(w, names, mixed.Params, truth)
	fmt.Fprintf(w, "\nσ̂ = %.3f（真の共有のずれ σ = %.2f）\n", mixed.Params.Sigma, cfg.Sigma)
	cv, err := fit.CrossValidateOpts(ft, fj, opts, fit.CVOptions{K: 5, Seed: cfg.Seed})
	if err != nil {
		return err
	}
	var rowsA []agreeRow
	for _, r := range []struct {
		name string
		th   ged.Theta
	}{{"R0 現行のコスト表", ged.Current()}, {"R2 作業独立（全データ、学習に使ったもの）", indep.Params.Theta}, {"R2 ランダム切片（全データ）", mixed.Params.Theta}, {"真のコスト θ*（極性反転は見えない）", cfg.True}} {
		d, err := fit.Delta(ft, r.th)
		if err != nil {
			return err
		}
		rowsA = append(rowsA, agreeRow{r.name, d})
	}
	rowsA = slices.Insert(rowsA, 2, agreeRow{"R2 作業独立（5 分割交差検証）", cv.Delta[0]})
	types := make([]perturb.TripletType, len(ft))
	sign := make([]int, len(ft))
	neg := make([]bool, len(ft))
	for i, k := range kept {
		m := metas[k]
		types[i] = m.Type
		switch {
		case m.TrueDelta > 1e-9:
			sign[i] = 1
		case m.TrueDelta < -1e-9:
			sign[i] = -1
		}
		neg[i] = ft[i].NegDiff != 0
	}
	agreementTable(w, rowsA, fj, neg)
	fmt.Fprintf(w, "\nOOF 対数損失（作業独立、5 分割）: %.4f nats/判定\n", cv.MeanLogLoss(0))
	judgeDiagnostics(w, names, types, sign, fj)
	return nil
}

// ---------------------------------------------------------------------------------------------------------------
// Across seeds
// ---------------------------------------------------------------------------------------------------------------

// acrossSeeds summarizes the per-seed records: per margin-judged parameter the label frequencies, the Monte-Carlo SD
// of θ̃ against the mean bootstrap SD and the 90% coverage of the truth (both fits); the verdicts; D's Monte-Carlo SD
// against the nested-bootstrap SD (§16 #14); the informative counts against the minimum; the timing.
func acrossSeeds(w io.Writer, recs []seedRecord) {
	n := len(recs)
	fmt.Fprintf(w, "\n## seed をまたいだ推定の要約（%d seed、正規化した尺度）\n", n)
	fmt.Fprintln(w, "\n「MC SD」は seed 間の θ̃ の SD、「SD」はブートストラップの SD の平均、「被覆」は 90% 区間が真の値（正規化）を含む割合。少ない seed では目安にすぎない（A.5 は 200 seed）。")
	fmt.Fprintln(w, "\n| パラメータ | 真の値 | 平均 θ̃（独立） | MC SD | SD | 被覆 90% | 平均 θ̃（切片） | MC SD | SD | 被覆 90% | 判断（共通）の内訳 |")
	fmt.Fprintln(w, "| --- | --: | --: | --: | --: | --: | --: | --: | --: | --: | --- |")
	for i, p := range recs[0].Params {
		var ei, em, si, sm []float64
		ci, cm := 0, 0
		labels := map[string]int{}
		for _, r := range recs {
			q := r.Params[i]
			ei = append(ei, float64(q.Indep.Est))
			si = append(si, float64(q.Indep.SD))
			if covers(q.Indep, q.TrueNorm) {
				ci++
			}
			if q.Mixed != nil {
				em = append(em, float64(q.Mixed.Est))
				sm = append(sm, float64(q.Mixed.SD))
				if covers(*q.Mixed, q.TrueNorm) {
					cm++
				}
			}
			labels[q.Combined]++
		}
		lab := "—（区間のみ）"
		if p.Margin {
			lab = labelCounts(labels)
		}
		fmt.Fprintf(w, "| %s | %.3f | %.3f | %.3f | %.3f | %d/%d | %.3f | %.3f | %.3f | %d/%d | %s |\n",
			p.Name, float64(p.TrueNorm), mean(ei), fit.SD(ei), mean(si), ci, n, mean(em), fit.SD(em), mean(sm), cm, n, lab)
	}
	verdicts := map[string]int{}
	cyc, bnd := 0, 0
	var secs []float64
	for _, r := range recs {
		verdicts[r.Verdict]++
		if r.Fits.Indep.Cycled || r.Fits.Mixed.Cycled {
			cyc++
		}
		if len(r.Fits.Indep.Boundary) > 0 || len(r.Fits.Mixed.Boundary) > 0 {
			bnd++
		}
		secs = append(secs, float64(r.Secs.Total))
	}
	fmt.Fprintf(w, "\n判定: %s。全データの当てはめが循環した seed %d、境界に達した費用がある seed %d。1 seed あたり %.0f 秒（範囲 %.0f–%.0f）。\n",
		labelCounts(verdicts), cyc, bnd, mean(secs), slices.Min(secs), slices.Max(secs))

	// D: nested bootstrap against the Monte-Carlo SD (§16 #14).
	var ds, sds []float64
	for _, r := range recs {
		if r.D != nil {
			ds = append(ds, float64(r.D.Agree))
			sds = append(sds, float64(r.D.SD))
		}
	}
	if len(ds) > 0 {
		md := mean(ds)
		cov := 0
		for _, r := range recs {
			if r.D != nil && float64(r.D.Lower) <= md && md <= float64(r.D.Upper) {
				cov++
			}
		}
		fmt.Fprintf(w, "\nD（pt）: 平均 %.2f、seed 間の SD（Monte-Carlo）%.2f、ネストしたブートストラップの SD の平均 %.2f（比 %.2f）、片側 95%% の下限–上限（= 両側 90%% 区間）が seed 平均の D を含む割合 %d/%d。\n",
			md, fit.SD(ds), mean(sds), mean(sds)/fit.SD(ds), cov, len(ds))
	}

	// Informative counts.
	fmt.Fprintln(w, "\n| 情報を持つ三つ組 n_p | 下限 | 最小 | 平均 | 最大 |")
	fmt.Fprintln(w, "| --- | --: | --: | --: | --: |")
	keys := make([]string, 0, len(recs[0].Informative))
	for k := range recs[0].Informative {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		var xs []float64
		for _, r := range recs {
			xs = append(xs, float64(r.Informative[k]))
		}
		minS := "—"
		if v, ok := recs[0].MinInformative[k]; ok {
			minS = fmt.Sprint(v)
		}
		fmt.Fprintf(w, "| %s | %s | %.0f | %.0f | %.0f |\n", k, minS, slices.Min(xs), mean(xs), slices.Max(xs))
	}
	var negs []float64
	for _, r := range recs {
		negs = append(negs, float64(r.NegInformative))
	}
	fmt.Fprintf(w, "| %s（NegDiff ≠ 0） | %d | %.0f | %.0f | %.0f |\n", fit.NormName(fit.NormNeg), recs[0].MinInformative[fit.NormName(fit.NormNeg)], slices.Min(negs), mean(negs), slices.Max(negs))

	// Diagnostic untied fit.
	fmt.Fprintln(w, "\n| 診断用の当てはめ | 真の値（正規化） | 平均推定（正規化） | seed 間の SD |")
	fmt.Fprintln(w, "| --- | --: | --: | --: |")
	for _, p := range []int{ged.PTypeRACA, ged.PTypeCAMA} {
		var xs []float64
		var tv float64
		for _, r := range recs {
			dp := r.Diag.Pairs[ged.ParamNames[p]]
			xs = append(xs, float64(dp.Norm))
			tv = float64(dp.TrueNorm)
		}
		fmt.Fprintf(w, "| %s | %.3f | %.3f | %.3f |\n", ged.ParamNames[p], tv, mean(xs), fit.SD(xs))
	}
}

func covers(d decRecord, truth jf) bool {
	if d.OneSided {
		return float64(truth) <= float64(d.Upper95)
	}
	return float64(d.Lo90) <= float64(truth) && float64(truth) <= float64(d.Hi90)
}

func labelCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, "、")
}

// countsReport prints the realized per-cell counts over the replicates against dataset-spec §2.3 and decisions-stats §15.3.
func countsReport(w io.Writer, cs []simulate.Counts, n int) {
	scale := float64(n) / 3000
	fitN := make([]float64, len(cs))
	for i, c := range cs {
		fitN[i] = float64(c.FitSet)
	}
	fmt.Fprintln(w, "\n## セルごとの情報を持つ三つ組の数（dataset-spec §2.3、decisions-stats §15.3）")
	fmt.Fprintf(w, "\nΔφ_p ≠ 0（θ₀ の対応付け）の三つ組を数える。期待値（§2.3 = この simulator の 3,000 三つ組 × 20 seed の平均）と最低数は 3,000 三つ組の値を %d 三つ組に比例換算（×%.2f）。", n, scale)
	fmt.Fprintf(w, "範囲は %d 回の最小–最大。「fit」は推定に入る三つ組（平均 %.0f）、「all」はすべて、「draws」は編集の抽選数。\n", len(cs), mean(fitN))
	fmt.Fprintln(w, "判定: 全回で最低数以上 → OK、平均は最低数以上だが下回る回がある → thin、平均が最低数未満 → **SHORT**。")
	fmt.Fprintln(w, "「全三つ組」列は fit のセルを除外分（X-spk、t3_diag の CA 種別変更）も含めて数えた平均。")
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
	fmt.Fprintln(w, "\n実行できない種類は引き直すため、実現割合は重みからずれうる。t3_ident 層（P-del / R-del のセルの限られた種類）と t3_diag 層（推定側の 1 編集）の抽選も含む。CA を含む種別変更は t3_diag 層だけで引く（下の向きごとの抽選数）。")
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

// acrossReps summarizes the anchored point estimates of the working-independence fit over the replicates.
func acrossReps(w io.Writer, cfg simulate.Config, fits []repFit) {
	fmt.Fprintf(w, "\n## 繰り返し %d 回の推定値（作業独立、固定尺度の点推定、推定に使う三つ組のみ）\n", len(fits))
	fmt.Fprintln(w, "\nRA↔CA と CA↔MA は種別のタイで RA↔MA の値を写すだけなので省く（真の値は診断用の当てはめの表を参照）。")
	fmt.Fprintln(w, "\n| パラメータ | 現行値 θ₀ | 真の値 θ* | 平均 | 回の間の SD | 偏り | RMSE |")
	fmt.Fprintln(w, "| --- | --: | --: | --: | --: | --: | --: |")
	th0 := ged.Current()
	tie := fit.TypeTie()
	for i := 0; i <= ged.NumParams; i++ {
		if _, tied := tie[i]; tied {
			continue
		}
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
