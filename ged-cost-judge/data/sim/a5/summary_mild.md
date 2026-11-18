# Appendix A.5 summary

### Completed and not-completed seeds per world (not-completed seeds are excluded from every rate below)

| world | ok | not completed | of which timeout | of which other status | not-completed rate [CP 95%] | not-completed seeds | limit (-max-seconds, all rows) |
| --- | --: | --: | --: | --- | --- | --- | --- |
| mild (reference) | 50 | 0 | 0 | — | 0.000 [0.00, 0.07] | — | 2700 s, 6600 s |

## mild (reference): 50 seeds (101–150), sources 750, B = 200, D reps = 50, m = 0.25

code: aa1fea6 ×1, e19a2bc ×1, 80a7eff ×1, eff7cac ×1, 947add6 ×1, 4189639 ×1, e5997ed ×1, 74c6960 ×1, e1a1a02 ×1, 1aa3ef6 ×1, a505f38 ×1, 7eb7b3f ×1, 27f0f70 ×1, 08ca9a8 ×1, b1127d8 ×1, 7b0179f ×1, bd94c83 ×1, 712bca4 ×1, 2c5ec50 ×1, d2b83cf ×1, fee308c ×1, 16932d6 ×1, 73c22d4 ×1, 1dff234 ×1, 8891663 ×1, 29b0810 ×1, 046aa6e ×1, 4d715be ×1, 947ca92 ×1, b8b09f3 ×1, 6fafe65 ×1, a6cc3ee ×1, c53e516 ×1, 87adf8b ×1, 75b52f9 ×1, 398311a ×1, 6e981fc ×1, 5c71ad8 ×1, 3b0f818 ×1, 787ebff ×1, 0fd3344 ×1, b1ba438 ×2, 5bcbfe0 ×1, a3c8c55 ×2, ebae20f ×1, b8d9af5 ×1, 022319e ×1, a2f2c4c ×1; ŝ_true = 1.1304; time per seed: mean 1595 s (fit.Confirmatory 1586, diagnostic 1)

judge QC: seeds with a judge excluded 0/50; judges failing the QC (seeds): none; scale identified 50/50; matching stability mean 0.071

### Decision labels (normalized scale; combined label of the two fits, counts over seeds)

| parameter | truth | θ₀ | in band | keep | change | und-diff | und-impr | und-model | not-est | keep indep/mixed | change indep/mixed |
| --- | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: |
| I 欠落 | 1.017 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| I 余分 | 1.017 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 辺の欠落 | 1.017 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 種別 RA↔MA | 0.796 | 0.750 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 向き違い | 1.017 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 辺の余分 | 0.885 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |

### Verdicts

capped = after every cap (the deviation cap always applies to simulations: -d-reps, scaled n_p floors, QC not applied, LOJO off); uncapped = before the deviation cap, data-dependent elements only (recorded for 50/50 seeds)

| verdict | capped: count | capped: rate [CP 95%] | uncapped: count | uncapped: rate [CP 95%] |
| --- | --: | --- | --: | --- |
| validated | 0 | 0.000 [0.00, 0.07] | 48 | 0.960 [0.86, 1.00] |
| not contradicted | 50 | 1.000 [0.93, 1.00] | 2 | 0.040 [0.00, 0.14] |
| contradicted | 0 | 0.000 [0.00, 0.07] | 0 | 0.000 [0.00, 0.07] |

missing elements (seeds): non-pre-registered settings (see the deviations list) 50; upper 95% bound of D not below the bar 2

### False 'change' (parameters whose truth is inside the band)

| parameter | P(change) [CP 95%] |
| --- | --- |
| I 欠落 | 0.000 [0.00, 0.07] |
| I 余分 | 0.000 [0.00, 0.07] |
| 辺の欠落 | 0.000 [0.00, 0.07] |
| 種別 RA↔MA | 0.000 [0.00, 0.07] |
| 向き違い | 0.000 [0.00, 0.07] |
| 辺の余分 | 0.000 [0.00, 0.07] |

at least one false change (family-wise): 0.000 [0.00, 0.07]

### Interval coverage of the normalized truth, SD and bias (independence | random intercept)

