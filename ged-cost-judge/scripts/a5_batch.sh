#!/usr/bin/env bash
# Appendix A.5 batch of decisions-stats.md: 5 worlds × 200 seeds of `gcj -simulate` at 750 sources (3,000 triplets),
# reference judges, both co-primary fits, paired cluster bootstrap, nested-bootstrap D, one JSONL line per seed.
#
# Resumable: a seed with a complete row (ending in "}") in <OUT>/<world>.jsonl is skipped, so the script can be stopped
# (Ctrl-C) and restarted with the same settings. Every seed is its own process, so an interruption loses at most one
# seed; a line cut by the interruption does not count as done.
# Seeds stopped by the time limit leave a "status":"timeout" row; they are skipped on resume too unless
# A5_RETRY_TIMEOUTS=1 (then a seed is skipped only if it has a complete row that is not a timeout; rows from before
# the status field count as done). The summariser ignores a seed's timeout row once it has an ok row, so a successful
# retry needs no cleanup.
#
# Settings (environment variables, defaults in brackets; works with the bash 3.2 of macOS):
#   GOMAXPROCS [3]        CPU threads per run (the machine overheats above ≈ 6)
#   OUT [out/a5]          output directory (JSONL per world, batch.log, stderr.log, last report per world under md/)
#   A5_WORLDS ["null edgeN alt single mild"]   worlds in order (WORLDS is accepted too)
#   A5_SEEDS [101-300]    "FROM-TO" or a count N (= SEED_FROM … SEED_FROM+N−1, SEED_FROM [101]); non-negative
#                         integers with FROM ≤ TO
#   A5_DREPS [50]         nested-D replicates (DREPS is accepted too)
#   A5_DREPS_BY_WORLD [""]   per-world override, comma-separated world=integer, e.g. "null=200,edgeN=200"
#   A5_SEED_LIMIT [2700]  wall-clock limit per seed in seconds (gcj -max-seconds; 0 = none). 45 min ≈ 3× the median
#                         seed at B = 200 / DREPS = 50. A shell watchdog kills the process 300 s after that as a backstop.
#                         Where something caps the run time of one command (e.g. an agent sandbox at ≈ 30 min), set
#                         A5_SEED_LIMIT well below that cap (limit + 300 s watchdog + margin): otherwise the seed is
#                         killed from outside with no row written and is rerun from scratch on every resume.
#   A5_RETRY_TIMEOUTS [0] 1 = rerun seeds whose only complete rows are timeouts
#   SOURCES [750]  BOOT [200]  SCENARIO [reference]  EXTRA [""] (extra gcj flags)
#
# Each seed runs fit.Confirmatory with the judge QC recorded but not applied unless EXTRA="-apply-qc". The QC excludes
# a judge on the T1 reversal gate or the tie share; Krippendorff α is descriptive (decisions §9 as revised
# 2026-10-29; their pairwise α ≈ 0.28–0.35 would fail the old 0.4 bar). The reference judges pass the gate and tie
# share (internal/fit/simulate_qc_test.go checks 150 sources), so -apply-qc normally excludes nobody; with few sources
# the T1 gate can fail them all, and Confirmatory then stops with "every judge failed the QC".
#
# Cost per seed at 750 sources, GOMAXPROCS=3 (measured 2026-10-29): two multi-start fits ≈ 56 s, paired bootstrap
# ≈ 2.3–4.4 s per replicate, nested D ≈ 21 s + 4.3 s per replicate, matching stability ≈ 9 s; seeds whose fits hit the
# 20-round outer cap take ≈ 2× longer. B = 200 with DREPS = 50 is ≈ 12–16 min per typical seed (≈ 200–270 h for
# 1,000 seeds) and DREPS = 200 ≈ 23–27 min (≈ 380–450 h).
#
# Exit status: 0 every seed run (or skipped) completed; 3 the only problems were timeouts (rerun with
# A5_RETRY_TIMEOUTS=1 if wanted); 1 any seed FAILed or was KILLED, or the build failed; 2 invalid settings;
# 130 interrupted.
#
# Usage:  scripts/a5_batch.sh                      # full batch (≈ 1,000 runs; ask before launching)
#         A5_SEEDS=50 A5_DREPS_BY_WORLD="null=200,edgeN=200" scripts/a5_batch.sh     # reduced plan
#         A5_WORLDS=null A5_SEEDS=1 BOOT=50 A5_SEED_LIMIT=60 scripts/a5_batch.sh      # smoke test
# Then:   python3 scripts/a5_summarise.py out/a5/*.jsonl
set -u
cd "$(dirname "$0")/.." || exit 1 # ged-cost-judge (git describe in the records runs here)

die() {
  echo "a5_batch.sh: $*" >&2
  exit 2
}
is_uint() {
  case "$1" in
    "" | *[!0-9]*) return 1 ;;
  esac
}

