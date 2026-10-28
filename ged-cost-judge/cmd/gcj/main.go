// Command gcj is the CLI for the experiment that estimates GED edit costs from judgments.
//
//	gcj -simulate      check with synthetic judgments whether estimation can recover the costs (no API, plan P1)
//	gcj -topics        stage 0: build the topic bank (API)
//	gcj -import-qt30   build the QT30 transfer set from ../aif-with-llm/data/aifdb (no API)
//	gcj -pilot         generate sources, variants and judgments, and estimate (API; -p2 for the P2 report)
//	gcj -qc            P2 audits on the records of a -pilot run (API)
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/negishi-tako/tori-pub/ged-cost-judge/internal/synth"
)

// Default reasoning settings (plan §5.1: fixed per judge in P2 and not changed afterwards).
//   - gpt-5.6-luna: reasoning_effort low (pilot 2 ran luna without an effort setting, ≈ 95 output tokens).
//   - claude-sonnet-5-5: effort low with adaptive thinking (the model default effort is high).
//   - claude-haiku-5-5: adaptive thinking on, made explicit (it thinks by default), and effort low
//     (its default is medium). §4.5 budgets ≈ 300 output tokens per Claude judgment including thinking.
//
// Generators / verifiers: Sonnet 5.5 at effort medium (default high), luna at the provider default.
const (
	defJudgeEffort   = "gpt-5.6-luna=low,claude-sonnet-5-5=low,claude-haiku-5-5=low"
	defJudgeThinking = "claude-sonnet-5-5=adaptive,claude-haiku-5-5=adaptive"
	defGenEffort     = "claude-sonnet-5-5=medium"
)

func main() {
	var (
		doSim     = flag.Bool("simulate", false, "合成の判定でパラメータの回収を確かめる（API 不使用）")
		doPilot   = flag.Bool("pilot", false, "元の議論・変種・判定を作って推定まで通す（API を使う）")
		doTopics  = flag.Bool("topics", false, "段階 0: 話題バンクを作る（領域ごとに 2 回、cos < 0.85 で重複除去。<out>/topics.json）")
		doQC      = flag.Bool("qc", false, "P2 の監査（ラベルの正しさ 100 変種、均質さ 40 + 40 件、例示の再注釈）を前の -pilot の記録に対して行う（API を使う）")
		doQT30    = flag.Bool("import-qt30", false, "QT30 の転移セットを作る（-aifdb から §1.4 の条件で。<out>/qt30_sources.json。API 不使用）")
		sources   = flag.Int("sources", 750, "元の議論の数（1 件につき三つ組 4 つ）")
		seed      = flag.Uint64("seed", 1, "乱数の seed")
		boot      = flag.Int("boot", 100, "クラスタブートストラップの回数")
		lambda    = flag.Float64("lambda", 0.5, "θ₀ を中心とする L2 正則化の係数")
		margin    = flag.Float64("m", 0.3, "判断の幅 m")
		sigma     = flag.Float64("sigma", 1, "合成: 三つ組ごとに判定者が共有するずれ σ")
		null      = flag.Bool("null", false, "合成: 真のコストを現行値にする")
		verify    = flag.Bool("verify", false, "v1 のみ: 書き下しの検証つきの手順を使う（realize_v2 + verify_v1、T3 の割り付け）")
		outDir    = flag.String("out", "out", "出力先")
		budget    = flag.Float64("budget", -1, "API の費用の上限（USD）。超えたら以降の呼び出しを止め、バッチは投げる前に見込みを確かめる。負なら既定（-sources 750 で $260 = dataset-spec §4.5 の定価の 1.5 倍、件数に比例。-topics は $2、-qc は $5）")
		promptVer = flag.String("prompt-version", synth.V2, "v2: 改訂版のパイプライン（例示つきの生成、realize_v3、盲検の再注釈、2 系列）。v1: パイロット 2 の手順（luna のみ。応答のキャッシュで再現する）")
		gens      = flag.String("generators", "gpt-5.6-luna,claude-sonnet-5-5", "v2: 生成モデル（半数ずつ。検証者は別系列の方）")
		judges    = flag.String("judges", "gpt-5.6-luna,claude-sonnet-5-5,claude-haiku-5-5", "v2: 判定者（全三つ組 × 提示順 2。Anthropic はバッチ）")
		jEffort   = flag.String("judge-effort", defJudgeEffort, "v2: 判定者ごとの effort（model=low|medium|high|xhigh|max、カンマ区切り）。既定は luna・Sonnet 5.5・Haiku 5.5 とも low")
		jThinking = flag.String("judge-thinking", defJudgeThinking, "v2: 判定者ごとの拡張思考（model=adaptive|off）。既定は Sonnet 5.5・Haiku 5.5 とも adaptive（Haiku 5.5 は既定で思考するので明示して固定する）。luna には指定できない")
		gEffort   = flag.String("gen-effort", defGenEffort, "v2: 生成・書き下し・検証のモデルごとの effort（model=…）。既定は Sonnet 5.5 が medium、luna は API の既定")
		gThinking = flag.String("gen-thinking", "", "v2: 生成・書き下し・検証のモデルごとの拡張思考（model=adaptive|off）")
		topicBank = flag.String("topic-bank", "", "v2: 話題バンクのファイル（既定 <out>/topics.json）")
		p2        = flag.Bool("p2", false, "v2: P2 パイロットとして実行する（記録に pilot=true、θ を出さず補助パラメータ・QC・破棄率・例示の組と領域の ICC を報告）")
		withQT30  = flag.Bool("with-qt30", false, "v2: <out>/qt30_sources.json（-import-qt30）を転移セットとして加える")
		aifdb     = flag.String("aifdb", "../aif-with-llm/data/aifdb", "-import-qt30: aif-with-llm が取得した nodeset のディレクトリ")
	)
	flag.Parse()
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}
	po := pilotOptions{
		sources: *sources, seed: *seed, boot: *boot, lambda: *lambda, m: *margin, out: *outDir, budget: *budget, verify: *verify,
		promptVersion: *promptVer, generators: parseList(*gens), judges: parseList(*judges),
		genEffort: parseModelMap(*gEffort), genThinking: parseModelMap(*gThinking),
		judgeEffort: parseModelMap(*jEffort), judgeThinking: parseModelMap(*jThinking),
		topicBank: *topicBank, p2: *p2, withQT30: *withQT30, aifdb: *aifdb,
	}
	start := time.Now()
	var err error
	switch {
	case *doSim:
		err = runSimulate(simOptions{
			sources: *sources, seed: *seed, boot: *boot, lambda: *lambda, m: *margin, sigma: *sigma, null: *null,
			out: filepath.Join(*outDir, map[bool]string{false: "simulate.md", true: "simulate_null.md"}[*null]),
		})
	case *doTopics:
		err = runTopics(po)
	case *doQT30:
		err = runImportQT30(po)
	case *doQC:
		err = runQC(po)
	case *doPilot:
		err = runPilot(po)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "done in %s\n", time.Since(start).Round(time.Millisecond))
}
