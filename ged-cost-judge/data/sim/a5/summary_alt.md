# Appendix A.5 summary

### Completed and not-completed seeds per world (not-completed seeds are excluded from every rate below)

| world | ok | not completed | of which timeout | of which other status | not-completed rate [CP 95%] | not-completed seeds | limit (-max-seconds, all rows) |
| --- | --: | --: | --: | --- | --- | --- | --- |
| alt (reference) | 50 | 0 | 0 | — | 0.000 [0.00, 0.07] | — | 2700 s |

## alt (reference): 50 seeds (101–150), sources 750, B = 200, D reps = 50, m = 0.25

code: 219e346 ×1, 0c55b36 ×1, 988b29c ×1, 8f41011 ×1, 38242de ×1, d20d362 ×1, d1e0248 ×1, 5b29123 ×1, e8dd784 ×1, eaff03d ×1, 7e2fe0f ×1, d68c25e ×1, c8b43bd ×1, 19966d7 ×1, 51fd474 ×1, 8c6b584 ×1, 8658e49 ×1, 9283276 ×1, d022746 ×1, 22a0a9b ×1, 2e6bc54 ×1, 1bd87de ×1, f036cb3 ×1, cd6c873 ×1, 7632d83 ×1, a4ec9f0 ×1, 852dddd ×1, bf2ad45 ×1, 2964536 ×1, d01277d ×1, 5ced101 ×1, 7ed8baf ×1, df9fb89 ×1, 8a7d808 ×1, d8d22ea ×1, 8b32f01 ×1, 420c8e3 ×1, 23463de ×1, dfb1fff ×1, d49dc4f ×1, cd68273 ×1, b623319 ×1, 2b27535 ×1, a9d3e3e ×1, 9487df8 ×1, 633001c ×1, 5539bda ×1, fcee20d ×1, 8e52d52 ×1, b430f3e ×1; ŝ_true = 1.1217; time per seed: mean 1325 s (fit.Confirmatory 1317, diagnostic 1)

judge QC: seeds with a judge excluded 0/50; judges failing the QC (seeds): none; scale identified 50/50; matching stability mean 0.073

### Decision labels (normalized scale; combined label of the two fits, counts over seeds)

| parameter | truth | θ₀ | in band | keep | change | und-diff | und-impr | und-model | not-est | keep indep/mixed | change indep/mixed |
| --- | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: |
| I 欠落 | 1.159 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| I 余分 | 0.624 | 1.000 | **no** | 0 | 50 | 0 | 0 | 0 | 0 | 0/0 | 50/50 |
| 辺の欠落 | 1.070 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 種別 RA↔MA | 0.936 | 0.750 | yes | 42 | 0 | 5 | 0 | 3 | 0 | 43/44 | 0/0 |
| 向き違い | 1.070 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 辺の余分 | 0.891 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |

### Verdicts

capped = after every cap (the deviation cap always applies to simulations: -d-reps, scaled n_p floors, QC not applied, LOJO off); uncapped = before the deviation cap, data-dependent elements only (recorded for 50/50 seeds)

| verdict | capped: count | capped: rate [CP 95%] | uncapped: count | uncapped: rate [CP 95%] |
| --- | --: | --- | --: | --- |
| validated | 0 | 0.000 [0.00, 0.07] | 0 | 0.000 [0.00, 0.07] |
| not contradicted | 0 | 0.000 [0.00, 0.07] | 0 | 0.000 [0.00, 0.07] |
| contradicted | 50 | 1.000 [0.93, 1.00] | 50 | 1.000 [0.93, 1.00] |

missing elements (seeds): I 余分 50; upper 95% bound of D not below the bar 50; non-pre-registered settings (see the deviations list) 50; 種別 RA↔MA 8

### False 'change' (parameters whose truth is inside the band)

| parameter | P(change) [CP 95%] |
| --- | --- |
| I 欠落 | 0.000 [0.00, 0.07] |
| 辺の欠落 | 0.000 [0.00, 0.07] |
| 種別 RA↔MA | 0.000 [0.00, 0.07] |
| 向き違い | 0.000 [0.00, 0.07] |
| 辺の余分 | 0.000 [0.00, 0.07] |

at least one false change (family-wise): 0.000 [0.00, 0.07]

