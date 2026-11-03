# Appendix A.5 summary

### Completed and not-completed seeds per world (not-completed seeds are excluded from every rate below)

| world | ok | not completed | of which timeout | of which other status | not-completed rate [CP 95%] | not-completed seeds | limit (-max-seconds, all rows) |
| --- | --: | --: | --: | --- | --- | --- | --- |
| edgeN (reference) | 50 | 0 | 0 | — | 0.000 [0.00, 0.07] | — | 4500 s |

## edgeN (reference): 50 seeds (101–150), sources 750, B = 200, D reps = 200, m = 0.25

code: b0019f6 ×1, 7ffd2d3 ×1, 9ae0a8b ×1, e44bb61 ×1, edb5f89 ×1, 5819296 ×1, 444f2be ×1, adf48ac ×1, b768c63 ×1, 74a6977 ×1, fb06fa2 ×1, 4c0b96c ×1, 9e36d93 ×1, b3a3048 ×1, b6f25e1 ×1, 07f7546 ×1, 18cef22 ×1, 6f82546 ×1, 9729085 ×1, b0f99c1 ×1, 8e916bd ×1, 06bfdb8 ×1, 90b9d62 ×1, 9da4709 ×1, 588c0b7 ×1, 37c738a ×1, b83075f ×1, b3aba31 ×1, e42975e ×1, ea15dc1 ×1, c1c23c7 ×1, 5c55392 ×1, 1d34865 ×1, 1de7b47 ×1, 14ed87a ×1, f8a6d23 ×1, 8bafdbe ×1, 21959db ×1, c535759 ×1, 2613f0f ×1, 870429a ×1, 288826d ×1, e514569 ×1, 3c59f04 ×1, f3a0815 ×1, a01dea2 ×1, f76bc36 ×1, 9bbdfb0 ×1, 8bf5cb0 ×1, 2978996 ×1; ŝ_true = 1.3333; time per seed: mean 1823 s (fit.Confirmatory 1815, diagnostic 1)

judge QC: seeds with a judge excluded 0/50; judges failing the QC (seeds): none; scale identified 50/50; matching stability mean 0.052

### Decision labels (normalized scale; combined label of the two fits, counts over seeds)

| parameter | truth | θ₀ | in band | keep | change | und-diff | und-impr | und-model | not-est | keep indep/mixed | change indep/mixed |
| --- | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: |
| I 欠落 | 1.250 | 1.000 | yes | 2 | 1 | 46 | 0 | 1 | 0 | 2/3 | 1/1 |
| I 余分 | 1.000 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 辺の欠落 | 1.000 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 種別 RA↔MA | 0.750 | 0.750 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 向き違い | 1.000 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 辺の余分 | 0.750 | 1.000 | yes | 1 | 1 | 44 | 0 | 4 | 0 | 1/2 | 2/3 |

### Verdicts

capped = after every cap (the deviation cap always applies to simulations: -d-reps, scaled n_p floors, QC not applied, LOJO off); uncapped = before the deviation cap, data-dependent elements only (recorded for 50/50 seeds)

| verdict | capped: count | capped: rate [CP 95%] | uncapped: count | uncapped: rate [CP 95%] |
| --- | --: | --- | --: | --- |
| validated | 0 | 0.000 [0.00, 0.07] | 0 | 0.000 [0.00, 0.07] |
| not contradicted | 39 | 0.780 [0.64, 0.88] | 39 | 0.780 [0.64, 0.88] |
| contradicted | 11 | 0.220 [0.12, 0.36] | 11 | 0.220 [0.12, 0.36] |

missing elements (seeds): upper 95% bound of D not below the bar 50; non-pre-registered settings (see the deviations list) 50; 辺の余分 49; I 欠落 48

### False 'change' (parameters whose truth is inside the band)

| parameter | P(change) [CP 95%] |
| --- | --- |
| I 欠落 | 0.020 [0.00, 0.11] |
| I 余分 | 0.000 [0.00, 0.07] |
| 辺の欠落 | 0.000 [0.00, 0.07] |
| 種別 RA↔MA | 0.000 [0.00, 0.07] |
| 向き違い | 0.000 [0.00, 0.07] |
| 辺の余分 | 0.020 [0.00, 0.11] |

at least one false change (family-wise): 0.040 [0.00, 0.14]

### Interval coverage of the normalized truth, SD and bias (independence | random intercept)

