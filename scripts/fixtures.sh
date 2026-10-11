#!/usr/bin/env bash
# Runs every fixture pair and checks each against its expected exit code: the chart with the defect
# must fail (1), its fixed twin must pass (0). Exits non-zero if any result differs.
#
#   scripts/fixtures.sh [--cluster helmbulance-xxxx]   # reuse a sandbox; otherwise each run makes one
#
# Needs a built bin/helmbulance, Docker, kind and helm.
set -uo pipefail
cd "$(dirname "$0")/.."

extra=("$@")
out=$(mktemp -d)
key=$(openssl rand -hex 32)
failed=0

run() {
  local name=$1 want=$2
  shift 2
  ./bin/helmbulance test "$@" ${extra[@]+"${extra[@]}"} --timeout 3m --report-dir "$out/$name" >"$out/$name.log" 2>&1
  local got=$?
  local findings
  findings=$(grep -o '^## HB-[A-Z0-9]*-[0-9]*' "$out/$name/report.md" 2>/dev/null | sed 's/## //' | tr '\n' ' ')
  if [ "$got" = "$want" ]; then
    echo "ok    $name (exit $got) $findings"
  else
    echo "FAIL  $name (exit $got, want $want) $findings  log: $out/$name.log"
    failed=1
  fi
}

run regenerates-secret 1 testdata/charts/regenerates-secret --scenario upgrade-without-values
run keeps-secret       0 testdata/charts/keeps-secret --scenario upgrade-without-values
run drops-set-key      1 testdata/charts/drops-set-key --set "encryptionKey=$key" --scenario upgrade-without-values
run keeps-set-key      0 testdata/charts/keeps-set-key --set "encryptionKey=$key" --scenario upgrade-without-values
run upgrade-v2-breaks  1 testdata/charts/upgrade-v2-breaks --scenario chart-upgrade --from testdata/charts/upgrade-v1 --from-version 1.0.0
run upgrade-v2-ok      0 testdata/charts/upgrade-v2-ok --scenario chart-upgrade --from testdata/charts/upgrade-v1 --from-version 1.0.0
run zd-recreate        1 testdata/charts/zd-recreate --scenario zero-downtime
run zd-rolling         0 testdata/charts/zd-rolling --scenario zero-downtime

# Static scan: no cluster, seconds. --strict makes candidates exit 1.
scan() {
  local name=$1 want=$2
  shift 2
  ./bin/helmbulance scan "$@" --strict --report-dir "$out/scan-$name" >"$out/scan-$name.log" 2>&1
  local got=$?
  if [ "$got" = "$want" ]; then echo "ok    scan $name (exit $got)"; else echo "FAIL  scan $name (exit $got, want $want)  log: $out/scan-$name.log"; failed=1; fi
}
scan regenerates-secret 1 testdata/charts/regenerates-secret
scan keeps-secret       0 testdata/charts/keeps-secret
scan upgrade-v2-breaks  1 testdata/charts/upgrade-v2-breaks --from testdata/charts/upgrade-v1 --from-version 1.0.0
scan upgrade-v2-ok      0 testdata/charts/upgrade-v2-ok --from testdata/charts/upgrade-v1 --from-version 1.0.0

exit $failed
