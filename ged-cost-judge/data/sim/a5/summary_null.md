# Appendix A.5 summary

### Completed and not-completed seeds per world (not-completed seeds are excluded from every rate below)

| world | ok | not completed | of which timeout | of which other status | not-completed rate [CP 95%] | not-completed seeds | limit (-max-seconds, all rows) |
| --- | --: | --: | --: | --- | --- | --- | --- |
| null (reference) | 50 | 0 | 0 | — | 0.000 [0.00, 0.07] | — | 4500 s |

## null (reference): 50 seeds (101–150), sources 750, B = 200, D reps = 200, m = 0.25

code: 71f1cba ×1, 549230c ×1, 5a1fe15 ×1, e6dcd07 ×1, 201cd47 ×1, ab69b57 ×1, 1c29eb6 ×1, ae92292 ×1, 68f2123 ×1, 8819488 ×1, e80c303 ×1, 486b1c4 ×1, 6fbfb4c ×1, ae42ca4 ×1, 85c2244 ×1, 47b4dd7 ×1, ec2c3b1 ×1, ea572eb ×1, 0ed78d8 ×1, 0cb2257 ×1, 6c88878 ×1, bc3d34c ×1, fa51f4c ×1, 81bf441 ×1, ac98d87 ×1, d448bf1 ×1, 6ffcc29 ×1, 08ef6a2 ×1, 9e783a0 ×1, 548d22c ×1, 4afbe70 ×1, 478a4e6 ×1, 7784286 ×1, 9f00630 ×1, 4b44538 ×1, c2abf15 ×1, 85da191 ×1, e89c3a9 ×1, e501ded ×1, 31c62c6 ×1, 15e2cda ×1, 33b7277 ×1, 5047c22 ×1, 952bd8d ×1, 80564d2 ×1, 9fed7be ×1, 92f36f9 ×1, 6efab41 ×1, 6f0e82f ×1, 0343966 ×1; ŝ_true = 1.0000; time per seed: mean 1979 s (fit.Confirmatory 1971, diagnostic 1)

judge QC: seeds with a judge excluded 0/50; judges failing the QC (seeds): none; scale identified 50/50; matching stability mean 0.069

### Decision labels (normalized scale; combined label of the two fits, counts over seeds)

| parameter | truth | θ₀ | in band | keep | change | und-diff | und-impr | und-model | not-est | keep indep/mixed | change indep/mixed |
| --- | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: | --: |
| I 欠落 | 1.000 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| I 余分 | 1.000 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 辺の欠落 | 1.000 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 種別 RA↔MA | 0.750 | 0.750 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 向き違い | 1.000 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |
| 辺の余分 | 1.000 | 1.000 | yes | 50 | 0 | 0 | 0 | 0 | 0 | 50/50 | 0/0 |

### Verdicts

capped = after every cap (the deviation cap always applies to simulations: -d-reps, scaled n_p floors, QC not applied, LOJO off); uncapped = before the deviation cap, data-dependent elements only (recorded for 50/50 seeds)

| verdict | capped: count | capped: rate [CP 95%] | uncapped: count | uncapped: rate [CP 95%] |
| --- | --: | --- | --: | --- |
| validated | 0 | 0.000 [0.00, 0.07] | 50 | 1.000 [0.93, 1.00] |
| not contradicted | 50 | 1.000 [0.93, 1.00] | 0 | 0.000 [0.00, 0.07] |
| contradicted | 0 | 0.000 [0.00, 0.07] | 0 | 0.000 [0.00, 0.07] |

