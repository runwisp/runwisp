#!/usr/bin/env bash
# Memory benchmark harness for runwisp daemon.
#
# Builds the daemon with `-ldflags "-s -w"`, launches it against a throwaway
# data dir, samples /proc/<pid> for a short window, and writes a JSON report
# summarising RSS, threads/goroutines, and go_memstats_* from /metrics.
#
# Intended use: run before/after a change to quantify memory impact.
#
#   ./scripts/mem-bench.sh            # default: 20s window, 1s sample period
#   ./scripts/mem-bench.sh 60 2 out.json
#
# Arguments:
#   $1 duration_s  (default 20)
#   $2 period_s    (default 1)
#   $3 output file (default mem-bench.json next to this script)

set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
app_dir=$(cd -- "${script_dir}/.." && pwd)

duration_s="${1:-20}"
period_s="${2:-1}"
out_file="${3:-${script_dir}/mem-bench.json}"

workdir=$(mktemp -d -t runwisp-mem-XXXXXX)
trap 'rm -rf "${workdir}"' EXIT

binary="${workdir}/runwisp"
(
    cd "${app_dir}"
    CGO_ENABLED=0 go build -ldflags "-s -w" -o "${binary}" ./cmd/runwisp
)

binary_bytes=$(stat -c%s "${binary}")

cat >"${workdir}/runwisp.toml" <<'TOML'
[storage]
max_size       = "1gb"
min_free_space = "100mb"

[defaults]
timeout = "1h"

[tasks.bench]
description = "bench placeholder"
cron        = "0 0 1 1 *"
run         = "true"
TOML

export RUNWISP_PASSWORD="benchpass"

# Pick a random high port to avoid clashes with a running daemon.
port=$(( (RANDOM % 10000) + 40000 ))

(
    cd "${workdir}"
    "${binary}" daemon --port "${port}" --config runwisp.toml --data "${workdir}/data" \
        >"${workdir}/daemon.log" 2>&1 &
    echo $! >"${workdir}/pid"
)

pid=$(cat "${workdir}/pid")
cleanup() {
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
    rm -rf "${workdir}"
}
trap cleanup EXIT

# Wait until the daemon is responsive (or fail fast).
for _ in $(seq 1 50); do
    if ! kill -0 "${pid}" 2>/dev/null; then
        echo "daemon exited before becoming ready:" >&2
        cat "${workdir}/daemon.log" >&2
        exit 1
    fi
    if curl -fsS -m 1 "http://127.0.0.1:${port}/api/health" >/dev/null 2>&1 \
        || curl -fsS -m 1 "http://127.0.0.1:${port}/healthz" >/dev/null 2>&1 \
        || grep -q "Listening on" "${workdir}/daemon.log" 2>/dev/null; then
        break
    fi
    sleep 0.1
done

# Give the daemon a beat to reach steady state.
sleep 2

samples_file="${workdir}/samples.tsv"
: >"${samples_file}"

end=$(( $(date +%s) + duration_s ))
while [ "$(date +%s)" -lt "${end}" ]; do
    if ! kill -0 "${pid}" 2>/dev/null; then
        break
    fi
    rss_kb=$(awk '/^VmRSS:/ {print $2}' "/proc/${pid}/status" 2>/dev/null || echo 0)
    hwm_kb=$(awk '/^VmHWM:/ {print $2}' "/proc/${pid}/status" 2>/dev/null || echo 0)
    data_kb=$(awk '/^VmData:/ {print $2}' "/proc/${pid}/status" 2>/dev/null || echo 0)
    threads=$(awk '/^Threads:/ {print $2}' "/proc/${pid}/status" 2>/dev/null || echo 0)
    echo -e "${rss_kb}\t${hwm_kb}\t${data_kb}\t${threads}" >>"${samples_file}"
    sleep "${period_s}"
done

# Final snapshot of binary-mapped resident pages.
binary_rss_kb=$(awk -v exe="${binary}" '
    /^[0-9a-f]+-[0-9a-f]+ / { in_block=($0 ~ exe) }
    in_block && /^Rss:/ { sum += $2 }
    END { print sum+0 }
' "/proc/${pid}/smaps" 2>/dev/null || echo 0)

summary=$(awk '
    { rss[NR]=$1; hwm[NR]=$2; data[NR]=$3; thr[NR]=$4;
      if (NR==1 || $1<rss_min) rss_min=$1;
      if ($1>rss_max) rss_max=$1;
      rss_sum+=$1; thr_sum+=$4 }
    END {
      if (NR==0) { print "0 0 0 0 0 0"; exit }
      printf "%d %d %d %d %d %.2f\n", NR, rss_min, rss_max, rss[NR], rss_sum/NR, thr_sum/NR
    }
' "${samples_file}")
read -r n rss_min rss_max rss_last rss_avg thr_avg <<<"${summary}"

cat >"${out_file}" <<JSON
{
  "timestamp": "$(date -Is)",
  "duration_s": ${duration_s},
  "period_s": ${period_s},
  "samples": ${n:-0},
  "binary_bytes": ${binary_bytes},
  "rss_min_kb": ${rss_min:-0},
  "rss_max_kb": ${rss_max:-0},
  "rss_last_kb": ${rss_last:-0},
  "rss_avg_kb": ${rss_avg:-0},
  "binary_rss_kb": ${binary_rss_kb},
  "threads_avg": ${thr_avg:-0}
}
JSON

cat "${out_file}"