| parameter | truth | 90% cov. (I) | 95% cov. (I) | mean θ̃ (I) | MC SD (I) | boot SD (I) | bias/MC SD (I) | 90% cov. (M) | 95% cov. (M) | mean θ̃ (M) | MC SD (M) | boot SD (M) | bias/MC SD (M) |
| --- | --: | --- | --- | --: | --: | --: | --: | --- | --- | --: | --: | --: | --: |
| I 欠落 | 1.017 | 0.900 [0.78, 0.97] | 0.960 [0.86, 1.00] | 1.018 | 0.020 | 0.022 | +0.05 | 0.880 [0.76, 0.95] | 0.960 [0.86, 1.00] | 1.018 | 0.020 | 0.021 | +0.06 |
| I 余分 | 1.017 | 0.840 [0.71, 0.93] | 0.900 [0.78, 0.97] | 1.016 | 0.019 | 0.018 | -0.05 | 0.820 [0.69, 0.91] | 0.880 [0.76, 0.95] | 1.017 | 0.018 | 0.018 | -0.03 |
| 置換 cos0.9 | 0.088 | 0.840 [0.71, 0.93] | 0.920 [0.81, 0.98] | 0.099 | 0.044 | 0.035 | +0.24 | 0.800 [0.66, 0.90] | 0.920 [0.81, 0.98] | 0.099 | 0.043 | 0.035 | +0.25 |
| 置換 cos0.7 | 0.177 | 0.860 [0.73, 0.94] | 0.920 [0.81, 0.98] | 0.163 | 0.069 | 0.066 | -0.20 | 0.840 [0.71, 0.93] | 0.900 [0.78, 0.97] | 0.165 | 0.070 | 0.065 | -0.17 |
| 置換 cos0.5 | 0.177 | 0.940 [0.83, 0.99] | 0.980 [0.89, 1.00] | 0.185 | 0.061 | 0.068 | +0.14 | 0.960 [0.86, 1.00] | 0.980 [0.89, 1.00] | 0.184 | 0.064 | 0.068 | +0.11 |
| 置換 cos0.3 | 0.177 | 0.900 [0.78, 0.97] | 0.960 [0.86, 1.00] | 0.169 | 0.070 | 0.067 | -0.11 | 0.880 [0.76, 0.95] | 0.960 [0.86, 1.00] | 0.170 | 0.068 | 0.068 | -0.11 |
| 辺の欠落 | 1.017 | 0.920 [0.81, 0.98] | 0.960 [0.86, 1.00] | 1.016 | 0.018 | 0.019 | -0.08 | 0.920 [0.81, 0.98] | 0.960 [0.86, 1.00] | 1.017 | 0.018 | 0.019 | -0.04 |
| 種別 RA↔MA | 0.796 | 0.840 [0.71, 0.93] | 0.960 [0.86, 1.00] | 0.799 | 0.023 | 0.021 | +0.13 | 0.840 [0.71, 0.93] | 0.920 [0.81, 0.98] | 0.799 | 0.022 | 0.021 | +0.11 |
| 向き違い | 1.017 | 0.940 [0.83, 0.99] | 0.960 [0.86, 1.00] | 1.016 | 0.018 | 0.022 | -0.07 | 0.940 [0.83, 0.99] | 0.960 [0.86, 1.00] | 1.017 | 0.020 | 0.022 | -0.04 |
| 辺の余分 | 0.885 | 0.880 [0.76, 0.95] | 0.980 [0.89, 1.00] | 0.884 | 0.021 | 0.021 | -0.02 | 0.900 [0.78, 0.97] | 0.980 [0.89, 1.00] | 0.883 | 0.020 | 0.021 | -0.07 |
| θ_neg（極性反転） | 1.327 | 0.760 [0.62, 0.87] | 0.860 [0.73, 0.94] | 1.325 | 0.073 | 0.058 | -0.02 | 0.780 [0.64, 0.88] | 0.860 [0.73, 0.94] | 1.324 | 0.068 | 0.058 | -0.05 |

ŝ (independence): mean 1.132, MC SD 0.026 (true 1.130); σ̂ (random intercept): mean 0.997

### D = agree(R2 OOF) − agree(R0), points (nested bootstrap vs Monte-Carlo SD, §16 #14)

(lower / upper = one-sided 95% bounds q0.05 / q0.95, so [lower, upper] is a two-sided 90% interval)

mean D 0.525, MC SD 0.374, mean nested-bootstrap SD 0.545 (ratio 1.455); [lower, upper] covers the across-seed mean (target 0.90): 0.980 [0.89, 1.00]; upper bound < 2 pt: 0.960 [0.86, 1.00]; lower bound ≥ 2 pt: 0.000 [0.00, 0.07]; log-loss form mean 0.001 nats/judgment

| stratum | mean D | MC SD |
| --- | --: | --: |
| T1 | -0.003 | 0.076 |
| T2 | 0.001 | 0.527 |
| T3-clear | -0.017 | 0.040 |
| T3-near | 1.926 | 1.324 |
| T4 | -0.036 | 0.144 |

### Hypotheses (supported = both fits at unadjusted α; Holm over the family computed in gcj)

| test | mean estimate (indep) | supported | Holm p < 0.05 |
| --- | --: | --- | --- |
| H1 | -1.101 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H2 | 0.000 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H4a | 0.002 | 0.100 [0.03, 0.22] | 0.060 [0.01, 0.17] |
| H4b | 0.132 | 1.000 [0.93, 1.00] | 0.960 [0.86, 1.00] |
| C1 | 3.553 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| C4 | 0.000 | 0.000 [0.00, 0.07] | 0.000 [0.00, 0.07] |

### Optimizer: full-data fits and bootstrap replicates

| fit | converged | cycled | capped (20 rounds) | valid (§11.2) | mean cycle width (cycled fits) | any cost at boundary | mean start spread | boot: cycled share | boot: capped share | boot: invalid (§11.2) | boot: not converged (older metric) | boot errors |
| --- | --- | --- | --- | --- | --: | --- | --: | --: | --: | --: | --: | --: |
| indep | 0.980 [0.89, 1.00] | 0.020 [0.00, 0.11] | 0.000 [0.00, 0.07] | 1.000 [0.93, 1.00] | 0.011 | 0.020 [0.00, 0.11] | 0.977 | 0.012 | 0.000 | 0.000 | 0.012 | 0 |
| mixed | 0.960 [0.86, 1.00] | 0.040 [0.00, 0.14] | 0.000 [0.00, 0.07] | 1.000 [0.93, 1.00] | 0.020 | 0.000 [0.00, 0.07] | 1.898 | 0.015 | 0.000 | 0.000 | 0.015 | 0 |

bootstrap boundary share (independence, mean over seeds incl. zeros): 置換 cos0.3 0.045, 置換 cos0.5 0.021, 置換 cos0.7 0.052, 置換 cos0.9 0.052

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
| 種別 CA↔MA | 0.900 | 0.796 | 0.778 | 0.068 | -0.021 | — | 104 |
| 種別 RA↔CA | 0.900 | 0.796 | 0.807 | 0.061 | 0.008 | — | 103 |
