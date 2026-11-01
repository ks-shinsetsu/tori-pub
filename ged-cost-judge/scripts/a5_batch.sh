#!/usr/bin/env bash
# Appendix A.5 batch of decisions-stats.md: 5 worlds × 200 seeds of `gcj -simulate` at 750 sources (3,000 triplets),
# reference judges, both co-primary fits, paired cluster bootstrap, nested-bootstrap D, one JSONL line per seed.
#
# Resumable: a seed already present in <OUT>/<world>.jsonl is skipped, so the script can be stopped (Ctrl-C) and
# restarted with the same settings. Every seed is its own process, so an interruption loses at most one seed.
# Seeds stopped by the time limit leave a "status":"timeout" row; they are skipped on resume too unless
# A5_RETRY_TIMEOUTS=1 (then only "ok" rows count as done; the summariser ignores a seed's timeout row once it has an
# ok row, so a successful retry needs no cleanup).
#
# Settings (environment variables, defaults in brackets; works with the bash 3.2 of macOS):
#   GOMAXPROCS [3]        CPU threads per run (the machine overheats above ≈ 6)
#   OUT [out/a5]          output directory (JSONL per world, batch.log, stderr.log, last report per world under md/)
#   A5_WORLDS ["null edgeN alt single mild"]   worlds in order (WORLDS is accepted too)
#   A5_SEEDS [101-300]    "FROM-TO" or a count N (= SEED_FROM … SEED_FROM+N−1, SEED_FROM [101])
#   A5_DREPS [50]         nested-D replicates (DREPS is accepted too)
#   A5_DREPS_BY_WORLD [""]   per-world override, e.g. "null=200,edgeN=200"
#   A5_SEED_LIMIT [2700]  wall-clock limit per seed in seconds (gcj -max-seconds; 0 = none). 45 min ≈ 3× the median
#                         seed at B = 200 / DREPS = 50. A shell watchdog kills the process 300 s after that as a backstop.
#   A5_RETRY_TIMEOUTS [0] 1 = rerun seeds whose only row is a timeout
#   SOURCES [750]  BOOT [200]  SCENARIO [reference]  EXTRA [""] (extra gcj flags)
#
# Each seed runs fit.Confirmatory (judge QC recorded but not applied unless EXTRA="-apply-qc": the reference
# simulated judges all fail the Krippendorff α ≥ 0.4 criterion, so applying it aborts the analysis).
#
# Cost per seed at 750 sources, GOMAXPROCS=3 (measured 2026-10-29): two multi-start fits ≈ 56 s, paired bootstrap
# ≈ 2.3–4.4 s per replicate, nested D ≈ 21 s + 4.3 s per replicate, matching stability ≈ 9 s; seeds whose fits hit the
# 20-round outer cap take ≈ 2× longer. B = 200 with DREPS = 50 is ≈ 12–16 min per typical seed (≈ 200–270 h for
# 1,000 seeds) and DREPS = 200 ≈ 23–27 min (≈ 380–450 h).
#
# Usage:  scripts/a5_batch.sh                      # full batch (≈ 1,000 runs; ask before launching)
#         A5_SEEDS=50 A5_DREPS_BY_WORLD="null=200,edgeN=200" scripts/a5_batch.sh     # reduced plan
#         A5_WORLDS=null A5_SEEDS=1 BOOT=50 A5_SEED_LIMIT=60 scripts/a5_batch.sh      # smoke test
# Then:   python3 scripts/a5_summarise.py out/a5/*.jsonl
set -u
cd "$(dirname "$0")/.." || exit 1 # ged-cost-judge (git describe in the records runs here)

export GOMAXPROCS="${GOMAXPROCS:-3}"
OUT="${OUT:-out/a5}"
WORLDS="${A5_WORLDS:-${WORLDS:-null edgeN alt single mild}}"
SEED_FROM="${SEED_FROM:-101}"
SEED_TO="${SEED_TO:-300}"
case "${A5_SEEDS:-}" in
  "") ;;
  *-*) SEED_FROM="${A5_SEEDS%-*}"; SEED_TO="${A5_SEEDS#*-}" ;;
  *) SEED_TO=$((SEED_FROM + A5_SEEDS - 1)) ;;
