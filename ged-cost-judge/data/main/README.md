# Main run records (750 sources, seed 1) and an exploratory re-estimation

The records of the main run of `gcj -pilot -sources 750 -seed 1` (2026-11-15 to 16) and two analyses of them.
Everything here was written by language models from the prompts in `../../prompts/`; no corpus text is included
(the run did not use `-with-qt30`).

| Path | Content |
| --- | --- |
| `pilot_sources.jsonl` | One line per source attempt slot: 807 records, 750 accepted |
| `pilot_triplets.jsonl.gz` | One line per triplet slot: 3,644 slots, 2,979 kept, with 17,874 judgments. Gzip of the 104 MB JSONL (GitHub does not take files over 100 MB); `gunzip -k` restores it |
| `pilot.md` | The report of the run under the pre-registered procedure (code `838a672`): provenance, cost, acceptance, first-pass rates, judge QC, the confirmatory analysis with B = 1000, D and the leave-one-judge-out refits |
| `audit.md` | The report of `gcj -audit` on the records |
| `profile.csv` | Profile likelihoods of the independence fit of `pilot.md` |
| `descent-b200/confirm.md` | An exploratory re-estimation on the same records (code `63b8d37`): `fit.Confirmatory` with the outer loop `OuterDescent`, B = 200, without D and the leave-one-judge-out refits |
| `descent-b200/replicates.jsonl` | Per fit of that re-estimation: estimates, bootstrap quantiles, the objective of every start, and the shares of replicates on the boundary, stalled or invalid |

Models: sources and variants by `gpt-5.6-luna` and `claude-sonnet-5-5`; verifier `claude-haiku-5-5`; judges
`gpt-5.6-luna`, `claude-sonnet-5-5`, `claude-haiku-5-5`. Prompt hashes and settings are in `pilot.md`.

## What the two analyses say

| | `pilot.md` (pre-registered procedure) | `descent-b200/confirm.md` (exploratory) |
| --- | --- | --- |
| Random-intercept fit, NLL | 11319.0 | 10752.6 |
| Independence fit, NLL | 11919.2 | 11878.6 |
| Largest gap between the two fits over the six normalized costs | 1.44 | 0.12 |
| Scale identified | no: every cost "not estimable" | yes |
| Verdict on the current table | contradicted (D = 6.91 pt, lower bound 4.52) | contradicted before the deviation cap (D not computed) |

Under the pre-registered procedure the random-intercept fit returned a fixed point of the alternation whose
likelihood is worse than that of rounds it had visited, with the two node costs at 0; 72% and 80% of its bootstrap
replicates sat on that boundary, which makes the scale unidentified by the rule of decisions §3. `OuterDescent`
(see `internal/fit/fit.go`, `fitDescent`) returns the best point visited and descends from it.

Decisions of the exploratory re-estimation (normalized scale, θ₀ the current value, independence / random intercept):

| Cost | θ₀ | Estimate | 90% interval (independence) | Decision |
| --- | --: | --- | --- | --- |
| I-missing | 1.00 | 0.44 / 0.37 | [0.34, 0.51] | change |
| I-extra | 1.00 | 0.44 / 0.36 | [0.36, 0.50] | change |
| edge-missing | 1.00 | 1.05 / 1.05 | [0.98, 1.13] | keep |
| type RA↔MA | 0.75 | 0.95 / 1.07 | [0.88, 1.03] | undecided (model-sensitive) |
| direction | 1.00 | 1.40 / 1.45 | [1.31, 1.51] | change |
| edge-extra | 1.00 | 1.47 / 1.44 | [1.40, 1.53] | change |

## Limits

- **The re-estimation is exploratory.** The outer loop was written after the pre-registered result was seen, to
  repair it. Its report lists the deviations (outer loop, B = 200, no D, no leave-one-judge-out), and the
  pre-registered result is `pilot.md`. The decisions above need a new pre-registration and new data before they
  are used as values.
- **The returned point is the best one the search found, not a certified minimum.** The fits depend on the start
  (largest difference between starts 9.05 and 8.99 on the anchored scale; one start of each fit stays at a much
  worse point), and the reported fit is the best of five starts. The start objectives are in
  `descent-b200/replicates.jsonl`.
