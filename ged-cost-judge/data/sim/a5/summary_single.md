# Appendix A.5 summary

### Completed and not-completed seeds per world (not-completed seeds are excluded from every rate below)

| world | ok | not completed | of which timeout | of which other status | not-completed rate [CP 95%] | not-completed seeds | limit (-max-seconds, all rows) |
| --- | --: | --: | --: | --- | --- | --- | --- |
| single (reference) | 50 | 0 | 0 | — | 0.000 [0.00, 0.07] | — | 2700 s |

## single (reference): 50 seeds (101–150), sources 750, B = 200, D reps = 50, m = 0.25

code: 73c96a9 ×1, 8583431 ×1, a2ffb0e ×1, c8c853f ×1, 8a40669 ×1, 5bfddf8 ×1, 6031106 ×1, d29ce83 ×1, 0bcc3af ×1, 9bf281e ×1, 08b048b ×1, bce8067 ×1, a1eeed2 ×1, 0a2cf5b ×1, fdbe7fe ×1, 3c9d735 ×1, 805a1e0 ×1, d0e735c ×1, 7c35385 ×1, 1bfb837 ×1, b2b6783 ×1, 771c5f6 ×1, 3060c51 ×1, d3f7078 ×1, c2ee23d ×1, f6861a6 ×1, 7cbd074 ×1, 2e2a8c0 ×1, 289af30 ×1, cbd9753 ×1, b96bdc1 ×1, c6b540c ×1, 264db8d ×1, bc91d19 ×1, 014db58 ×1, 8fd5668 ×1, 697c48e ×1, 5dd1308 ×1, 342d465 ×1, a232910 ×1, 440bdea ×1, 57eec56 ×1, 70e78f1 ×1, cbaab9f ×1, 5115fdd ×1, 1bb34c0 ×1, 4084fa6 ×1, e47981b ×1, 73b8e1e ×1, 5beec09 ×1; ŝ_true = 1.0522; time per seed: mean 1315 s (fit.Confirmatory 1307, diagnostic 1)

judge QC: seeds with a judge excluded 0/50; judges failing the QC (seeds): none; scale identified 50/50; matching stability mean 0.071

### Decision labels (normalized scale; combined label of the two fits, counts over seeds)

| parameter | truth | θ₀ | in band | keep | change | und-diff | und-impr | und-model | not-est | keep indep/mixed | change indep/mixed |
| --- | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: |
| I 欠落 | 1.236 | 1.000 | yes | 6 | 0 | 44 | 0 | 0 | 0 | 6/6 | 0/0 |
| I 余分 | 0.950 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 辺の欠落 | 0.950 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 種別 RA↔MA | 0.713 | 0.750 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 向き違い | 0.950 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 辺の余分 | 0.950 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |

### Verdicts

capped = after every cap (the deviation cap always applies to simulations: -d-reps, scaled n_p floors, QC not applied, LOJO off); uncapped = before the deviation cap, data-dependent elements only (recorded for 50/50 seeds)

| verdict | capped: count | capped: rate [CP 95%] | uncapped: count | uncapped: rate [CP 95%] |
| --- | --: | --- | --: | --- |
| validated | 0 | 0.000 [0.00, 0.07] | 3 | 0.060 [0.01, 0.17] |
| not contradicted | 50 | 1.000 [0.93, 1.00] | 47 | 0.940 [0.83, 0.99] |
| contradicted | 0 | 0.000 [0.00, 0.07] | 0 | 0.000 [0.00, 0.07] |

