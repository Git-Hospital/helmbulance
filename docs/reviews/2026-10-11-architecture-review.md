# Review: architecture at 7c9cd34, code at 3ae143f

Date: 11 October 2026. Reviewer: Kadirhan (skeptic role).

Three independent read-only reviews looked at `docs/architecture.md`, the README and the Go code: one on architecture and feasibility, one adversarial review of the code and its claims, one on strategy. The blockers were then re-checked by hand. Nothing ran on a cluster (Docker was off), so every item says how it was checked:

- **Run:** reproduced with a throwaway Go test against the real function.
- **Code:** read in this repository.
- **Source:** read in Helm or Kubernetes source or documentation.
- **Hypothesis:** needs a cluster run; the experiment that settles it is given.

The reviews started on 3ae143f. Commit 7c9cd34 rewrote the architecture while they ran, so everything below was checked again against 7c9cd34. The code did not change between the two commits.

## Already settled by 7c9cd34

The rewrite closes most of what the reviews raised about the design:

- Helm and kind run as commands; Helm 3 and Helm 4 can give different results.
- `lookup` findings name Argo CD and no longer claim the same for Flux.
- Secret drift uses a per-run HMAC; exit codes 0 to 3 are documented.
- `gitops-render` is in v0.1; network checks start with a CNI canary and port discovery.
- `process-kill` no longer targets PID 1; `probe-abuse` has a control and needs egress cut outside the cluster.
- A third-party chart mode is designed; targets are checked at connect time, including ExternalName and selector-less Services.
- No hosted infrastructure; the relation to ct and other tools is written down; "not an application security scanner" is stated.

The six doc-versus-code differences listed in PR #1 were checked against 7c9cd34 and are settled; PR #1 now says so.

## Blockers

These are in the code, and the architecture already promises the opposite.

### B1. The caller's environment reaches helm (Code, Source)