| parameter | truth | 90% cov. (I) | 95% cov. (I) | mean θ̃ (I) | MC SD (I) | boot SD (I) | bias/MC SD (I) | 90% cov. (M) | 95% cov. (M) | mean θ̃ (M) | MC SD (M) | boot SD (M) | bias/MC SD (M) |
| --- | --: | --- | --- | --: | --: | --: | --: | --- | --- | --: | --: | --: | --: |
| I 欠落 | 1.250 | 0.940 [0.83, 0.99] | 0.960 [0.86, 1.00] | 1.251 | 0.019 | 0.020 | +0.03 | 0.920 [0.81, 0.98] | 0.960 [0.86, 1.00] | 1.251 | 0.018 | 0.019 | +0.03 |
| I 余分 | 1.000 | 0.880 [0.76, 0.95] | 0.920 [0.81, 0.98] | 1.000 | 0.018 | 0.016 | -0.01 | 0.820 [0.69, 0.91] | 0.920 [0.81, 0.98] | 1.000 | 0.017 | 0.016 | -0.01 |
| 置換 cos0.9 | 0.100 | 0.840 [0.71, 0.93] | 0.920 [0.81, 0.98] | 0.111 | 0.037 | 0.032 | +0.29 | 0.900 [0.78, 0.97] | 0.920 [0.81, 0.98] | 0.110 | 0.035 | 0.032 | +0.28 |
| 置換 cos0.7 | 0.200 | 0.880 [0.76, 0.95] | 0.920 [0.81, 0.98] | 0.189 | 0.062 | 0.060 | -0.17 | 0.900 [0.78, 0.97] | 0.940 [0.83, 0.99] | 0.191 | 0.062 | 0.060 | -0.14 |
| 置換 cos0.5 | 0.200 | 0.900 [0.78, 0.97] | 0.960 [0.86, 1.00] | 0.202 | 0.056 | 0.061 | +0.04 | 0.940 [0.83, 0.99] | 0.940 [0.83, 0.99] | 0.202 | 0.057 | 0.061 | +0.04 |
| 置換 cos0.3 | 0.200 | 0.920 [0.81, 0.98] | 0.980 [0.89, 1.00] | 0.196 | 0.065 | 0.062 | -0.06 | 0.940 [0.83, 0.99] | 0.980 [0.89, 1.00] | 0.197 | 0.061 | 0.061 | -0.05 |
| 辺の欠落 | 1.000 | 0.920 [0.81, 0.98] | 0.940 [0.83, 0.99] | 1.000 | 0.016 | 0.017 | -0.02 | 0.920 [0.81, 0.98] | 0.940 [0.83, 0.99] | 1.001 | 0.016 | 0.017 | +0.06 |
| 種別 RA↔MA | 0.750 | 0.940 [0.83, 0.99] | 0.980 [0.89, 1.00] | 0.752 | 0.018 | 0.019 | +0.10 | 0.920 [0.81, 0.98] | 0.980 [0.89, 1.00] | 0.752 | 0.017 | 0.019 | +0.10 |
| 向き違い | 1.000 | 0.940 [0.83, 0.99] | 0.960 [0.86, 1.00] | 0.999 | 0.016 | 0.019 | -0.08 | 0.940 [0.83, 0.99] | 0.940 [0.83, 0.99] | 0.999 | 0.016 | 0.019 | -0.05 |
| 辺の余分 | 0.750 | 0.940 [0.83, 0.99] | 1.000 [0.93, 1.00] | 0.749 | 0.016 | 0.019 | -0.04 | 0.900 [0.78, 0.97] | 1.000 [0.93, 1.00] | 0.748 | 0.017 | 0.019 | -0.14 |
| θ_neg（極性反転） | 1.125 | 0.880 [0.76, 0.95] | 0.920 [0.81, 0.98] | 1.123 | 0.056 | 0.052 | -0.04 | 0.840 [0.71, 0.93] | 0.900 [0.78, 0.97] | 1.124 | 0.053 | 0.051 | -0.03 |

ŝ (independence): mean 1.335, MC SD 0.029 (true 1.333); σ̂ (random intercept): mean 1.000

### D = agree(R2 OOF) − agree(R0), points (nested bootstrap vs Monte-Carlo SD, §16 #14)

(lower / upper = one-sided 95% bounds q0.05 / q0.95, so [lower, upper] is a two-sided 90% interval)

mean D 2.520, MC SD 0.539, mean nested-bootstrap SD 0.615 (ratio 1.140); [lower, upper] covers the across-seed mean (target 0.90): 0.980 [0.89, 1.00]; upper bound < 2 pt: 0.000 [0.00, 0.07]; lower bound ≥ 2 pt: 0.200 [0.10, 0.34]; log-loss form mean 0.015 nats/judgment

| stratum | mean D | MC SD |
| --- | --: | --: |
| T1 | -0.003 | 0.073 |
| T2 | -0.041 | 0.412 |
| T3-clear | 0.232 | 0.315 |
| T3-near | 8.638 | 1.719 |
| T4 | -0.008 | 0.045 |

### Hypotheses (supported = both fits at unadjusted α; Holm over the family computed in gcj)

| test | mean estimate (indep) | supported | Holm p < 0.05 |
| --- | --: | --- | --- |
| H1 | -0.997 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H2 | -0.001 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H4a | 0.251 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H4b | 0.250 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| C1 | 4.958 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| C4 | 0.000 | 0.000 [0.00, 0.07] | 0.000 [0.00, 0.07] |

### Optimizer: full-data fits and bootstrap replicates

| fit | converged | cycled | capped (20 rounds) | valid (§11.2) | mean cycle width (cycled fits) | any cost at boundary | mean start spread | boot: cycled share | boot: capped share | boot: invalid (§11.2) | boot: not converged (older metric) | boot errors |
| --- | --- | --- | --- | --- | --: | --- | --: | --: | --: | --: | --: | --: |
| indep | 0.980 [0.89, 1.00] | 0.020 [0.00, 0.11] | 0.000 [0.00, 0.07] | 1.000 [0.93, 1.00] | 0.004 | 0.000 [0.00, 0.07] | 0.573 | 0.007 | 0.000 | 0.000 | 0.007 | 0 |
| mixed | 0.960 [0.86, 1.00] | 0.040 [0.00, 0.14] | 0.000 [0.00, 0.07] | 1.000 [0.93, 1.00] | 0.025 | 0.000 [0.00, 0.07] | 0.965 | 0.011 | 0.000 | 0.000 | 0.011 | 0 |

bootstrap boundary share (independence, mean over seeds incl. zeros): 置換 cos0.3 0.012, 置換 cos0.5 0.008, 置換 cos0.7 0.017, 置換 cos0.9 0.012

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
| 種別 CA↔MA | 1.000 | 0.750 | 0.740 | 0.056 | -0.011 | — | 104 |
| 種別 RA↔CA | 1.000 | 0.750 | 0.757 | 0.046 | 0.006 | — | 103 |
