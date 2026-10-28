# Known-bug corpus

Real charts with a documented day-two defect, each next to the version that fixed it. The corpus answers one question: does Helmbulance find defects that people already reported, and stay quiet about the fix? Decision [0001](decisions/0001-v0.1-path.md) uses it for the check on 8 November (D8).

## How an entry is chosen

- The defect has a public issue, and a published chart version shows it.
- A published version fixes it. The two versions should differ in little else.
- Both install on kind without changes to the chart. Values needed only to install on a current Kubernetes go into a values file under `testdata/corpus/`.
- The archive is pinned by sha256.
- A defect that no published version fixes can still be an entry, marked "no published fix". It counts toward the defects found; there is no fixed version to stay quiet on.

## How it runs

`.github/workflows/corpus.yml` runs every entry in its own job, through the Action in this repository (D3). A corpus chart is someone else's code, and the protections for third-party charts arrive in v0.3. Until then each job has a fresh VM, a token with no permissions, no secrets and no caches.

Each job compares the exit code and the set of finding IDs with the row below. A finding that is not in the row fails the job, and so does a missing one.

## Entries

| ID | Chart | Upstream | Version | Expected | Seen |
|---|---|---|---|---|---|
| HBC-01 | grafana | [grafana/helm-charts#10](https://github.com/grafana/helm-charts/issues/10), fixed by [#62](https://github.com/grafana/helm-charts/pull/62) | 6.9.2 (defect) | exit 1: HB-UV-001, HB-UV-003 | 13 Oct 2026, as expected |
| | | | 6.9.3 (fix) | exit 1: HB-UV-003 | 13 Oct 2026, as expected |
| HBC-02 | seaweedfs | [seaweedfs/seaweedfs#11719](https://github.com/seaweedfs/seaweedfs/issues/11719), fixed by [#11720](https://github.com/seaweedfs/seaweedfs/pull/11720) | 4.48.0 (defect) | exit 1: HB-UV-003 | 28 Oct 2026, as expected |
| | | | 4.50.0 (fix) | exit 0 | 28 Oct 2026, as expected |
| HBC-03 | docker-registry | [twuni/docker-registry.helm#187](https://github.com/twuni/docker-registry.helm/issues/187), no published fix | 3.0.0 (defect) | exit 1: HB-UV-001, HB-UV-003 | 28 Oct 2026, as expected |
| HBC-04 | jenkins | [jenkinsci/helm-charts#1026](https://github.com/jenkinsci/helm-charts/issues/1026), no published fix | 5.10.0 (defect) | exit 1: HB-GR-001 (`gitops-render`) | 28 Oct 2026, as expected |
| HBC-05 | jupyterhub | [jupyterhub/zero-to-jupyterhub-k8s#3996](https://github.com/jupyterhub/zero-to-jupyterhub-k8s/issues/3996), fix [#3997](https://github.com/jupyterhub/zero-to-jupyterhub-k8s/pull/3997) not released | 4.4.2 (defect) | exit 1: HB-UV-003 | 28 Oct 2026, as expected |
| HBC-06 | semaphore | [semaphoreui/charts#34](https://github.com/semaphoreui/charts/issues/34), fix [#35](https://github.com/semaphoreui/charts/pull/35) not released | 16.2.2 (defect) | exit 1: HB-UV-003 | 28 Oct 2026, as expected |
| HBC-07 | docker-registry-ui | [Joxit/helm-charts#22](https://github.com/Joxit/helm-charts/pull/22) (open fix, not released) | 1.1.3 → 1.1.4 (defect) | exit 1: HB-CU-001 (`chart-upgrade`) | 28 Oct 2026, as expected |
| HBC-08 | yugabyte | [yugabyte/charts#240](https://github.com/yugabyte/charts/issues/240), no fix | 2026.1.1 → 2026.1.2 (defect) | exit 1: HB-CU-001 (`chart-upgrade`) | 28 Oct 2026, as expected |

"Seen" is from `helmbulance test <archive> --scenario <scenario> [-f testdata/corpus/<entry>/values.yaml]` on kind, with `--from <older archive> --from-version <version>` for `chart-upgrade`. HBC-01 ran with Helm v4.0.4; HBC-02 to HBC-08 with Helm v4.3.0 and kind v0.33.0 (Kubernetes 1.37), all on arm64.

### HBC-01: grafana regenerates the admin password

- **6.9.2** writes `admin-password` with `randAlphaNum` on every render, so every upgrade changes it: HB-UV-001 on `admin-password`. The Deployment hashes the Secret template into a `checksum/secret` annotation, so the pods are replaced as well: HB-UV-003.
- **6.9.3** reads the existing Secret with `lookup` and keeps the password: HB-UV-001 is gone. HB-UV-003 stays: the pods of `Deployment/grafana` are still replaced by an upgrade that changes no values.
  - It is the restart on the first upgrade described in [first-upgrade-restart](case-studies/first-upgrade-restart.md): a second upgrade that changes nothing replaces no pods on 6.9.3, and replaces them again on 6.9.2.
  - Likely cause, read from the template: `checksum/secret` hashes a fresh render of `secret.yaml`. At install `lookup` finds nothing and the render holds a new random password; at the first upgrade it finds the Secret. The hash changes once.
  - So the fixed version is not a clean PASS. The corpus expects HB-UV-003 on both versions and HB-UV-001 on 6.9.2 only.

### HBC-02: seaweedfs restarts the filer and S3 gateway on the first upgrade

- **4.48.0**, with `s3.enabled` and `s3.enableAuth`: the filer and S3 pods carry a `checksum/s3config` annotation that hashes a fresh render of the S3 config Secret. At install it holds new random keys; at the first upgrade `lookup` finds the stored Secret. The hash changes once, so an upgrade that changes no values replaces the pods: HB-UV-003. The Secret itself keeps its keys, so there is no HB-UV-001.
- **4.50.0** hashes the config through `seaweedfs.s3.configChecksum`, which the fix (#11720, found and sent with Helmbulance) added: exit 0.
- **4.49.0 is skipped.** It is the first release with the fix, but its default image `chrislusf/seaweedfs:4.49` was never pushed to Docker Hub (4.48, 4.50 and 4.51 exist), so it cannot install. Helmbulance reported it as INCONCLUSIVE ("the chart did not install"), which is right for an image that cannot be pulled.
- The run takes about 5 minutes on an arm64 laptop, so the entry has a 10 minute install timeout.

### HBC-03: docker-registry regenerates haSharedSecret

- **3.0.0** writes `haSharedSecret` with `randAlphaNum` on every render, so the control upgrade (no values flags) already changes it: HB-UV-001. The Deployment hashes the Secret template into `checksum/secret`, so its pod is replaced as well: HB-UV-003. Details in [oss-charts-2026-10](case-studies/oss-charts-2026-10.md).
- No published fix: a `lookup` fix ([#56](https://github.com/twuni/docker-registry.helm/pull/56)) was declined in 2022.

### HBC-04: jenkins renders a new admin password without cluster access

- **5.10.0**, default values: `jenkins-admin-password` comes from `lookup` at `templates/_helpers.tpl:92`. With cluster access the stored password is kept; rendered without it, as Argo CD renders, it is new every time, so every sync replaces it: HB-GR-001.
- This is the drift reported in #1026, which was filed against Helm 3.14 and Kustomize. The chart documents `existingSecret` for GitOps, so upstream may see it as documented behaviour (decision 0001, earlier candidate targets).
- No published fix. The install takes most of the 5 minutes, so the entry has a 10 minute timeout.

### HBC-05 and HBC-06: jupyterhub and semaphore restart on the first upgrade

- The same pattern as HBC-02, described in [first-upgrade-restart](case-studies/first-upgrade-restart.md): a `checksum/secret` annotation hashes a second render of a Secret template that keeps generated values with `lookup`, so the hash changes once, at the first upgrade. HB-UV-003 names `Deployment/hub` and `Deployment/proxy` (jupyterhub 4.4.2) and the Semaphore Deployment (16.2.2). The Secrets keep their values, so there is no HB-UV-001.
- jupyterhub runs without the image pre-puller and the user scheduler, and with a ClusterIP proxy Service (`testdata/corpus/hbc-05-jupyterhub/values.yaml`), as in the upstream report.
- Both fixes are open pull requests; when one is released, its version becomes the entry's fixed twin.

### HBC-07 and HBC-08: an immutable selector breaks the upgrade

- **docker-registry-ui 1.1.3 → 1.1.4:** the Deployment selector holds `app.kubernetes.io/version`, so any `appVersion` change is rejected: `spec.selector: Invalid value … field is immutable`, HB-CU-001. `helmbulance scan` lists it first (HB-SCAN-001); see [the sweep](case-studies/sweep-2026-10.md).
- **yugabyte 2026.1.1 → 2026.1.2:** with the default `oldNamingStyle: true`, the master and tserver StatefulSet selectors go from `{app, release}` to `{app}`, and both are rejected as immutable: HB-CU-001. A one-master, one-tserver universe with small requests and no LoadBalancer Services (`testdata/corpus/hbc-08-yugabyte/values.yaml`). See [the case study](case-studies/yugabyte-2026.1.2.md).
- These two entries use the workflow's `from-url`, `from-sha256` and `from-version` fields: the older archive is pinned like the newer one, and `chart-upgrade` upgrades from it.

## What the corpus has shown so far

- **Known defects found:** 8 of 8 (HBC-01 to HBC-08), across three scenarios: `upgrade-without-values` (HB-UV-001, HB-UV-003), `gitops-render` (HB-GR-001) and `chart-upgrade` (HB-CU-001). D8 asks for at least 5 of 6.
- **Findings on a fixed version:** 1 (HB-UV-003 on grafana 6.9.3); none on seaweedfs 4.50.0. It is a second defect that the fix did not touch, not a false finding. For D8 this means "a finding on the fixed version" has to be read per finding ID.
