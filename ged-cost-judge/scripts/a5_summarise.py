#!/usr/bin/env python3
"""Summarise the Appendix A.5 batch (decisions-stats.md) from the per-seed JSONL of `gcj -simulate -jsonl`.

Usage:  python3 scripts/a5_summarise.py out/a5/*.jsonl   (standard library only)

Per world (and scenario) it prints:
  - decision-label frequencies per margin-judged parameter (combined label, and P(keep) / P(change) per fit);
  - verdict frequencies with Clopper-Pearson 95% intervals;
  - the false-change rate: P("change") for every parameter whose normalized truth lies inside the band
    (|truth - theta0| <= m), and the family-wise rate of at least one false change (the null and edgeN worlds are
    the ones that matter);
  - 90% / 95% interval coverage of the normalized truth (both fits; one-sided bound when the rule used it);
  - normalized SD: mean bootstrap SD against the Monte-Carlo SD of the estimates across seeds, and the bias;
  - D: mean, Monte-Carlo SD across seeds, mean nested-bootstrap SD, coverage of the across-seed mean by the
    [one-sided 95% lower, upper] interval (two-sided 90%), and the shares with upper < 2 pt / lower >= 2 pt;
  - judge QC failures (recorded; applied only with gcj -apply-qc), scale identification, matching stability;
  - hypothesis support rates; boundary / cycled / capped shares (full-data fits and bootstrap replicates);
  - informative counts against the minimum; the untied diagnostic fit's RA<->CA / CA<->MA against the truth;
  - run time per seed.
"""

import json
import math
import statistics
import sys
from collections import Counter, OrderedDict, defaultdict

LABELS = [
    "keep",
    "change",
    "undecided (different, size unclear)",
    "undecided (imprecise)",
    "undecided (model-sensitive)",
    "not estimable",
]
SHORT = {
    "keep": "keep",
    "change": "change",
    "undecided (different, size unclear)": "und-diff",
    "undecided (imprecise)": "und-impr",
    "undecided (model-sensitive)": "und-model",
    "not estimable": "not-est",
}
VERDICTS = ["validated", "not contradicted", "contradicted"]


# ----------------------------------------------------------------------------------------------- statistics helpers


def _log_binom_pmf(k, n, p):
    if p <= 0.0:
        return 0.0 if k == 0 else -math.inf
    if p >= 1.0:
        return 0.0 if k == n else -math.inf
    return (math.lgamma(n + 1) - math.lgamma(k + 1) - math.lgamma(n - k + 1)
            + k * math.log(p) + (n - k) * math.log1p(-p))


def _binom_cdf(k, n, p):
    """P(X <= k) for X ~ Bin(n, p)."""
    if k < 0:
        return 0.0
    if k >= n:
        return 1.0
    return min(1.0, sum(math.exp(_log_binom_pmf(i, n, p)) for i in range(k + 1)))


def _bisect(f, lo=0.0, hi=1.0, it=80):
    """Root of a monotone decreasing f on [lo, hi]."""
    for _ in range(it):
        mid = (lo + hi) / 2
        if f(mid) > 0:
            lo = mid
        else:
            hi = mid
    return (lo + hi) / 2


def clopper_pearson(k, n, conf=0.95):
    if n == 0:
        return (float("nan"), float("nan"))
    a = (1 - conf) / 2
    lo = 0.0 if k == 0 else _bisect(lambda p: a - (1 - _binom_cdf(k - 1, n, p)))
    hi = 1.0 if k == n else _bisect(lambda p: _binom_cdf(k, n, p) - a)
    return (lo, hi)


def rate(k, n):
    if n == 0:
        return "—"
    lo, hi = clopper_pearson(k, n)
    return f"{k / n:.3f} [{lo:.2f}, {hi:.2f}]"


def num(x):
    return x is not None and not (isinstance(x, float) and math.isnan(x))


def mean(xs):
    xs = [x for x in xs if num(x)]
    return statistics.fmean(xs) if xs else float("nan")


def sd(xs):
    xs = [x for x in xs if num(x)]
    return statistics.stdev(xs) if len(xs) > 1 else float("nan")


def f3(x):
    return "—" if not num(x) else f"{x:.3f}"


def table(head, rows, align=None):
    align = align or ["---"] + ["--:"] * (len(head) - 1)
    out = ["| " + " | ".join(head) + " |", "| " + " | ".join(align) + " |"]
    out += ["| " + " | ".join(str(c) for c in r) + " |" for r in rows]
    return "\n".join(out)