esac
SOURCES="${SOURCES:-750}"
BOOT="${BOOT:-200}"
DREPS="${A5_DREPS:-${DREPS:-50}}"
DREPS_BY_WORLD="${A5_DREPS_BY_WORLD:-}"
LIMIT="${A5_SEED_LIMIT:-2700}"
RETRY_TIMEOUTS="${A5_RETRY_TIMEOUTS:-0}"
SCENARIO="${SCENARIO:-reference}"
EXTRA="${EXTRA:-}"

# dreps_for WORLD: the A5_DREPS_BY_WORLD entry for WORLD, else DREPS.
dreps_for() {
  local kv
  for kv in $(echo "$DREPS_BY_WORLD" | tr ',' ' '); do
    if [ "${kv%%=*}" = "$1" ]; then
      echo "${kv#*=}"
      return
    fi
  done
  echo "$DREPS"
}

mkdir -p "$OUT/md"
LOG="$OUT/batch.log"
BIN="$OUT/gcj-a5"

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*" | tee -a "$LOG"; }

go build -o "$BIN" ./cmd/gcj || { log "BUILD_FAILED"; exit 1; }
log "START worlds=[$WORLDS] seeds=$SEED_FROM-$SEED_TO sources=$SOURCES boot=$BOOT d-reps=$DREPS d-reps-by-world=[$DREPS_BY_WORLD] seed-limit=${LIMIT}s retry-timeouts=$RETRY_TIMEOUTS scenario=$SCENARIO GOMAXPROCS=$GOMAXPROCS extra=[$EXTRA] git=$(git describe --tags --always --dirty 2>/dev/null)"

pid=""
wd=""
stop_children() {
  [ -n "$pid" ] && kill -TERM "$pid" 2>/dev/null
  [ -n "$wd" ] && kill "$wd" 2>/dev/null
}
trap 'stop_children; log "INTERRUPTED (resume by rerunning with the same settings)"; exit 130' INT TERM

suffix=""
[ "$SCENARIO" != "reference" ] && suffix="_$SCENARIO"
for world in $WORLDS; do
  jsonl="$OUT/$world$suffix.jsonl"
  dreps=$(dreps_for "$world")
  done_n=0
  for seed in $(seq "$SEED_FROM" "$SEED_TO"); do
    pat="^{\"world\":\"$world\",\"seed\":$seed,"
    [ "$RETRY_TIMEOUTS" = "1" ] && pat="$pat\"status\":\"ok\""
    if [ -f "$jsonl" ] && grep -q "$pat" "$jsonl"; then
      done_n=$((done_n + 1))
      continue
    fi
    t0=$(date +%s)
    # shellcheck disable=SC2086 # EXTRA is a list of flags
    "$BIN" -simulate -world "$world" -scenario "$SCENARIO" -sources "$SOURCES" -seed "$seed" -reps 1 \
      -boot "$BOOT" -d-reps "$dreps" -max-seconds "$LIMIT" -jsonl "$jsonl" -out "$OUT/md" $EXTRA \
      >/dev/null 2>>"$OUT/stderr.log" &
    pid=$!
    wd=""
    if [ "$LIMIT" -gt 0 ]; then
      # Backstop for a process that does not stop itself (gcj -max-seconds normally ends it first). It polls, so it
      # ends on its own when the seed's process does.
      (
        end=$((SECONDS + LIMIT + 300))
        while kill -0 "$pid" 2>/dev/null; do
          if [ "$SECONDS" -ge "$end" ]; then
            kill -KILL "$pid" 2>/dev/null
            break
          fi
          sleep 5
        done
      ) &
      wd=$!
    fi
    wait "$pid"
    rc=$?
    [ -n "$wd" ] && wait "$wd" 2>/dev/null
    pid=""
    wd=""
    secs=$(($(date +%s) - t0))
    case $rc in
      0) log "ok $world$suffix seed=$seed d-reps=$dreps ${secs}s" ;;
      3) log "TIMEOUT $world$suffix seed=$seed after ${secs}s (limit ${LIMIT}s; row with status timeout written)" ;;
      137) log "KILLED $world$suffix seed=$seed after ${secs}s by the watchdog (no row written)" ;;
      *) log "FAIL $world$suffix seed=$seed exit=$rc after ${secs}s (see $OUT/stderr.log)" ;;
    esac
  done
  [ "$done_n" -gt 0 ] && log "$world$suffix: skipped $done_n seeds already in $jsonl"
  log "DONE $world$suffix"
done
log "ALL_DONE"
