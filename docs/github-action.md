# GitHub Action

The Action runs Helmbulance on a chart in your repository and writes the report to the job summary.

On each run it:
1. builds Helmbulance from the Action's own source, at the commit you pin;
2. installs kind v0.33.0, kubectl v1.37.1 and helm v4.3.0, each pinned by checksum;
3. runs `helmbulance test` in a throwaway kind cluster on the runner.

## Status: experimental

The Action passes fewer options than the CLI, and that changes what it can find ([#48](https://github.com/Git-Hospital/helmbulance/issues/48)):

- **No install-only values.** The Action has no `--set` input, and the files in `values` are given both at install and in the upgrade. So `upgrade-without-values` cannot find a value the upgrade leaves out (HB-UV-002); through the Action it finds HB-UV-001 and HB-UV-003 only. Run the CLI with `--set` for that check.
- **No input for `--traffic-path`, `--traffic-target` or `--expected-rotation`.** `zero-downtime` runs by default and ends INCONCLUSIVE when `/` does not answer 2xx or the picked port is wrong, which fails the step. Leave it out with `scenarios` in that case. A chart that renews a certificate on purpose gets HB-UV-001 on every run.

Use it on your own charts and read the report, not only the exit code.

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
      - uses: Git-Hospital/helmbulance@<commit SHA>
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
| `from` | | Chart that `chart-upgrade` and `zero-downtime` upgrade from: a path, or with `allow-remote` a `repo/chart`, `oci://…` or URL reference. |
| `allow-remote` | `false` | `true` lets `chart` and `from` name a chart from a repository, OCI registry or URL. Without it such a reference ends the run with exit 2. |
| `from-version` | | Version to upgrade from. Setting it enables `chart-upgrade`. For a local `chart`, also set `from` to the earlier chart; without it the run stops with exit 2, because helm ignores the version of a path. |
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

- **Runners:** the Action needs a Linux runner with Docker, on x64 or arm64. It stops with an error before installing anything when the OS is not Linux or `docker info` fails.
  - On a self-hosted runner it prints a warning. kind runs privileged containers on that machine, so use an ephemeral runner.
- **Go toolchain:** the Action installs Go, at the version in this repository's `go.mod`, with `actions/setup-go`. That Go stays on `PATH`, with `GOROOT` set, for the rest of your job. kind, kubectl and helm go into a private directory and do not change your `PATH`.
- **Other clusters:** before Helmbulance starts, the Action drops the variables that could point helm or kubectl at another cluster: `HELM_KUBE*`, `HELM_DRIVER*`, `KUBECONFIG` and `HELM_NAMESPACE`. The log names the variables it dropped, never their values. The CLI passes helm, kind and kubectl only an allowlist of variables as well, so this is a second layer.
- **Chart trust:** run it on your own charts. Third-party charts are not isolated yet (#5), so `allow-remote` is off by default. Turn it on only for a chart you trust, for example your own published chart as `from`.
- **Triggers:** use `pull_request`, not `pull_request_target`. Keep `contents: read`, and give the job no secrets.
- **Report size:** the job summary holds up to 1 MiB per step. A longer report is truncated there, and the full report stays in `report-dir`.