### Interval coverage of the normalized truth, SD and bias (independence | random intercept)

| parameter | truth | 90% cov. (I) | 95% cov. (I) | mean θ̃ (I) | MC SD (I) | boot SD (I) | bias/MC SD (I) | 90% cov. (M) | 95% cov. (M) | mean θ̃ (M) | MC SD (M) | boot SD (M) | bias/MC SD (M) |
| --- | --: | --- | --- | --: | --: | --: | --: | --- | --- | --: | --: | --: | --: |
| I 欠落 | 1.159 | 0.960 [0.86, 1.00] | 0.980 [0.89, 1.00] | 1.159 | 0.020 | 0.023 | +0.02 | 0.920 [0.81, 0.98] | 0.960 [0.86, 1.00] | 1.160 | 0.020 | 0.022 | +0.07 |
| I 余分 | 0.624 | 0.880 [0.76, 0.95] | 0.960 [0.86, 1.00] | 0.625 | 0.020 | 0.020 | +0.04 | 0.860 [0.73, 0.94] | 0.940 [0.83, 0.99] | 0.623 | 0.020 | 0.020 | -0.05 |
| 置換 cos0.9 | 0.089 | 0.880 [0.76, 0.95] | 0.960 [0.86, 1.00] | 0.098 | 0.041 | 0.036 | +0.21 | 0.860 [0.73, 0.94] | 0.940 [0.83, 0.99] | 0.098 | 0.040 | 0.036 | +0.22 |
| 置換 cos0.7 | 0.178 | 0.860 [0.73, 0.94] | 0.880 [0.76, 0.95] | 0.171 | 0.072 | 0.067 | -0.09 | 0.860 [0.73, 0.94] | 0.900 [0.78, 0.97] | 0.173 | 0.073 | 0.067 | -0.07 |
| 置換 cos0.5 | 0.178 | 0.960 [0.86, 1.00] | 0.980 [0.89, 1.00] | 0.180 | 0.066 | 0.069 | +0.02 | 0.960 [0.86, 1.00] | 0.980 [0.89, 1.00] | 0.180 | 0.067 | 0.069 | +0.02 |
| 置換 cos0.3 | 0.178 | 0.920 [0.81, 0.98] | 0.940 [0.83, 0.99] | 0.167 | 0.068 | 0.069 | -0.17 | 0.940 [0.83, 0.99] | 0.960 [0.86, 1.00] | 0.166 | 0.065 | 0.068 | -0.19 |
| 辺の欠落 | 1.070 | 0.940 [0.83, 0.99] | 0.980 [0.89, 1.00] | 1.068 | 0.019 | 0.020 | -0.08 | 0.940 [0.83, 0.99] | 0.980 [0.89, 1.00] | 1.069 | 0.018 | 0.020 | -0.05 |
| 種別 RA↔MA | 0.936 | 0.920 [0.81, 0.98] | 1.000 [0.93, 1.00] | 0.936 | 0.021 | 0.022 | +0.02 | 0.920 [0.81, 0.98] | 1.000 [0.93, 1.00] | 0.937 | 0.021 | 0.022 | +0.04 |
| 向き違い | 1.070 | 0.960 [0.86, 1.00] | 0.980 [0.89, 1.00] | 1.069 | 0.017 | 0.023 | -0.02 | 0.940 [0.83, 0.99] | 0.960 [0.86, 1.00] | 1.070 | 0.018 | 0.023 | +0.01 |
| 辺の余分 | 0.891 | 0.960 [0.86, 1.00] | 1.000 [0.93, 1.00] | 0.892 | 0.020 | 0.022 | +0.01 | 0.960 [0.86, 1.00] | 0.980 [0.89, 1.00] | 0.891 | 0.020 | 0.021 | -0.02 |
| θ_neg（極性反転） | 1.337 | 0.820 [0.69, 0.91] | 0.880 [0.76, 0.95] | 1.329 | 0.069 | 0.060 | -0.11 | 0.820 [0.69, 0.91] | 0.900 [0.78, 0.97] | 1.329 | 0.066 | 0.058 | -0.12 |

ŝ (independence): mean 1.122, MC SD 0.025 (true 1.122); σ̂ (random intercept): mean 0.991