- **The bootstrap intervals of the random-intercept fit are probably too narrow.** 96% of its replicates (79% for
  the independence fit) ended as a stall of the descent, most of them near the warm start; several of its intervals
  have the estimate at one end (edge-extra 1.44 in [1.44, 1.53]). The independence intervals are the ones shown above.
- B = 200, not 1000: at the measured 15 CPU-minutes per replicate the full analysis would have taken 40 to 70 hours
  on 32 vCPUs. D, the leave-one-judge-out refits and the profile likelihoods were not run under `OuterDescent`.
- The outer loop was checked on synthetic data: the simulator recovers known costs under it as under the
  alternation (9 runs at 750 sources), and five rounds of code review found no remaining counterexample on small
  kink cases (12 to 25 sources). Its behaviour at 750 sources on real judgments rests on this run alone.
- The run failed four gates of the P2 plan: first-pass realization rate (1 of 48 cells at 80%; 47.9% overall),
  final discard rate 18.2% (≤ 10%), exemplar copies (83 detected, 6 sources rejected for it at the last attempt),
  and one uninstructed polarity flip in the label audit. The label audit read 92.7% of the checked edits as intended.
- Judges are language models only; no human judged a triplet or checked a label.
- The records were written in three passes of the same command (the first stopped on an exhausted API credit, the
  second was stopped by hand during the estimation). Failed responses are not cached, so a rerun asks for them
  again: five triplet slots differ between the second and the third pass. These are the records of the third.
- The response cache is not published, so the same command regenerates new text, not these records.
- Names of speakers come from a fixed pool of given names; the people and the settings are invented.

---

# 本番の記録（元の議論 750 件、seed 1）と、探索的な再推定

`gcj -pilot -sources 750 -seed 1`（2026-11-15〜16）の本番の記録と、それに対する 2 つの分析。すべて
`../../prompts/` のプロンプトから言語モデルが書いたもので、コーパスの本文は含まない（`-with-qt30` は使っていない）。

| パス | 内容 |
| --- | --- |
| `pilot_sources.jsonl` | 元の議論の枠ごとに 1 行。807 件、受理 750 件 |
| `pilot_triplets.jsonl.gz` | 三つ組の枠ごとに 1 行。3,644 枠、採用 2,979、判定 17,874 件。104 MB の JSONL を gzip したもの（GitHub は 100 MB を超えるファイルを受け付けない）。`gunzip -k` で戻る |
| `pilot.md` | 事前登録の手続きでの実行の報告（コード `838a672`）。来歴、費用、受理、1 回目の合格率、判定者の品質管理、確認的分析（B = 1000、D、判定者を 1 人ずつ除いた判断） |
| `audit.md` | 記録に対する `gcj -audit` の報告 |
| `profile.csv` | `pilot.md` の作業独立の当てはめのプロファイル尤度 |
| `descent-b200/confirm.md` | 同じ記録に対する探索的な再推定（コード `63b8d37`）。外側のループを `OuterDescent` にした `fit.Confirmatory`、B = 200、D と判定者を除く計算なし |
| `descent-b200/replicates.jsonl` | その再推定の当てはめごとのまとめ。推定値、ブートストラップの分位点、各開始点の目的関数、境界・停留・無効の反復の割合 |

モデル: 元の議論と変種は `gpt-5.6-luna` と `claude-sonnet-5-5`、検証者は `claude-haiku-5-5`、判定者は
`gpt-5.6-luna`・`claude-sonnet-5-5`・`claude-haiku-5-5`。プロンプトのハッシュと設定は `pilot.md` にある。

## 2 つの分析の結果

| | `pilot.md`（事前登録の手続き） | `descent-b200/confirm.md`（探索的） |
| --- | --- | --- |
| ランダム切片の当てはめの NLL | 11319.0 | 10752.6 |
| 作業独立の当てはめの NLL | 11919.2 | 11878.6 |
| 正規化した 6 個のコストでの、2 つの当てはめの差の最大 | 1.44 | 0.12 |
| 尺度の同定 | できない。全コストが「推定不能」 | できた |
| 現行表の判定 | 否定（D = 6.91 pt、下限 4.52） | 逸脱による制限の前は否定（D は未計算） |

