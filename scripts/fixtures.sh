#!/usr/bin/env bash
# Runs every fixture pair and checks each against its expected exit code and its expected findings:
# the chart with the defect must fail (1) with exactly the finding IDs listed here, its fixed twin
# must pass (0) with none. Exits non-zero if any result differs.
#
#   scripts/fixtures.sh [--cluster helmbulance-xxxx]   # reuse a sandbox for the test pairs;
#                                                      # otherwise each run makes one
#
# The matrix pair always makes its own cluster, on Kubernetes $K8S_VERSION.
#
# Needs a built bin/helmbulance, Docker, kind and helm.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

extra=("$@")
out=$(mktemp -d)
key=$(openssl rand -hex 32)
k8s=${K8S_VERSION:-1.37}
failed=0

# ids prints the finding IDs of a report.json on one line, sorted.
ids() {
  grep -o '"id": *"HB-[A-Z0-9-]*"' "$1" | sed 's/.*"\(HB-[^"]*\)"/\1/' | sort -u | tr '\n' ' ' | sed 's/ $//'
}

# check compares one result with what the fixture must give. The IDs are compared as a set, so a
# finding that goes missing fails the fixture even when another one keeps the exit code at 1.
check() {
  local name=$1 got=$2 want=$3 want_ids=$4 dir=$5 log=$6
  local got_ids
  # A setup error (2) stops before the run, so it writes no report. The only ones here are a missing
  # chart or values file, and the message proves the run stopped for that and not for another
  # usage error.
  if [ "$want" = 2 ]; then
    if [ "$got" = 2 ] && [ ! -e "$dir/report.json" ] && grep -qE 'path not found|values file not found' "$log"; then
      echo "ok    $name (exit $got)"
    else
      echo "FAIL  $name (exit $got, want 2, no report.json and 'not found')  log: $log"
      failed=1
    fi
    return
  fi
  # A run that wrote no report is never "no findings".
  if [ ! -s "$dir/report.json" ]; then
    echo "FAIL  $name (exit $got, want $want; no report.json)  log: $log"
    failed=1
    return
  fi
  got_ids=$(ids "$dir/report.json")
  if [ "$got" = "$want" ] && [ "$got_ids" = "$want_ids" ]; then
    echo "ok    $name (exit $got) $got_ids"
  else
    echo "FAIL  $name (exit $got, want $want; findings '$got_ids', want '$want_ids')  log: $log"
    failed=1
  fi
}

# A fixture that never becomes ready waits out the whole --timeout on each install, so it sets a
# shorter one: wait_for=45s run ...
run() {
  local name=$1 want=$2 want_ids=$3
  shift 3
  ./bin/helmbulance test "$@" ${extra[@]+"${extra[@]}"} --timeout "${wait_for:-3m}" --report-dir "$out/$name" >"$out/$name.log" 2>&1
  check "$name" $? "$want" "$want_ids" "$out/$name" "$out/$name.log"
}