### D = agree(R2 OOF) − agree(R0), points (nested bootstrap vs Monte-Carlo SD, §16 #14)

(lower / upper = one-sided 95% bounds q0.05 / q0.95, so [lower, upper] is a two-sided 90% interval)

mean D 4.455, MC SD 0.536, mean nested-bootstrap SD 0.712 (ratio 1.329); [lower, upper] covers the across-seed mean (target 0.90): 0.960 [0.86, 1.00]; upper bound < 2 pt: 0.000 [0.00, 0.07]; lower bound ≥ 2 pt: 1.000 [0.93, 1.00]; log-loss form mean 0.025 nats/judgment

| stratum | mean D | MC SD |
| --- | --: | --: |
| T1 | -0.010 | 0.053 |
| T2 | -0.033 | 0.522 |
| T3-clear | 0.332 | 0.310 |
| T3-near | 15.176 | 1.967 |
| T4 | -0.022 | 0.134 |

### Hypotheses (supported = both fits at unadjusted α; Holm over the family computed in gcj)

| test | mean estimate (indep) | supported | Holm p < 0.05 |
| --- | --: | --- | --- |
| H1 | -1.024 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H2 | 0.001 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H4a | 0.534 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H4b | 0.177 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| C1 | 1.330 | 0.640 [0.49, 0.77] | 0.460 [0.32, 0.61] |
| C4 | 0.000 | 0.000 [0.00, 0.07] | 0.000 [0.00, 0.07] |

### Optimizer: full-data fits and bootstrap replicates

| fit | converged | cycled | capped (20 rounds) | valid (§11.2) | mean cycle width (cycled fits) | any cost at boundary | mean start spread | boot: cycled share | boot: capped share | boot: invalid (§11.2) | boot: not converged (older metric) | boot errors |
| --- | --- | --- | --- | --- | --: | --- | --: | --: | --: | --: | --: | --: |
| indep | 0.980 [0.89, 1.00] | 0.020 [0.00, 0.11] | 0.000 [0.00, 0.07] | 1.000 [0.93, 1.00] | 0.003 | 0.020 [0.00, 0.11] | 0.717 | 0.014 | 0.000 | 0.000 | 0.014 | 0 |
| mixed | 0.980 [0.89, 1.00] | 0.020 [0.00, 0.11] | 0.000 [0.00, 0.07] | 1.000 [0.93, 1.00] | 0.001 | 0.040 [0.00, 0.14] | 2.766 | 0.016 | 0.000 | 0.000 | 0.016 | 0 |

bootstrap boundary share (independence, mean over seeds incl. zeros): 置換 cos0.3 0.048, 置換 cos0.5 0.032, 置換 cos0.7 0.047, 置換 cos0.9 0.047

### Informative triplets n_p in the fit set (§15.3)

| parameter | minimum | min | mean | max | seeds below |
| --- | --: | --: | --: | --: | --: |
| I 余分 | 650 | 734 | 792 | 845 | 0 |
| I 欠落 | 650 | 842 | 890 | 939 | 0 |
| 向き違い | 650 | 688 | 751 | 805 | 0 |
| 種別 RA↔MA | 450 | 797 | 844 | 899 | 0 |
| 置換 cos0.3 | — | 617 | 657 | 695 | — |
| 置換 cos0.5 | — | 765 | 815 | 854 | — |
| 置換 cos0.7 | — | 1263 | 1319 | 1391 | — |
| 置換 cos0.9 | — | 1344 | 1399 | 1466 | — |
| 辺の余分 | 650 | 769 | 843 | 894 | 0 |
| 辺の欠落 | 650 | 1162 | 1217 | 1264 | 0 |
| θ_neg (NegDiff ≠ 0) | 300 | 317 | 359 | 388 | 0 |

### Untied diagnostic fit (§12b; diag_ca_type included)

| pair | truth (anchored) | truth (normalized) | mean estimate | MC SD | mean contrast vs RA↔MA | 90% cov. | relabels |
| --- | --: | --: | --: | --: | --: | --: | --: |
| 種別 CA↔MA | 0.750 | 0.669 | 0.654 | 0.070 | -0.283 | — | 104 |
| 種別 RA↔CA | 1.500 | 1.337 | 1.340 | 0.075 | 0.403 | — | 103 |