export GOMAXPROCS="${GOMAXPROCS:-3}"
OUT="${OUT:-out/a5}"
WORLDS="${A5_WORLDS:-${WORLDS:-null edgeN alt single mild}}"
SEED_FROM="${SEED_FROM:-101}"
SEED_TO="${SEED_TO:-300}"
case "${A5_SEEDS:-}" in
  "") ;;
  *-*) SEED_FROM="${A5_SEEDS%-*}"; SEED_TO="${A5_SEEDS#*-}" ;;
  *)
    is_uint "$A5_SEEDS" && is_uint "$SEED_FROM" || die "A5_SEEDS=\"$A5_SEEDS\" (SEED_FROM=\"$SEED_FROM\"): need a count N or FROM-TO of non-negative integers"
    SEED_TO=$((10#$SEED_FROM + 10#$A5_SEEDS - 1))
    ;;
esac
is_uint "$SEED_FROM" && is_uint "$SEED_TO" || die "seed range FROM=\"$SEED_FROM\" TO=\"$SEED_TO\" (A5_SEEDS=\"${A5_SEEDS:-}\"): FROM and TO must be non-negative integers"
SEED_FROM=$((10#$SEED_FROM)) # 10#: a leading zero is not octal
SEED_TO=$((10#$SEED_TO))
[ "$SEED_FROM" -le "$SEED_TO" ] || die "seed range $SEED_FROM-$SEED_TO is empty (need FROM ≤ TO, or a count N ≥ 1)"
SOURCES="${SOURCES:-750}"
BOOT="${BOOT:-200}"
DREPS="${A5_DREPS:-${DREPS:-50}}"
DREPS_BY_WORLD="${A5_DREPS_BY_WORLD:-}"
for kv in $(echo "$DREPS_BY_WORLD" | tr ',' ' '); do
  [ "${kv%%=*}" != "$kv" ] && [ -n "${kv%%=*}" ] && is_uint "${kv#*=}" ||
    die "A5_DREPS_BY_WORLD entry \"$kv\" is not world=integer (e.g. \"null=200,edgeN=200\")"
done
LIMIT="${A5_SEED_LIMIT:-2700}"
is_uint "$LIMIT" || die "A5_SEED_LIMIT=\"$LIMIT\" must be a non-negative integer (seconds)"
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

# seed_done JSONL WORLD SEED: the seed has a complete row (not cut by an interruption); with A5_RETRY_TIMEOUTS=1 one
# that is not a timeout. Every row ends with its "secs" object, so a complete row ends in "}}" (a line cut just after
# a nested object would still end in a single "}").
seed_done() {
  [ -f "$1" ] || return 1
  local pre="^{\"world\":\"$2\",\"seed\":$3,"
  if [ "$RETRY_TIMEOUTS" = "1" ]; then
    grep "$pre" "$1" | grep '}}$' | grep -qv '"status":"timeout"'
  else
    grep -q "$pre.*}}\$" "$1"
  fi
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
n_timeout=0
n_fail=0
for world in $WORLDS; do
  jsonl="$OUT/$world$suffix.jsonl"
  dreps=$(dreps_for "$world")
  done_n=0
  # A while loop rather than seq: BSD seq counts down when FROM > TO.
  seed=$((SEED_FROM - 1))
  while [ "$seed" -lt "$SEED_TO" ]; do
    seed=$((seed + 1))
    if seed_done "$jsonl" "$world" "$seed"; then
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
      # Backstop for a process that does not stop itself (gcj -max-seconds normally ends it first).
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
    # Stop the poller now rather than waiting out its sleep (that would inflate the seed time), and before the PID
    # of the finished seed can be reused by another process.
    if [ -n "$wd" ]; then
      kill "$wd" 2>/dev/null
      wait "$wd" 2>/dev/null
    fi
    pid=""
    wd=""
    secs=$(($(date +%s) - t0))
    case $rc in
      0) log "ok $world$suffix seed=$seed d-reps=$dreps ${secs}s" ;;
      3)
        n_timeout=$((n_timeout + 1))
        log "TIMEOUT $world$suffix seed=$seed after ${secs}s (limit ${LIMIT}s; row with status timeout written)"
        ;;
      137)
        n_fail=$((n_fail + 1))
        log "KILLED $world$suffix seed=$seed after ${secs}s (watchdog or external SIGKILL; no row written)"
        ;;
      *)
        n_fail=$((n_fail + 1))
        log "FAIL $world$suffix seed=$seed exit=$rc after ${secs}s (see $OUT/stderr.log)"
        ;;
    esac
  done
  [ "$done_n" -gt 0 ] && log "$world$suffix: skipped $done_n seeds already in $jsonl"
  log "DONE $world$suffix"
done
log "ALL_DONE timeouts=$n_timeout failed-or-killed=$n_fail"
[ "$n_fail" -gt 0 ] && exit 1
[ "$n_timeout" -gt 0 ] && exit 3
exit 0
