#!/usr/bin/env bash
# Appendix A.5 batch of decisions-stats.md: 5 worlds × 200 seeds of `gcj -simulate` at 750 sources (3,000 triplets),
# reference judges, both co-primary fits, paired cluster bootstrap, nested-bootstrap D, one JSONL line per seed.
#
# Resumable: a seed already present in <OUT>/<world>.jsonl is skipped, so the script can be stopped (Ctrl-C) and
# restarted with the same settings. Every seed is its own process, so an interruption loses at most one seed.
#
# Settings (environment variables, defaults in brackets):
#   GOMAXPROCS [3]   CPU threads per run (the machine overheats above ≈ 6)
#   OUT [out/a5]     output directory (JSONL per world, batch.log, stderr.log, last report per world under md/)
#   WORLDS ["null edgeN alt single mild"]   SEED_FROM [101]  SEED_TO [300]
#   SOURCES [750]  BOOT [200]  DREPS [50]  SCENARIO [reference]  EXTRA [""] (extra gcj flags)
#
# Each seed runs fit.Confirmatory (judge QC recorded but not applied unless EXTRA="-apply-qc": the reference
# simulated judges all fail the Krippendorff α ≥ 0.4 criterion, so applying it aborts the analysis).
#
# Cost per seed at 750 sources, GOMAXPROCS=3 (measured 2026-10-29): two multi-start fits ≈ 56 s, paired bootstrap
# ≈ 2.3–4.4 s per replicate, nested D ≈ 21 s + 4.3 s per replicate, matching stability ≈ 9 s. So B = 200 with
# DREPS = 50 is ≈ 13–20 min per seed (≈ 210–330 h for 1,000 seeds) and DREPS = 200 ≈ 24–30 min (≈ 400–500 h).
#
# Usage:  scripts/a5_batch.sh                      # full batch (≈ 1,000 runs; ask before launching)
#         WORLDS="null alt" SEED_TO=102 BOOT=50 scripts/a5_batch.sh     # smoke test
# Then:   python3 scripts/a5_summarise.py out/a5/*.jsonl
set -u
cd "$(dirname "$0")/.." || exit 1 # ged-cost-judge (git describe in the records runs here)

export GOMAXPROCS="${GOMAXPROCS:-3}"
OUT="${OUT:-out/a5}"
WORLDS="${WORLDS:-null edgeN alt single mild}"
SEED_FROM="${SEED_FROM:-101}"
SEED_TO="${SEED_TO:-300}"
SOURCES="${SOURCES:-750}"
BOOT="${BOOT:-200}"
DREPS="${DREPS:-50}"
SCENARIO="${SCENARIO:-reference}"
EXTRA="${EXTRA:-}"

mkdir -p "$OUT/md"
LOG="$OUT/batch.log"
BIN="$OUT/gcj-a5"

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*" | tee -a "$LOG"; }

go build -o "$BIN" ./cmd/gcj || { log "BUILD_FAILED"; exit 1; }
log "START worlds=[$WORLDS] seeds=$SEED_FROM-$SEED_TO sources=$SOURCES boot=$BOOT d-reps=$DREPS scenario=$SCENARIO GOMAXPROCS=$GOMAXPROCS extra=[$EXTRA] git=$(git describe --tags --always --dirty 2>/dev/null)"
trap 'log "INTERRUPTED (resume by rerunning with the same settings)"; exit 130' INT TERM

suffix=""
[ "$SCENARIO" != "reference" ] && suffix="_$SCENARIO"
for world in $WORLDS; do
  jsonl="$OUT/$world$suffix.jsonl"
  done_n=0
  for seed in $(seq "$SEED_FROM" "$SEED_TO"); do
    if [ -f "$jsonl" ] && grep -q "^{\"world\":\"$world\",\"seed\":$seed," "$jsonl"; then
      done_n=$((done_n + 1))
      continue
    fi
    t0=$(date +%s)
    # shellcheck disable=SC2086 # EXTRA is a list of flags
    if "$BIN" -simulate -world "$world" -scenario "$SCENARIO" -sources "$SOURCES" -seed "$seed" -reps 1 \
      -boot "$BOOT" -d-reps "$DREPS" -jsonl "$jsonl" -out "$OUT/md" $EXTRA >/dev/null 2>>"$OUT/stderr.log"; then
      log "ok $world$suffix seed=$seed $(($(date +%s) - t0))s"
    else
      log "FAIL $world$suffix seed=$seed after $(($(date +%s) - t0))s (see $OUT/stderr.log)"
    fi
  done
  [ "$done_n" -gt 0 ] && log "$world$suffix: skipped $done_n seeds already in $jsonl"
  log "DONE $world$suffix"
done
log "ALL_DONE"