missing elements (seeds): non-pre-registered settings (see the deviations list) 50

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
| I 欠落 | 1.000 | 0.900 [0.78, 0.97] | 0.980 [0.89, 1.00] | 1.002 | 0.022 | 0.024 | +0.08 | 0.920 [0.81, 0.98] | 0.960 [0.86, 1.00] | 1.002 | 0.021 | 0.024 | +0.07 |
| I 余分 | 1.000 | 0.840 [0.71, 0.93] | 0.880 [0.76, 0.95] | 0.999 | 0.023 | 0.020 | -0.06 | 0.840 [0.71, 0.93] | 0.880 [0.76, 0.95] | 0.999 | 0.022 | 0.020 | -0.06 |
| 置換 cos0.9 | 0.100 | 0.800 [0.66, 0.90] | 0.880 [0.76, 0.95] | 0.112 | 0.050 | 0.040 | +0.23 | 0.800 [0.66, 0.90] | 0.900 [0.78, 0.97] | 0.112 | 0.050 | 0.039 | +0.24 |
| 置換 cos0.7 | 0.200 | 0.900 [0.78, 0.97] | 0.920 [0.81, 0.98] | 0.184 | 0.078 | 0.073 | -0.20 | 0.860 [0.73, 0.94] | 0.920 [0.81, 0.98] | 0.186 | 0.079 | 0.072 | -0.17 |
| 置換 cos0.5 | 0.200 | 0.980 [0.89, 1.00] | 0.980 [0.89, 1.00] | 0.211 | 0.068 | 0.075 | +0.16 | 0.940 [0.83, 0.99] | 0.960 [0.86, 1.00] | 0.210 | 0.072 | 0.075 | +0.15 |
| 置換 cos0.3 | 0.200 | 0.900 [0.78, 0.97] | 0.960 [0.86, 1.00] | 0.191 | 0.081 | 0.076 | -0.11 | 0.940 [0.83, 0.99] | 0.960 [0.86, 1.00] | 0.191 | 0.077 | 0.076 | -0.12 |
| 辺の欠落 | 1.000 | 0.980 [0.89, 1.00] | 1.000 [0.93, 1.00] | 0.999 | 0.018 | 0.021 | -0.08 | 0.940 [0.83, 0.99] | 1.000 [0.93, 1.00] | 0.999 | 0.018 | 0.021 | -0.05 |
| 種別 RA↔MA | 0.750 | 0.900 [0.78, 0.97] | 0.960 [0.86, 1.00] | 0.751 | 0.025 | 0.023 | +0.04 | 0.900 [0.78, 0.97] | 0.960 [0.86, 1.00] | 0.750 | 0.024 | 0.023 | +0.00 |
| 向き違い | 1.000 | 0.900 [0.78, 0.97] | 0.940 [0.83, 0.99] | 1.000 | 0.021 | 0.024 | +0.02 | 0.900 [0.78, 0.97] | 0.940 [0.83, 0.99] | 1.000 | 0.022 | 0.024 | +0.01 |
| 辺の余分 | 1.000 | 0.940 [0.83, 0.99] | 0.980 [0.89, 1.00] | 1.000 | 0.023 | 0.023 | -0.02 | 0.900 [0.78, 0.97] | 1.000 [0.93, 1.00] | 1.000 | 0.023 | 0.023 | +0.02 |
| θ_neg（極性反転） | 1.500 | 0.860 [0.73, 0.94] | 0.860 [0.73, 0.94] | 1.504 | 0.075 | 0.066 | +0.05 | 0.800 [0.66, 0.90] | 0.880 [0.76, 0.95] | 1.502 | 0.072 | 0.065 | +0.03 |

ŝ (independence): mean 1.001, MC SD 0.023 (true 1.000); σ̂ (random intercept): mean 0.996

### D = agree(R2 OOF) − agree(R0), points (nested bootstrap vs Monte-Carlo SD, §16 #14)

(lower / upper = one-sided 95% bounds q0.05 / q0.95, so [lower, upper] is a two-sided 90% interval)

mean D -0.060, MC SD 0.345, mean nested-bootstrap SD 0.545 (ratio 1.580); [lower, upper] covers the across-seed mean (target 0.90): 1.000 [0.93, 1.00]; upper bound < 2 pt: 1.000 [0.93, 1.00]; lower bound ≥ 2 pt: 0.000 [0.00, 0.07]; log-loss form mean -0.001 nats/judgment

| stratum | mean D | MC SD |
| --- | --: | --: |
| T1 | 0.000 | 0.018 |
| T2 | 0.009 | 0.520 |
| T3-clear | -0.015 | 0.037 |
| T3-near | -0.197 | 1.171 |
| T4 | -0.017 | 0.087 |

### Hypotheses (supported = both fits at unadjusted α; Holm over the family computed in gcj)

| test | mean estimate (indep) | supported | Holm p < 0.05 |
| --- | --: | --- | --- |
| H1 | -1.247 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H2 | 0.002 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| H4a | 0.003 | 0.120 [0.05, 0.24] | 0.040 [0.00, 0.14] |
| H4b | -0.001 | 0.000 [0.00, 0.07] | 0.000 [0.00, 0.07] |
| C1 | 3.985 | 1.000 [0.93, 1.00] | 1.000 [0.93, 1.00] |
| C4 | 0.000 | 0.000 [0.00, 0.07] | 0.000 [0.00, 0.07] |

### Optimizer: full-data fits and bootstrap replicates

| fit | converged | cycled | capped (20 rounds) | valid (§11.2) | mean cycle width (cycled fits) | any cost at boundary | mean start spread | boot: cycled share | boot: capped share | boot: invalid (§11.2) | boot: not converged (older metric) | boot errors |
| --- | --- | --- | --- | --- | --: | --- | --: | --: | --: | --: | --: | --: |
| indep | 0.980 [0.89, 1.00] | 0.020 [0.00, 0.11] | 0.000 [0.00, 0.07] | 1.000 [0.93, 1.00] | 0.011 | 0.020 [0.00, 0.11] | 0.595 | 0.011 | 0.000 | 0.000 | 0.011 | 0 |
| mixed | 0.980 [0.89, 1.00] | 0.020 [0.00, 0.11] | 0.000 [0.00, 0.07] | 1.000 [0.93, 1.00] | 0.008 | 0.000 [0.00, 0.07] | 2.313 | 0.013 | 0.000 | 0.000 | 0.013 | 0 |

bootstrap boundary share (independence, mean over seeds incl. zeros): 置換 cos0.3 0.045, 置換 cos0.5 0.018, 置換 cos0.7 0.049, 置換 cos0.9 0.053

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
| 種別 CA↔MA | 0.750 | 0.750 | 0.734 | 0.080 | -0.017 | — | 104 |
| 種別 RA↔CA | 0.750 | 0.750 | 0.763 | 0.068 | 0.013 | — | 103 |
