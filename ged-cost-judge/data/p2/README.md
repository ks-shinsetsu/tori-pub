# P2 pilot records: synthetic discussions, variants and judgments

The records of the three P2 pilot runs (25 sources each, `-seed 2`) and of one run at 100 sources. Everything here was written by language
models from the prompts in `../../prompts/`; no corpus text is included (the runs did not use `-with-qt30`).
The discussions are kept because they can serve as a dataset later, and because they are what the numbers in
the reports were computed from.

| Path | Content |
| --- | --- |
| `topics.json` | The topic bank (G0): 1,184 questions in 12 domains. The same file served both rounds |
| `round1/` | First pilot, code `a5a49ca`: prompts `synth_source_v2`, `realize_v3`, `verify_v2`, `verify_source_v1` |
| `round2/` | Second pilot, code `ea8dea4`: prompts `synth_source_v3`, `realize_v4`, `verify_v3`, `verify_source_v2` (the shared IAT definitions) |
| `round3/` | Third pilot, code `fe85aed`: the prompts of round 2; the collateral check samples only relations the verifier read in the source; R-type RA→MA, R-add MA and t3_diag are out of the catalogue. Its sources are the discussions of round 2, replayed from the response cache |
| `scale100/` | The same pipeline at `-sources 100` (code `b63b913`: as round 3, with the label audit's criterion printed as 80%). A trial of scale before the main run; all text was generated anew |
| `sources100/` | Source generation only at `-sources 100` (code `640d23a`: prompt `synth_source_v4`, and a retried source is told why the last attempt was rejected). The run was stopped on purpose when the sources were done, so there are no variants or judgments: `pilot_sources.jsonl` (109 records, 100 accepted) and the report of the stopped run |

In each round:

| File | Content |
| --- | --- |
| `pilot_sources.jsonl` | One line per source attempt slot (accepted and rejected): the discussion (utterances, propositions, relations), the generator, the checks of every attempt and the verifier's blind re-annotation (`g1_annotation`) |
| `pilot_triplets.jsonl` | One line per triplet slot (kept and discarded): the edit plans, the variants A and B (text and graph), every realization attempt with the verifier's reading, and for kept triplets the judgments of the three judges in both presentation orders |
| `pilot.md` | The report of the run (`gcj -pilot -p2`): provenance, cost, acceptance, first-pass rates, judge diagnostics |
| `audit.md` | The report of `gcj -audit` on that run's records |
| `round2/audit_round1_texts.md` | `gcj -audit` of the round-1 records with the round-2 verifier prompt (same texts, new definitions) |

| | Round 1 | Round 2 | Round 3 | Scale 100 |
| --- | --: | --: | --: | --: |
| Source records / accepted | 35 / 25 | 33 / 25 | 33 / 25 | 138 / 95 |
| Triplet slots / kept | 173 / 92 | 158 / 99 | 119 / 99 | 470 / 375 |
| Judgments | 552 | 594 | 594 | 2,250 |

Models: sources and variants by `gpt-5.6-luna` and `claude-sonnet-5-5`; verifier `claude-haiku-5-5`; judges
`gpt-5.6-luna`, `claude-sonnet-5-5`, `claude-haiku-5-5`. Settings and prompt hashes are in each `pilot.md`.

## Limits

- These are pilot runs. All of them failed gates of the P2 plan (first-pass realization rate, discard rate, label
  audit, source acceptance); see each `pilot.md` and `audit.md`. Use the records with that in mind.
- The labels (which edit a variant carries) are the writer's plan, accepted when one model (`claude-haiku-5-5`)
  read the edit back. No human checked them. The audit read 87.9% (round 1), 84.7% (round 2), 84.4% (round 3) and 85.7%
  (scale 100) of the checked edits as intended, and the same verifier recovered 73% and 93% of the relations of the hand-made exemplars.
- A source was accepted when the verifier recovered at least 65% of its relations, so a source graph contains
  relations that the verifier did not read in the text. `g1_annotation` says which.
- Discarded slots and rejected sources are included, with the reason. Filter on `accepted` (sources) and on
  the presence of `judgments` (triplets) for the kept set.
- The runs were made on linux/amd64. The response cache (`.cache/`) is not published, so the same command
  regenerates new text, not these records.
- Names of speakers come from a fixed pool of given names; the people and the settings are invented.

---

# P2 パイロットの記録: 合成の議論、変種、判定

P2 パイロット 3 回分（元の議論 25 件ずつ、`-seed 2`）と、100 件での実行 1 回分の記録。すべて `../../prompts/` のプロンプトから言語モデルが
書いたもので、コーパスの本文は含まない（`-with-qt30` は使っていない）。後でデータセットとして使えること、
各回の報告の数値の出どころであることから、ここに置く。

| パス | 内容 |
| --- | --- |
| `topics.json` | 話題バンク（G0）。12 領域、1,184 件の問い。2 回とも同じファイルを使った |
| `round1/` | 第 1 回。コード `a5a49ca`、プロンプト `synth_source_v2`・`realize_v3`・`verify_v2`・`verify_source_v1` |
| `round2/` | 第 2 回。コード `ea8dea4`、プロンプト `synth_source_v3`・`realize_v4`・`verify_v3`・`verify_source_v2`（IAT の定義を共通化） |
| `round3/` | 第 3 回。コード `fe85aed`、プロンプトは第 2 回と同じ。触れていない関係の抜き取りを、元の議論で検証者が読めた関係に限った。R-type RA→MA、R-add MA、t3_diag をカタログから外した。元の議論は第 2 回のものを応答のキャッシュから再生した |
| `scale100/` | 同じパイプラインを `-sources 100` で実行したもの（コード `b63b913`。第 3 回と同じで、ラベルの監査の基準の表示だけ 80%）。本番の前の規模の試行。文章はすべて新しく生成した |
| `sources100/` | 元の議論の生成だけを `-sources 100` で実行したもの（コード `640d23a`。プロンプト `synth_source_v4`、やり直しに前回の不採用の理由を伝える）。元の議論がそろった時点で意図して止めたので、変種と判定は無い。`pilot_sources.jsonl`（109 件、受理 100 件）と、止めた実行の報告 |

各回のファイル:

| ファイル | 内容 |
| --- | --- |
| `pilot_sources.jsonl` | 元の議論の枠ごとに 1 行（受理・不採用とも）。議論（発話、命題、関係）、生成モデル、各試行の検査、検証者の盲検の再注釈（`g1_annotation`） |
| `pilot_triplets.jsonl` | 三つ組の枠ごとに 1 行（採用・破棄とも）。編集計画、変種 A・B（文章とグラフ）、書き下しの各試行と検証者の読み、採用分は 3 判定者 × 提示順 2 の判定 |
| `pilot.md` | 実行の報告（`gcj -pilot -p2`）。来歴、費用、受理、1 回目の合格率、判定者の診断 |
| `audit.md` | その回の記録に対する `gcj -audit` の報告 |
| `round2/audit_round1_texts.md` | 第 1 回の記録を第 2 回の検証者プロンプトで監査した報告（同じ文章、新しい定義） |

| | 第 1 回 | 第 2 回 | 第 3 回 | 100 件 |
| --- | --: | --: | --: | --: |
| 元の議論の記録 / 受理 | 35 / 25 | 33 / 25 | 33 / 25 | 138 / 95 |
| 三つ組の枠 / 採用 | 173 / 92 | 158 / 99 | 119 / 99 | 470 / 375 |
| 判定 | 552 | 594 | 594 | 2,250 |

モデル: 元の議論と変種は `gpt-5.6-luna` と `claude-sonnet-5-5`、検証者は `claude-haiku-5-5`、判定者は
`gpt-5.6-luna`・`claude-sonnet-5-5`・`claude-haiku-5-5`。設定とプロンプトのハッシュは各回の `pilot.md` にある。

## 限界

- パイロットの記録である。どの回も P2 の関門（書き下しの 1 回目の合格率、破棄率、ラベルの監査、元の議論の
  受理率）を通っていない。各回の `pilot.md` と `audit.md` を見たうえで使うこと。
- ラベル（変種がどの編集を持つか）は書き手の計画で、1 つのモデル（`claude-haiku-5-5`）がその編集を読み取れた
  ときに採用している。人手の確認はしていない。監査で意図どおり読めた編集は 87.9%（第 1 回）、84.7%（第 2 回）、84.4%（第 3 回）、85.7%（100 件）、
  同じ検証者が手作りの例示の関係を再現できた割合は 73% と 93% だった。
- 元の議論は、検証者が関係の 65% 以上を再現できれば受理している。このため元の議論のグラフには、検証者が
  文章から読めなかった関係が残っている。どの関係かは `g1_annotation` で分かる。
- 破棄した枠と不採用の元の議論も、理由つきで含めている。採用分だけを使うには、元の議論は `accepted`、
  三つ組は `judgments` の有無で絞る。
- 実行環境は linux/amd64。応答のキャッシュ（`.cache/`）は公開していないので、同じコマンドを実行しても
  新しい文章が生成され、この記録は再現されない。
- 話者の名前は固定の名前の一覧から取っている。人物も場面も架空である。