事前登録の手続きでは、ランダム切片の当てはめが交互最適化の不動点を返した。その尤度は途中で通った回より悪く、
命題の 2 つのコストが 0 だった。ブートストラップの反復の 72% と 80% がその境界に張り付き、決定文書 §3 の規則で
尺度が同定できなくなった。`OuterDescent`（`internal/fit/fit.go` の `fitDescent`）は、通った中で最良の点を返し、
そこから降下する。

探索的な再推定の判断（正規化した尺度、θ₀ は現行値、作業独立 / ランダム切片）:

| コスト | θ₀ | 推定値 | 90% 区間（作業独立） | 判断 |
| --- | --: | --- | --- | --- |
| I 欠落 | 1.00 | 0.44 / 0.37 | [0.34, 0.51] | 変更 |
| I 余分 | 1.00 | 0.44 / 0.36 | [0.36, 0.50] | 変更 |
| 辺の欠落 | 1.00 | 1.05 / 1.05 | [0.98, 1.13] | 維持 |
| 種別 RA↔MA | 0.75 | 0.95 / 1.07 | [0.88, 1.03] | 未決（モデル依存） |
| 向き違い | 1.00 | 1.40 / 1.45 | [1.31, 1.51] | 変更 |
| 辺の余分 | 1.00 | 1.47 / 1.44 | [1.40, 1.53] | 変更 |

## 限界

- **再推定は探索的な分析である。** 外側のループは、事前登録の結果を見た後に、それを直すために書いた。報告には
  逸脱（外側のループ、B = 200、D なし、判定者を除く計算なし）が記録されており、事前登録の結果は `pilot.md` である。
  上の判断を値として使うには、新しい事前登録と新しいデータが要る。
- **返る点は探索が見つけた最良の点であり、極小の保証ではない。** 当てはめは開始点に依存する（開始点の間の最大の
  ずれは固定尺度で 9.05 と 8.99。どちらの当てはめにも、ずっと悪い点に残る開始点が 1 つある）。報告の当てはめは
  5 つの開始点のうち最良のもの。各開始点の目的関数は `descent-b200/replicates.jsonl` にある。
- **ランダム切片の当てはめのブートストラップ区間は、おそらく狭すぎる。** その反復の 96%（作業独立は 79%）が
  降下の停留で終わり、多くはウォームスタートの近くにある。区間の端に推定値が来ているものがある
  （辺の余分 1.44 に対して [1.44, 1.53]）。上の表の区間は作業独立のもの。
- B は 1000 ではなく 200。反復 1 回に 15 CPU 分（実測）かかり、全体の分析は 32 vCPU で 40〜70 時間になる見込み
  だった。D、判定者を 1 人ずつ除いた判断、プロファイル尤度は `OuterDescent` では計算していない。
- 外側のループは合成データで確かめた。シミュレータは、このループでも交互最適化と同じく既知のコストを復元した
  （750 件で 9 本）。5 巡のコードレビューで、小さな折れ点の例（12〜25 件）には反例が残らなかった。750 件の
  実際の判定での振る舞いは、この 1 回の実行だけが根拠である。
- 実行は P2 の関門のうち 4 つを通っていない。書き下しの 1 回目の合格率（48 セル中 1 が 80% 以上、全体 47.9%）、
  最終の破棄率 18.2%（基準 10% 以下）、例示の写し（検出 83、最後の試行で不採用 6）、ラベルの監査での
  指示していない極性の反転 1。ラベルの監査では、確かめた編集の 92.7% が意図どおりに読めた。
- 判定者は言語モデルだけ。人は三つ組を判定しておらず、ラベルも確認していない。
- 記録は同じコマンドの 3 回の実行で書かれた（1 回目は API のクレジット切れで停止、2 回目は推定の途中で手動で
  停止）。失敗した応答はキャッシュされないので、再実行のたびに聞き直す。2 回目と 3 回目で三つ組の 5 枠が違う。
  ここにあるのは 3 回目の記録。
- 応答のキャッシュは公開していないので、同じコマンドを実行しても新しい文章が生成され、この記録は再現されない。
- 話者の名前は固定の名前の一覧から取っている。人物も場面も架空である。
