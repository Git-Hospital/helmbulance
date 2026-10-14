# Known-bug corpus

Real charts with a documented day-two defect, each next to the version that fixed it. The corpus answers one question: does Helmbulance find defects that people already reported, and stay quiet about the fix? Decision [0001](decisions/0001-v0.1-path.md) uses it for the check on 8 November (D8).

## How an entry is chosen

- The defect has a public issue, and a published chart version shows it.
- A published version fixes it. The two versions should differ in little else.
- Both install on kind without changes to the chart. Values needed only to install on a current Kubernetes go into a values file under `testdata/corpus/`.
- The archive is pinned by sha256.

## How it runs

`.github/workflows/corpus.yml` runs every entry in its own job, through the Action in this repository (D3). A corpus chart is someone else's code, and the protections for third-party charts arrive in v0.3. Until then each job has a fresh VM, a token with no permissions, no secrets and no caches.

Each job compares the exit code and the set of finding IDs with the row below. A finding that is not in the row fails the job, and so does a missing one.

## Entries

| ID | Chart | Upstream | Version | Expected | Seen |
|---|---|---|---|---|---|
| HBC-01 | grafana | [grafana/helm-charts#10](https://github.com/grafana/helm-charts/issues/10), fixed by [#62](https://github.com/grafana/helm-charts/pull/62) | 6.9.2 (defect) | exit 1: HB-UV-001, HB-UV-003 | 13 Oct 2026, as expected |
| | | | 6.9.3 (fix) | exit 1: HB-UV-003 | 13 Oct 2026, as expected |

"Seen" is from `helmbulance test <archive> --scenario upgrade-without-values -f testdata/corpus/hbc-01-grafana/values.yaml` on kind, with Helm v4.0.4 on arm64.

### HBC-01: grafana regenerates the admin password

- **6.9.2** writes `admin-password` with `randAlphaNum` on every render, so every upgrade changes it: HB-UV-001 on `admin-password`. The Deployment hashes the Secret template into a `checksum/secret` annotation, so the pods are replaced as well: HB-UV-003.
- **6.9.3** reads the existing Secret with `lookup` and keeps the password: HB-UV-001 is gone. HB-UV-003 stays: the pods of `Deployment/grafana` are still replaced by an upgrade that changes no values.
  - It is the restart on the first upgrade described in [first-upgrade-restart](case-studies/first-upgrade-restart.md): a second upgrade that changes nothing replaces no pods on 6.9.3, and replaces them again on 6.9.2.
  - Likely cause, read from the template: `checksum/secret` hashes a fresh render of `secret.yaml`. At install `lookup` finds nothing and the render holds a new random password; at the first upgrade it finds the Secret. The hash changes once.
  - So the fixed version is not a clean PASS. The corpus expects HB-UV-003 on both versions and HB-UV-001 on 6.9.2 only.

## What the corpus has shown so far

- **Known defects found:** 1 of 1 (HBC-01).
- **Findings on a fixed version:** 1 (HB-UV-003 on grafana 6.9.3). It is a second defect that the fix did not touch, not a false finding. For D8 this means "a finding on the fixed version" has to be read per finding ID.