- `cmd/helmbulance/main.go:102` builds the runner with `Env: os.Environ()`. helm only gets `--kubeconfig` and `--namespace` (`internal/helm/helm.go:46`).
- helm reads `HELM_KUBEAPISERVER`, `HELM_KUBETOKEN`, `HELM_KUBECAFILE`, `HELM_KUBEINSECURE_SKIP_TLS_VERIFY`, `HELM_KUBECONTEXT`, `HELM_DRIVER` and `HELM_DRIVER_SQL_CONNECTION_STRING` from the environment (`helm --help`; `pkg/cli/environment.go`).
- **Expected failure (Hypothesis; the experiment below settles it):** a CI job with deploy variables set runs `helmbulance test`. helm installs, upgrades and uninstalls on that cluster, kubectl reads the empty sandbox namespace, and the verdict is PASS.
- This contradicts §3.1 ("no flag to run scenarios against any other cluster"): no flag is needed.
- **Fix:** pass an allowlist (PATH, HOME, helm's cache, config and data dirs, proxy variables), drop every `HELM_KUBE*` and `HELM_DRIVER*` variable, and set `HELM_DRIVER=secret`. Add a test that sets `HELM_KUBEAPISERVER=https://127.0.0.1:1` and expects the run to still use the sandbox.
- **Cluster experiment (optional):** two kind clusters; export cluster B's server, CA and token; run the `drops-set-key` fixture against A; check `helm list -A` on B.

### B2. Secret values reach transcripts and reports (Run, Code, Source)

- `internal/execx/execx.go:89` (`maskAssignments`) splits on every comma. `--set pw=hunter\,2secret` becomes `pw=***,2secret` in the transcript. **Run.**
- `cmd/helmbulance/main.go:196` (`keysOf`) splits the same way. `keysOf(["pw=hunter\,2secret", "list={a,b}"])` returns `["pw" "2secret" "list" "b}"]`, which goes into the report's `setKeys` field, documented as "keys only, never values" (`internal/report/report.go:25`). **Run.**
- `internal/scenario/upgradevalues.go:173` (`inconclusive`) copies helm's error text into `Summary` unredacted. Values-schema validation errors and chart `fail` messages can quote the value (**Source**, not run here). §3.6's redaction covers logs and events, but nothing in the code applies it to helm's own error text.
- **Fix:** mask the whole argument that follows a `--set*` flag, or parse it with Helm's own escaping rules; redact every value the tool holds in memory from command output before it reaches `Summary`; add tests for escaped commas, lists and schema errors.

### B3. Remote charts run without the third-party protections (Code)

- §4 says third-party mode is "automatic for OCI, URL and repository charts". No such mode exists in the code yet: `internal/helm/helm.go:55` installs any chart reference, and `internal/sandbox/sandbox.go:59` creates a default kind cluster.
- So an OCI or repository chart runs today in own-chart mode, where a privileged pod is root on the Docker host.
- **Fix now, cheap:** refuse non-local chart references until the hardened mode exists, and say so in the README's "Try it".

## Major: oracle accuracy

The promise "reproduced or not reported" depends on these.

### M1. Secret drift counts Secrets the release does not own (Code; the false finding itself is a Hypothesis)

- `internal/kube/kube.go:33` reads every Secret in the namespace and skips only two types (`kube.go:27-30`). `oracle.Diff` counts added keys (`internal/oracle/secrets.go:72`). `upgradevalues.go:103` turns any difference in the control into HB-UV-001 with severity high.
- `--wait` (`helm.go:51`) does not wait for non-hook Jobs, in Helm 3 or with Helm 4's watcher (**Source**).
- **Expected failure:** a Secret written by a Job, an operator or the app between two snapshots produces HB-UV-001.
- §3.5 lists this false positive, but its guard ("expected-change set per scenario") does not cover it.
- **Fix:** compare only objects listed in `helm get manifest` (hooks separately); report added keys on their own; take a snapshot only after two identical reads (the settle window §3.4 already promises).
- **Experiment:** a fixture with a non-hook Job that runs `sleep 5; kubectl create secret generic late`, tested with `-f values.yaml`. Expected today: a false HB-UV-001.

### M2. A failed treatment upgrade is INCONCLUSIVE (Code)

- `upgradevalues.go:79-80`: any error from the treatment upgrade becomes INCONCLUSIVE with exit code 3, the same as "the cluster never settled".
- Two of the cases that matter most land there:
  - a chart that refuses safely (`required`, `fail`, Bitnami's "PASSWORDS ERROR");
  - an upgrade that breaks, such as an immutable Secret or an app that crashes once the key is gone. The fixtures use `pause`, so this never shows in tests.
- This is a decision (D4 in `docs/decisions/0001-v0.1-path.md`), not a bug report.

### M3. Deliberate rotation becomes a high-severity defect (Code)

- HB-UV-001 is always "high", and its impact text talks about database passwords, whatever the key is.
- A chart that regenerates a webhook certificate on purpose (`genCA` on every render) would get that finding.
- For a contribution tool this matters most: it is exactly the report a maintainer closes as "by design".
- **Fix:** classify keys (TLS material, credentials, other); keep "high" for credentials or encryption keys read by a stateful workload; lower it otherwise; allow a list of expected rotations.

### M4. The sandbox can test a different apply path from production (Source)

- A fresh install with Helm 4 uses server-side apply. With `--server-side=auto`, a release first installed with Helm 3 keeps client-side apply on upgrade, and most production releases were installed that way.
- The `drops-set-key` fixture uses `data` rather than `stringData` (`secret.yaml:5`), a workaround for how a key removed from `stringData` behaves under client-side apply. That behaviour has not been checked under server-side apply.
- §3.1 says results can differ but no step covers it.
- **Fix:** record the Helm version and the apply method in the report, and run the fixtures under Helm 3 and Helm 4.

### M5. The fixtures are not run by any test (Code)

- No `_test.go` file references `testdata/`.
- §3.5 says "an oracle change is only accepted when the bad fixture still fails and the good one still passes"; nothing enforces it.
- **Fix:** an end-to-end test on kind, behind a build tag, run in CI under Helm 3 and Helm 4.

## Minor

- **Fix text:** HB-UV-001 and HB-UV-002 recommend `lookup` (`upgradevalues.go:114-116`, `140-142`). The `keeps-*` twins pass this scenario but regenerate or drop keys under Argo CD, which `gitops-render` will flag. Put `existingSecret` first in the fix text.
- **Repro lines:** they contain `--set k=***`, which cannot be re-run as written, and they leave out the snapshot step. A `repro.sh` with named placeholders would fix both.
- **CA check:** `sandbox.go:89-93` compares the recorded fingerprint with the kubeconfig's CA. It never contacts the cluster. That guards against a swapped kubeconfig; it is worth saying in §3.1 what it does not prove.
- **Fixed names:** the scenario's release and namespace names (`upgradevalues.go:23`, `33`) collide if `--parallel` above 1 is ever enabled.

## What could not be broken

- **Value reuse:** an upgrade with no values flags reuses the last release's values in Helm 3.22 and 4.3, and `--reset-values -f` gives chart defaults plus the files (Source).
- **Helm 4 flags:** a bare `--wait` means the watcher, the flags still parse, and the release Secret type is unchanged (Source).
- **Cluster handling:** deletes touch only clusters Helmbulance created, and the user's kubeconfig is never changed (Code).
- **Hashing:** the HMAC key is per run, and reports carry no hashes (Code).
- **Fixtures:** the defective fixtures do trigger their defects (Code).
- **Checks:** `go build`, `go vet` and `go test ./...` pass (Run).
