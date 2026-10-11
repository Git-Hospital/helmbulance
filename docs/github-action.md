# GitHub Action

The Action runs Helmbulance on a chart in your repository and writes the report to the job summary.

On each run it:
1. builds Helmbulance from the Action's own source, at the commit you pin;
2. installs kind v0.33.0, kubectl v1.37.1 and helm v4.3.0, each pinned by checksum;
3. runs `helmbulance test` in a throwaway kind cluster on the runner.

## Usage

```yaml
name: Helmbulance

on:
  pull_request:

permissions:
  contents: read

jobs:
  helmbulance:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: egezamb/helmbulance@<commit SHA>
        with:
          chart: ./chart
          values: |
            ci/values.yaml
```

Nothing is released yet, so pin the Action to a full commit SHA.

## Inputs

| Input | Default | Meaning |
|---|---|---|
| `chart` | `./chart` | Path to the chart directory, relative to the workspace. |
| `values` | | Values files, one per line. |
| `scenarios` | | Scenarios to run, one per line. Empty runs the default set. |
| `from` | | Chart that `chart-upgrade` and `zero-downtime` upgrade from. |
| `from-version` | | Version to upgrade from. Setting it enables `chart-upgrade`. |
| `timeout` | `10m` | Timeout for each install or upgrade. |
| `report-dir` | `helmbulance-report` | Where `report.md` and `report.json` are written. |
| `fail-on` | `findings` | `findings` fails the step on exit 1, 2 or 3. `error` fails only on 2 or 3. `never` always passes; read the `exit-code` output. |

There is no input for `--set` on purpose. Values given on the command line end up in logs. Put test values in a values file instead.

## Outputs

| Output | Meaning |
|---|---|
| `exit-code` | 0 clean, 1 findings, 2 error, 3 incomplete. |
| `verdict` | `PASS`, `FAIL`, `ERROR` or `INCONCLUSIVE`, from the exit code. |
| `report-dir` | The directory with `report.md` and `report.json`, for example to upload them as an artifact. |

## Security notes

- **Runners:** the Action needs a Linux runner with Docker, on x64 or arm64.
  - On a self-hosted runner it prints a warning. kind runs privileged containers on that machine, so use an ephemeral runner.
- **Go toolchain:** the Action installs Go, at the version in this repository's `go.mod`, with `actions/setup-go`. That Go stays on `PATH`, with `GOROOT` set, for the rest of your job. kind, kubectl and helm go into a private directory and do not change your `PATH`.
- **Other clusters:** before Helmbulance starts, the Action drops the variables that could point helm or kubectl at another cluster: `HELM_KUBE*`, `HELM_DRIVER*`, `KUBECONFIG` and `HELM_NAMESPACE`. The log names the variables it dropped, never their values. The CLI itself still needs the same fix (#4).
- **Chart trust:** run it on your own charts. Third-party charts are not isolated yet (#5).
- **Triggers:** use `pull_request`, not `pull_request_target`. Keep `contents: read`, and give the job no secrets.
- **Report size:** the job summary holds up to 1 MiB per step. A longer report is truncated there, and the full report stays in `report-dir`.