run regenerates-secret 1 "HB-UV-001 HB-UV-003" testdata/charts/regenerates-secret --scenario upgrade-without-values
run keeps-secret       0 ""                    testdata/charts/keeps-secret --scenario upgrade-without-values
# The regenerated password crash-loops the database, so the control upgrade times out (#35).
run regenerates-db-password 1 "HB-UV-001"  testdata/charts/regenerates-db-password --scenario upgrade-without-values
# Not pairs: Secrets the release stops rendering or never renders must not be findings.
run generate-once      0 ""                    testdata/charts/generate-once --scenario upgrade-without-values
# A workload rewrites a Secret the chart does not render.
run runtime-secret     0 ""                    testdata/charts/runtime-secret -f testdata/charts/runtime-secret/values.yaml --scenario upgrade-without-values
run drops-set-key      1 "HB-UV-002"           testdata/charts/drops-set-key --set "encryptionKey=$key" --scenario upgrade-without-values
run keeps-set-key      0 ""                    testdata/charts/keeps-set-key --set "encryptionKey=$key" --scenario upgrade-without-values
run upgrade-v2-breaks  1 "HB-CU-001"           testdata/charts/upgrade-v2-breaks --scenario chart-upgrade --from testdata/charts/upgrade-v1 --from-version 1.0.0
run upgrade-v2-ok      0 ""                    testdata/charts/upgrade-v2-ok --scenario chart-upgrade --from testdata/charts/upgrade-v1 --from-version 1.0.0
# v2 crash-loops, so --wait times out (the full --timeout): a finding, not INCONCLUSIVE (#28). Its twin is upgrade-v2-ok.
run upgrade-v2-crashes 1 "HB-CU-001"          testdata/charts/upgrade-v2-crashes --scenario chart-upgrade --from testdata/charts/upgrade-v1 --from-version 1.0.0
run reinstall-loses-creds 1 "HB-RI-001"         testdata/charts/reinstall-loses-creds --scenario reinstall
run reinstall-keeps-creds 0 ""                  testdata/charts/reinstall-keeps-creds --scenario reinstall
# The same defect with the password read through a projected volume, which reinstall did not see (#46).
run reinstall-loses-creds-projected 1 "HB-RI-001" testdata/charts/reinstall-loses-creds-projected --scenario reinstall
# Its twin: the items mount only a key that does not change, so the new password reaches no kept data (#73).
run reinstall-projected-items-other-key 0 "" testdata/charts/reinstall-projected-items-other-key --scenario reinstall
# A pre-install hook Secret kept on uninstall is created again, with new values, by the next install (HB-RI-004). Its twin reuses them with lookup.
run reinstall-replaces-kept-hook 1 "HB-RI-004"  testdata/charts/reinstall-replaces-kept-hook --scenario reinstall
run reinstall-reuses-kept-hook 0 ""             testdata/charts/reinstall-reuses-kept-hook --scenario reinstall
# The same hook without keep: Helm leaves it, but the chart never asked to keep it, so a new one is not a finding (#43).
run reinstall-replaces-plain-hook 0 ""          testdata/charts/reinstall-replaces-plain-hook --scenario reinstall
# The kept-hook pair with a client that never becomes ready: the replaced kept Secret is still seen; its twin proves nothing (#43).
wait_for=45s run reinstall-unready-replaces-kept 1 "HB-RI-004" testdata/charts/reinstall-unready-replaces-kept --scenario reinstall
wait_for=45s run reinstall-unready-reuses-kept 3 ""            testdata/charts/reinstall-unready-reuses-kept --scenario reinstall
# Not a pair: a templated claim is deleted with the release, so new credentials lose nothing.
run reinstall-templated-pvc 0 ""                testdata/charts/reinstall-templated-pvc --scenario reinstall
# A password generated behind lookup is new on every helm template render, as under Argo CD. Its twin takes it from values.
run gitops-lookup-random 1 "HB-GR-001"          testdata/charts/gitops-lookup-random --scenario gitops-render
run gitops-values-secret 0 ""                   testdata/charts/gitops-values-secret --scenario gitops-render
# Helm keeps these on upgrade, but Argo CD renders an install and runs the hook on every sync (#45).
run gitops-install-hook  1 "HB-GR-001"          testdata/charts/gitops-install-hook --scenario gitops-render
run gitops-install-only  1 "HB-GR-001"          testdata/charts/gitops-install-only --scenario gitops-render
# A hook that also runs on upgrade changes under Helm too: upgrade-without-values reports it, not this.
run gitops-upgrade-hook  0 ""                   testdata/charts/gitops-upgrade-hook --scenario gitops-render
run zd-recreate        1 "HB-ZD-001"           testdata/charts/zd-recreate --scenario zero-downtime
run zd-rolling         0 ""                    testdata/charts/zd-rolling --scenario zero-downtime

# Static scan: no cluster, seconds. --strict makes candidates exit 1.
scan() {
  local name=$1 want=$2 want_ids=$3
  shift 3
  ./bin/helmbulance scan "$@" --strict --report-dir "$out/scan-$name" >"$out/scan-$name.log" 2>&1
  check "scan $name" $? "$want" "$want_ids" "$out/scan-$name" "$out/scan-$name.log"
}
scan regenerates-secret 1 "HB-SCAN-003" testdata/charts/regenerates-secret
scan keeps-secret       0 ""            testdata/charts/keeps-secret
# A quoted default that renders another field unquoted (env value becomes valueFrom). Its twin renders scalars alike.
scan scan-unquoted-env  1 "HB-SCAN-004" testdata/charts/scan-unquoted-env
scan scan-typed-env     0 ""            testdata/charts/scan-typed-env
# A value that sets a variable the chart also sets names it twice (Helm 4 rejects it). Its twin leaves its own entry out.
scan scan-duplicate-env 1 "HB-SCAN-005" testdata/charts/scan-duplicate-env --set extraEnv.DB_PASSWORD=from-values
scan scan-single-env    0 ""            testdata/charts/scan-single-env --set extraEnv.DB_PASSWORD=from-values
scan upgrade-v2-breaks  1 "HB-SCAN-001" testdata/charts/upgrade-v2-breaks --from testdata/charts/upgrade-v1 --from-version 1.0.0
scan upgrade-v2-ok      0 ""            testdata/charts/upgrade-v2-ok --from testdata/charts/upgrade-v1 --from-version 1.0.0
# A check that could not run is never a clean pass (#50): a missing chart or values file is a setup
# error (#68), and a chart that does not render leaves the scan INCONCLUSIVE.
scan missing-chart      2 ""            ./testdata/charts/does-not-exist
scan scan-render-fails  3 ""            testdata/charts/scan-render-fails
scan missing-values     2 ""            testdata/charts/regenerates-secret -f testdata/charts/regenerates-secret/does-not-exist.yaml

# Kubernetes matrix, on one version.
matrix() {
  local name=$1 want=$2 want_ids=$3
  shift 3
  ./bin/helmbulance matrix "$@" --k8s "$k8s" --report-dir "$out/matrix-$name" >"$out/matrix-$name.log" 2>&1
  check "matrix $name" $? "$want" "$want_ids" "$out/matrix-$name" "$out/matrix-$name.log"
}
matrix deprecated-api 1 "HB-K8S-003" testdata/charts/deprecated-api
matrix current-api    0 ""           testdata/charts/current-api

exit $failed