missing elements (seeds): non-pre-registered settings (see the deviations list) 50; I 欠落 44; upper 95% bound of D not below the bar 25

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
| I 欠落 | 1.236 | 0.900 [0.78, 0.97] | 0.940 [0.83, 0.99] | 1.235 | 0.021 | 0.023 | -0.02 | 0.880 [0.76, 0.95] | 0.960 [0.86, 1.00] | 1.235 | 0.021 | 0.023 | -0.01 |
| I 余分 | 0.950 | 0.840 [0.71, 0.93] | 0.900 [0.78, 0.97] | 0.950 | 0.022 | 0.019 | -0.02 | 0.860 [0.73, 0.94] | 0.920 [0.81, 0.98] | 0.950 | 0.022 | 0.019 | -0.03 |
| 置換 cos0.9 | 0.095 | 0.860 [0.73, 0.94] | 0.920 [0.81, 0.98] | 0.105 | 0.046 | 0.038 | +0.23 | 0.860 [0.73, 0.94] | 0.900 [0.78, 0.97] | 0.106 | 0.045 | 0.038 | +0.23 |
| 置換 cos0.7 | 0.190 | 0.860 [0.73, 0.94] | 0.920 [0.81, 0.98] | 0.178 | 0.074 | 0.071 | -0.16 | 0.820 [0.69, 0.91] | 0.900 [0.78, 0.97] | 0.180 | 0.076 | 0.069 | -0.13 |
| 置換 cos0.5 | 0.190 | 0.980 [0.89, 1.00] | 0.980 [0.89, 1.00] | 0.198 | 0.064 | 0.073 | +0.12 | 0.940 [0.83, 0.99] | 0.960 [0.86, 1.00] | 0.198 | 0.068 | 0.072 | +0.11 |
| 置換 cos0.3 | 0.190 | 0.860 [0.73, 0.94] | 0.920 [0.81, 0.98] | 0.177 | 0.079 | 0.072 | -0.17 | 0.900 [0.78, 0.97] | 0.960 [0.86, 1.00] | 0.177 | 0.074 | 0.072 | -0.17 |
| 辺の欠落 | 0.950 | 0.920 [0.81, 0.98] | 0.980 [0.89, 1.00] | 0.950 | 0.018 | 0.020 | -0.02 | 0.960 [0.86, 1.00] | 0.960 [0.86, 1.00] | 0.951 | 0.018 | 0.020 | +0.02 |
| 種別 RA↔MA | 0.713 | 0.900 [0.78, 0.97] | 0.960 [0.86, 1.00] | 0.714 | 0.023 | 0.022 | +0.05 | 0.880 [0.76, 0.95] | 0.960 [0.86, 1.00] | 0.713 | 0.023 | 0.022 | +0.03 |
| 向き違い | 0.950 | 0.860 [0.73, 0.94] | 0.940 [0.83, 0.99] | 0.952 | 0.020 | 0.024 | +0.07 | 0.880 [0.76, 0.95] | 0.960 [0.86, 1.00] | 0.951 | 0.020 | 0.023 | +0.05 |
| 辺の余分 | 0.950 | 0.940 [0.83, 0.99] | 0.980 [0.89, 1.00] | 0.949 | 0.021 | 0.022 | -0.07 | 0.920 [0.81, 0.98] | 0.960 [0.86, 1.00] | 0.949 | 0.021 | 0.022 | -0.05 |
| θ_neg（極性反転） | 1.426 | 0.820 [0.69, 0.91] | 0.860 [0.73, 0.94] | 1.427 | 0.073 | 0.063 | +0.02 | 0.840 [0.71, 0.93] | 0.860 [0.73, 0.94] | 1.427 | 0.068 | 0.062 | +0.02 |

ŝ (independence): mean 1.054, MC SD 0.023 (true 1.052); σ̂ (random intercept): mean 0.997

### D = agree(R2 OOF) − agree(R0), points (nested bootstrap vs Monte-Carlo SD, §16 #14)

(lower / upper = one-sided 95% bounds q0.05 / q0.95, so [lower, upper] is a two-sided 90% interval)

mean D 1.134, MC SD 0.491, mean nested-bootstrap SD 0.586 (ratio 1.193); [lower, upper] covers the across-seed mean (target 0.90): 0.940 [0.83, 0.99]; upper bound < 2 pt: 0.500 [0.36, 0.64]; lower bound ≥ 2 pt: 0.000 [0.00, 0.07]; log-loss form mean 0.005 nats/judgment

| stratum | mean D | MC SD |
| --- | --: | --: |
| T1 | -0.003 | 0.031 |
| T2 | -0.031 | 0.528 |
| T3-clear | 0.051 | 0.195 |
| T3-near | 3.993 | 1.640 |
| T4 | -0.025 | 0.120 |

### Hypotheses (supported = both fits at unadjusted α; Holm over the family computed in gcj)

| test | mean estimate (indep) | supported | Holm p < 0.05 |
| --- | --: | --- | --- |
| H1 | -1.185 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H2 | 0.002 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H4a | 0.285 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H4b | 0.001 | 0.000 [0.00, 0.07] | 0.000 [0.00, 0.07] |
| C1 | 5.062 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| C4 | 0.000 | 0.000 [0.00, 0.07] | 0.000 [0.00, 0.07] |

### Optimizer: full-data fits and bootstrap replicates

| fit | converged | cycled | capped (20 rounds) | valid (§11.2) | mean cycle width (cycled fits) | any cost at boundary | mean start spread | boot: cycled share | boot: capped share | boot: invalid (§11.2) | boot: not converged (older metric) | boot errors |
| --- | --- | --- | --- | --- | --: | --- | --: | --: | --: | --: | --: | --: |
| indep | 0.960 [0.86, 1.00] | 0.040 [0.00, 0.14] | 0.000 [0.00, 0.07] | 1.000 [0.93, 1.00] | 0.012 | 0.060 [0.01, 0.17] | 0.575 | 0.011 | 0.000 | 0.000 | 0.011 | 0 |
| mixed | 0.980 [0.89, 1.00] | 0.020 [0.00, 0.11] | 0.000 [0.00, 0.07] | 1.000 [0.93, 1.00] | 0.037 | 0.040 [0.00, 0.14] | 0.953 | 0.014 | 0.000 | 0.000 | 0.014 | 0 |

bootstrap boundary share (independence, mean over seeds incl. zeros): 置換 cos0.3 0.048, 置換 cos0.5 0.023, 置換 cos0.7 0.046, 置換 cos0.9 0.048

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
| 種別 CA↔MA | 0.750 | 0.713 | 0.700 | 0.072 | -0.014 | — | 104 |
| 種別 RA↔CA | 0.750 | 0.713 | 0.722 | 0.063 | 0.008 | — | 103 |