# --------------------------------------------------------------------------------------------------------- loading


def load(paths):
    worlds = OrderedDict()
    bad = 0
    for path in paths:
        with open(path, encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    r = json.loads(line)
                except json.JSONDecodeError:
                    bad += 1  # a line cut by an interruption
                    continue
                key = (r["world"], r.get("scenario", "reference"))
                seeds = worlds.setdefault(key, OrderedDict())
                seeds.setdefault(r["seed"], r)  # the first record of a seed wins
    if bad:
        print(f"(skipped {bad} unparsable line(s))\n")
    return worlds


def covers(d, truth, level):
    if d is None or not num(truth):
        return None
    if level == 90 and d.get("one_sided"):  # boundary share 10–50%: one-sided 95% upper bound replaces the 90% interval
        return num(d.get("upper95")) and truth <= d["upper95"]
    lo, hi = (d.get("lo90"), d.get("hi90")) if level == 90 else (d.get("lo95"), d.get("hi95"))
    if not (num(lo) and num(hi)):
        return None
    return lo <= truth <= hi


# ------------------------------------------------------------------------------------------------------- summaries


def summarise(world, scenario, recs):
    n = len(recs)
    r0 = recs[0]
    m = r0["m"]
    seeds = sorted(r["seed"] for r in recs)
    print(f"\n## {world} ({scenario}): {n} seeds ({seeds[0]}–{seeds[-1]}), sources {r0['sources']}, "
          f"B = {r0['boot']}, D reps = {r0['d_reps']}, m = {m}")
    gits = Counter(r.get("git", "?") for r in recs)
    print(f"\ncode: {', '.join(f'{g} ×{c}' for g, c in gits.items())}; "
          f"ŝ_true = {r0['s_true']:.4f}; time per seed: mean {mean([r['secs']['total'] for r in recs]):.0f} s "
          f"(fit.Confirmatory {mean([r['secs'].get('confirmatory') for r in recs]):.0f}, "
          f"diagnostic {mean([r['secs']['diag'] for r in recs]):.0f})")
    qc = Counter(j for r in recs for j in r.get("qc_fail", {}))
    applied = sum(bool(r.get("excluded_judges")) for r in recs)
    print(f"\njudge QC: seeds with a judge excluded {applied}/{n}; judges failing the QC (seeds): "
          + (", ".join(f"{j} {c}" for j, c in sorted(qc.items())) or "none")
          + f"; scale identified {sum(r.get('scale_identified', True) for r in recs)}/{n}; "
          f"matching stability mean {f3(mean([r.get('stability') for r in recs]))}")

    names = [p["name"] for p in r0["params"]]
    margin = [p["margin"] for p in r0["params"]]
    truth = [p["true_norm"] for p in r0["params"]]
    theta0 = [p["theta0"] for p in r0["params"]]
    inband = [num(t) and abs(t - t0) <= m + 1e-9 for t, t0 in zip(truth, theta0)]

    # Decision labels.
    rows = []
    for i, name in enumerate(names):
        if not margin[i]:
            continue
        c = Counter(r["params"][i]["combined"] for r in recs)
        ki = sum(r["params"][i]["indep"]["label"] == "keep" for r in recs)
        km = sum((r["params"][i]["mixed"] or {}).get("label") == "keep" for r in recs)
        ci_ = sum(r["params"][i]["indep"]["label"] == "change" for r in recs)
        cm = sum((r["params"][i]["mixed"] or {}).get("label") == "change" for r in recs)
        rows.append([name, f3(truth[i]), f3(theta0[i]), "yes" if inband[i] else "**no**"]
                    + [c.get(lab, 0) for lab in LABELS] + [f"{ki}/{km}", f"{ci_}/{cm}"])
    print("\n### Decision labels (normalized scale; combined label of the two fits, counts over seeds)\n")
    print(table(["parameter", "truth", "θ₀", "in band"] + [SHORT[l] for l in LABELS]
                + ["keep indep/mixed", "change indep/mixed"], rows))

    # Verdicts.
    v = Counter(r["verdict"] for r in recs)
    print("\n### Verdicts\n")
    print(table(["verdict", "count", "rate [CP 95%]"], [[x, v.get(x, 0), rate(v.get(x, 0), n)] for x in VERDICTS],
                ["---", "--:", "---"]))
    miss = Counter(item.split(":")[0] if ":" in item else item for r in recs for item in r.get("verdict_missing", []))
    if miss:
        print("\nmissing elements (seeds): " + "; ".join(f"{k} {c}" for k, c in miss.most_common()))

    # False changes.
    fc_rows = []
    any_false = 0
    for r in recs:
        if any(inband[i] and margin[i] and r["params"][i]["combined"] == "change" for i in range(len(names))):
            any_false += 1
    for i, name in enumerate(names):
        if margin[i] and inband[i]:
            k = sum(r["params"][i]["combined"] == "change" for r in recs)
            fc_rows.append([name, rate(k, n)])
    print("\n### False 'change' (parameters whose truth is inside the band)\n")
    if fc_rows:
        print(table(["parameter", "P(change) [CP 95%]"], fc_rows, ["---", "---"]))
        print(f"\nat least one false change (family-wise): {rate(any_false, n)}")
    else:
        print("no margin-judged parameter is inside the band in this world")

    # Coverage, SD, bias.
    rows = []
    for i, name in enumerate(names):
        cells = [name, f3(truth[i])]
        for fitname in ("indep", "mixed"):
            ds = [r["params"][i][fitname] for r in recs if r["params"][i][fitname] is not None]
            if not ds:
                cells += ["—"] * 6
                continue
            est = [d["est"] for d in ds]
            c90 = [covers(d, truth[i], 90) for d in ds]
            c95 = [covers(d, truth[i], 95) for d in ds]
            c90 = [c for c in c90 if c is not None]
            c95 = [c for c in c95 if c is not None]
            mc = sd(est)
            bsd = mean([d["sd"] for d in ds])
            bias = mean(est) - truth[i] if num(truth[i]) else float("nan")
            cells += [rate(sum(c90), len(c90)), rate(sum(c95), len(c95)), f3(mean(est)), f3(mc), f3(bsd),
                      "—" if not (num(bias) and num(mc) and mc > 0) else f"{bias / mc:+.2f}"]
        rows.append(cells)
    print("\n### Interval coverage of the normalized truth, SD and bias (independence | random intercept)\n")
    sub = ["90% cov.", "95% cov.", "mean θ̃", "MC SD", "boot SD", "bias/MC SD"]
    print(table(["parameter", "truth"] + [f"{s} (I)" for s in sub] + [f"{s} (M)" for s in sub], rows,
                ["---", "--:"] + ["---", "---", "--:", "--:", "--:", "--:"] * 2))
    shat = [r["s_hat_indep"] for r in recs]
    print(f"\nŝ (independence): mean {f3(mean(shat))}, MC SD {f3(sd(shat))} (true {r0['s_true']:.3f}); "
          f"σ̂ (random intercept): mean {f3(mean([r['sigma_hat'] for r in recs]))}")

    # D.
    ds = [r["d"] for r in recs if r.get("d")]
    print("\n### D = agree(R2 OOF) − agree(R0), points (nested bootstrap vs Monte-Carlo SD, §16 #14)\n")
    print("(lower / upper = one-sided 95% bounds q0.05 / q0.95, so [lower, upper] is a two-sided 90% interval)\n")
    if ds:
        agree = [d["agree"] for d in ds]
        md, mc = mean(agree), sd(agree)
        bsd = mean([d["boot_sd"] for d in ds])
        cov = [num(d["lower"]) and num(d["upper"]) and d["lower"] <= md <= d["upper"] for d in ds]
        up = sum(num(d["upper"]) and d["upper"] < 2 for d in ds)
        low = sum(num(d["lower"]) and d["lower"] >= 2 for d in ds)
        print(f"mean D {f3(md)}, MC SD {f3(mc)}, mean nested-bootstrap SD {f3(bsd)} "
              f"(ratio {f3(bsd / mc) if num(mc) and mc > 0 else '—'}); "
              f"[lower, upper] covers the across-seed mean (target 0.90): {rate(sum(cov), len(cov))}; "
              f"upper bound < 2 pt: {rate(up, len(ds))}; lower bound ≥ 2 pt: {rate(low, len(ds))}; "
              f"log-loss form mean {f3(mean([d['logloss'] for d in ds]))} nats/judgment")
        strata = sorted({k for d in ds for k in d.get("strata", {})})
        if strata:
            print("\n" + table(["stratum", "mean D", "MC SD"],
                               [[s, f3(mean([d['strata'].get(s) for d in ds])), f3(sd([d['strata'].get(s) for d in ds]))]
                                for s in strata]))
    else:
        print("D not computed (-d-reps 0)")

    # Hypotheses.
    ids = [h["id"] for h in r0.get("hypotheses", [])]
    if ids:
        rows = []
        for j, hid in enumerate(ids):
            sup = sum(r["hypotheses"][j]["supported"] for r in recs)
            rej = sum(num(r["hypotheses"][j]["p_holm"]) and r["hypotheses"][j]["p_holm"] < 0.05 for r in recs)
            rows.append([hid, f3(mean([r["hypotheses"][j]["est"] for r in recs])), rate(sup, n), rate(rej, n)])
        print("\n### Hypotheses (supported = both fits at unadjusted α; Holm over the family computed in gcj)\n")
        print(table(["test", "mean estimate (indep)", "supported", "Holm p < 0.05"], rows, ["---", "--:", "---", "---"]))

    # Optimizer health.
    print("\n### Optimizer: full-data fits and bootstrap replicates\n")
    rows = []
    for fitname in ("indep", "mixed"):
        fl = [r["fits"][fitname] for r in recs]
        bs = [r["boot_shares"][fitname] for r in recs]
        rows.append([fitname, rate(sum(f["converged"] for f in fl), n), rate(sum(f["cycled"] for f in fl), n),
                     rate(sum(f["capped"] for f in fl), n), rate(sum(bool(f["boundary"]) for f in fl), n),
                     f3(mean([f["start_spread"] for f in fl])),
                     f3(mean([b["cycled"] for b in bs])), f3(mean([b["capped"] for b in bs])),
                     f3(mean([b["not_converged"] for b in bs])), sum(b.get("errors", 0) for b in bs)])
    print(table(["fit", "converged", "cycled", "capped (20 rounds)", "any cost at boundary", "mean start spread",
                 "boot: cycled share", "boot: capped share", "boot: not converged", "boot errors"], rows,
                ["---", "---", "---", "---", "---", "--:", "--:", "--:", "--:", "--:"]))
    bnd = defaultdict(list)
    for r in recs:
        for k, v in r["boot_shares"]["indep"]["boundary"].items():
            bnd[k].append(v)
    if bnd:
        print("\nbootstrap boundary share (independence, mean over seeds incl. zeros): "
              + ", ".join(f"{k} {sum(v) / n:.3f}" for k, v in sorted(bnd.items())))

    # Informative counts.
    keys = sorted(r0["informative"])
    rows = []
    for k in keys:
        xs = [r["informative"][k] for r in recs]
        mn = r0["min_informative"].get(k)
        rows.append([k, mn if mn is not None else "—", min(xs), f"{mean(xs):.0f}", max(xs),
                     "—" if mn is None else sum(x < mn for x in xs)])
    xs = [r["neg_informative"] for r in recs]
    negname = next((k for k in r0["min_informative"] if k.startswith("θ_neg")), None)
    mn = r0["min_informative"].get(negname) if negname else None
    rows.append(["θ_neg (NegDiff ≠ 0)", mn if mn is not None else "—", min(xs), f"{mean(xs):.0f}", max(xs),
                 "—" if mn is None else sum(x < mn for x in xs)])
    print("\n### Informative triplets n_p in the fit set (§15.3)\n")
    print(table(["parameter", "minimum", "min", "mean", "max", "seeds below"], rows))

    # Diagnostic untied fit.
    if all(r.get("diag") for r in recs):
        rows = []
        for pair in r0["diag"]["pairs"]:
            dp = [r["diag"]["pairs"][pair] for r in recs]
            t = dp[0]["true_norm"]
            c = [d["lo90"] <= t <= d["hi90"] for d in dp if num(d.get("lo90")) and num(d.get("hi90"))]
            rows.append([pair, f3(dp[0]["true_anchored"]), f3(t), f3(mean([d["norm"] for d in dp])), f3(sd([d["norm"] for d in dp])),
                         f3(mean([d["contrast"] for d in dp])), rate(sum(c), len(c)) if c else "—",
                         f"{mean([d['feasible_relabels'] for d in dp]):.0f}"])
        print("\n### Untied diagnostic fit (§12b; diag_ca_type included)\n")
        print(table(["pair", "truth (anchored)", "truth (normalized)", "mean estimate", "MC SD", "mean contrast vs RA↔MA",
                     "90% cov.", "relabels"], rows))


def main(argv):
    if len(argv) < 2:
        print(__doc__)
        return 2
    worlds = load(argv[1:])
    print("# Appendix A.5 summary")
    for (world, scenario), seeds in worlds.items():
        summarise(world, scenario, list(seeds.values()))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
