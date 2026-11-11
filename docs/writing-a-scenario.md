# Writing a scenario

A scenario is one day-two situation tested against a live release: an upgrade, a reinstall, a restart under traffic. This guide shows what a new one needs, using `gitops-render` (`internal/scenario/gitopsrender.go`, about 250 lines, and its tests) as the worked example.

## The interface

```go
type Scenario interface {
	Name() string        // the --scenario name, such as "gitops-render"
	Description() string // one sentence, printed by `helmbulance list-scenarios`
	Run(ctx context.Context, env *Env) Result
}
```

It is in `internal/scenario/scenario.go`, with `Env` (the runner, the helm and kube clients, the chart, the values and the flags), `Result` and `Finding`. A scenario is registered by adding it to `var scenarios` in `cmd/helmbulance/main.go`. The order there is the order `test` runs them in.

## The rules

Each rule below is enforced by code that already exists. Use it rather than writing your own.

| Rule | Why | Use |
|---|---|---|
| A run that cannot finish is `INCONCLUSIVE`, never `FAIL` | A finding must be reproduced, not inferred from a broken run | `inconclusive(res, why, err)`; `secretsInconclusive` for a Secret read |
| A tool or cluster error is not the chart's fault | A timeout, a pull error or a lost API server says nothing about the chart | `IsEnvironmentError(ctx, err)`; `timedOutWaiting(ctx, err)` for a `--wait` that ran out with the release applied |
| Each scenario has its own release name and namespace | Scenarios run one after another on one cluster | `const release, ns = "hb-…", "hb-…"` at the top of `Run` |
| Start clean | A run killed before its cleanup leaves the release behind on a reused `--cluster` | `startClean(ctx, env, release, ns)` before the first install |
| Clean up, even after Ctrl-C | So the next run on the same cluster can start | `defer` the uninstall and namespace delete with `ctx, cancel := cleanupContext(ctx)` |
| Read Secrets only once they stop changing, and only the release's own | An operator or a Job can write Secrets for a while after install | `newSecretReader(ctx, env, release, ns).snapshot()`, which uses `oracle.Settle` |
| Watch workloads until they settle | `helm --wait` returns as soon as pods are ready; a crash seconds later is missed | `Settle(ctx, check)` in `settle.go` |
| A summary that says what was not checked | A PASS must not claim more than was compared | `withNotes(summary, note(label, items)...)`; `noSecretsTested` when no key was compared |
| Reproduce steps are commands that ran | A reader can run them on their own cluster | `reproLine(c.Line)` on `env.Runner.Transcript()` entries |

Values never reach a report. Secret values are compared as keyed hashes (`env.Hasher`), and `--set` values are masked in the transcript.

## Findings

A finding has an ID of the form `HB-<area>-<nnn>`, such as `HB-GR-001`. It also has a title, a severity (D5: see `changeSeverity` in `severity.go` for Secrets), the evidence, the impact, a fix, and the reproduce steps. A new area gets a new prefix. Describe the ID with its scenario in `docs/architecture.md` §3.4; a change there needs both maintainers (decision 0003).

## The fixture pair

Every finding ID needs a pair in `testdata/charts/`: a small chart with the defect, and its fixed twin, the same chart without it. Keep them tiny. Use `registry.k8s.io/pause` or the pinned busybox from `internal/scenario/sentinel.go`, no ServiceAccount token, a read-only root filesystem and dropped capabilities. Copy the `securityContext` lines of `testdata/charts/gitops-values-secret`. Say in `Chart.yaml`'s `description` what the defect is and which chart is its twin.

Then add both to `testdata/verdicts.yaml`:

```yaml
  - name: gitops-lookup-random
    pair: gitops-secret
    role: defect
    command: test
    args: [testdata/charts/gitops-lookup-random, --scenario, gitops-render]
    exit: 1
    verdicts: {gitops-render: FAIL}
    findings:
      HB-GR-001: []          # texts the evidence must contain, so the finding is about the right object
  - name: gitops-values-secret
    pair: gitops-secret
    role: fixed
    command: test
    args: [testdata/charts/gitops-values-secret, --scenario, gitops-render]
    exit: 0
    verdicts: {gitops-render: PASS}
```

`go test ./tools/verdicts` checks the table without a cluster. It also runs the coverage lock: a finding ID in `internal/` with no pair fails it, unless it is listed under `uncovered` with the reason. `scripts/fixtures.sh -only gitops-lookup-random,gitops-values-secret` runs the two cases on kind. CI runs every case under Helm 3 and Helm 4. [verdict-quality.md](verdict-quality.md) explains the table in full.

## Unit tests

Scenario tests need no cluster. Write small fake `helm` and `kubectl` shell scripts, put them first on `PATH`, and run the real `Run`. `fakeGitopsTools` in `internal/scenario/gitopsrender_test.go` and `fakeKubectl` in `zerodowntime_test.go` show the pattern: a `case "$(basename "$0") $*" in` over the calls the scenario makes, with answers read from files the test writes. `TestMain` in `internal/scenario/main_test.go` shortens the waits (`settleTimeout`, `controlDuration` and the rest), so the tests do not sleep through production timings. Add a variable there if your scenario waits for something new.

## The pull request

The template (`.github/pull_request_template.md`) asks for:

- a test or fixture that fails on `main`;
- the output before and after on kind;
- the mutations: each line of the fix broken on purpose, and the test that catches it.

Ask Ege or Kadirhan to review a new scenario. A new verdict needs the maintainer who did not write it (0003).
