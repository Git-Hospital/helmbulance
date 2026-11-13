# Helmbulance architecture

Status: v0.1 in progress. This document sets the boundaries the code is built inside; it changes when the code teaches us something. It went through an architecture review and a security review, and the decisions below include their findings.

## 1. What the system does

Given a Helm chart and its values:

1. Bring up a disposable Kubernetes cluster that behaves like a production one where it matters for the test.
2. Install the chart and work out what it deployed.
3. Run day-two scenarios against the live release: lifecycle operations (upgrade, uninstall and reinstall, rotation), faults, and network checks.
4. Decide, with evidence, whether each scenario exposed a defect, and report it in a form people and CI can use.
5. Never harm anything outside the sandbox.

### What it finds, and what it does not

Helmbulance finds problems in how a chart deploys and operates an application:

- secrets that change or vanish on upgrade;
- data that does not survive a reinstall;
- rotations that leave components out of step;
- ports and datastores reachable from where they should not be;
- probes that depend on things outside the process.

It is not an application security scanner. A bug in the application's own code may show up as a symptom, for example a probe that fails under load. Finding the root cause is still a person's job. The report says "something is wrong here", not "the bug is on this line".

## 2. Principles

- **Reproduced or not reported.** A finding exists only if the cluster reproduced it. Rules and optional AI analysis produce hypotheses, which become scenario parameters; they are never findings on their own.
- **Control before treatment.** Every assertion is checked twice: on an untouched release or a neutral operation (the control), and after the action (the treatment). A difference that already shows in the control is reported as such, not blamed on the action.
- **Three verdicts.** `PASS`, `FAIL`, or `INCONCLUSIVE`.
  - A timeout, a failed install or a cluster that never settles is `INCONCLUSIVE`, never a finding. Errors that come from the run rather than the chart (helm's `--wait` timing out, or Helm 4 stopping it early on a Deployment past its progress deadline; a helm call stopped at its own deadline, even when helm handles the SIGTERM and exits with an error of its own; a killed process, an image pull or registry rate limit, a cluster or etcd that does not answer, a chart host that cannot be resolved or does not serve the chart when helm downloads it again, pods that could not be read until the settle window ended) are always `INCONCLUSIVE`. `chart-upgrade`, `reinstall` and `matrix` use one list for them (#49). A finding already proved, such as a changed Secret, stands; the summary names the check that did not finish. A `FAIL` comes from the chart: an API or render rejection (an object over etcd's request limit too), a failing call to the chart's own admission webhook, or a check that failed.
  - Some timeouts are read further. When `chart-upgrade`'s `--wait` times out, the release was applied, so its containers are read: a container of a new pod that crash-loops or cannot start (`CreateContainerConfigError`, `CreateContainerError`, `RunContainerError`, `InvalidImageName`) or restarted more often than the same container did in its workload's pods before the upgrade, or a container of a Ready pod from before that restarted since, makes it a `FAIL` (HB-CU-001) with each container's state and last exit as evidence. A first-start crash both versions have is the app's startup, as in the health check below. Image pulls and pods that are only not ready yet stay `INCONCLUSIVE` (#28).
  - `matrix` reads an install's `--wait` timeout the same way. Every pod is new, so only a container that crash-loops or cannot start counts, and makes it HB-K8S-002 with the timeout first in its evidence and the install's API warnings after it. With neither, the timeout stays `INCONCLUSIVE` (#49). Such a crash is kept only if the chart ran healthy on another version tested (a `PASS`, or API warnings alone): a Secret the sandbox lacks or an app with no database crashes on every version, so with one version tested it is `INCONCLUSIVE` too, and its API warnings stand as HB-K8S-003/004 (#78).
  - `upgrade-without-values` reads a `--wait` timeout through its Secrets. A regenerated password usually shows up as one: the app cannot log in to data set up with the old value and never gets ready. The upgrade was applied, so the Secrets are compared anyway. A key the control upgrade changed is HB-UV-001, and one the values-files upgrade changed is HB-UV-002, with the timeout and the new pods' crash-looping containers first in its evidence. With no changed key the timeout is unexplained and stays `INCONCLUSIVE`; after a control timeout the values-files upgrade does not run (#35). A values-files upgrade that the chart refuses at render time (`fail`, `required`) is a `PASS` that "refuses safely" (D4), which is what HB-UV-002's fix asks for, when it dropped one `--set` key; with more, the refusal says nothing of the others and the result is `INCONCLUSIVE` (`--set a=1,b=2` is two keys, `--set a=1 --set a=2` one). Any other failure of it is `INCONCLUSIVE`. Either way an HB-UV-001 the control proved stands (#49).
  - `matrix` blames a Kubernetes version for a failed install only if the chart installed on another version, or if the error names a missing built-in API (`no matches for kind` in a group Kubernetes serves itself, such as `policy/v1beta1`), which a version removed. A missing kind from any other group needs a CRD, and kind clusters have none, so that failure is INCONCLUSIVE.
  - A scenario with nothing to test (no HTTP Service, no `--from-version`) is `SKIPPED`. It is not a verdict on the chart and counts for nothing: a run in which everything was skipped is `INCONCLUSIVE`.
  - Exit codes: 0 `PASS`, 1 `FAIL`, 2 usage or setup error, 3 `INCONCLUSIVE`.
  - From v0.2, a `FAIL` is re-run on a fresh release, and only a failure that reproduces twice is reported.
- **App-agnostic first.** Scenarios rely on what Kubernetes can observe. Knowledge of a specific application is an optional plug-in.
- **The chart is untrusted code.** A chart can create privileged pods, mount host paths and run any image. Section 4 says how far each mode protects against that.
- **The measurement must not change what it measures.** No sandbox-wide policy, admission rule or traffic may alter the behaviour under test. Every oracle asks: did I produce this signal myself?

## 3. Components

```
                ┌────────────────────────── helmbulance CLI (Go) ───────────────────────────┐
chart + values ─▶ Chart model ─▶ Sandbox ─▶ Scenario engine ─▶ Oracles ─▶ Reporter            │──▶ Markdown / JSON / SARIF
                │   (render,      (kind)     (control/act/      (Secrets, PVC sentinels,      │
                │    lookup scan)            observe)            health, reachability)        │
                └────────────────────────────────────────────────────────────────────────────┘
```

### 3.1 CLI and tool drivers

- **Language:** Go, distributed as one binary.
- **Helm and kind** run as commands, not as embedded libraries. Two reasons:
  - Helmbulance tests the Helm version the user actually runs. Helm 4 applies resources server-side and Helm 3 client-side, so results can differ between them.
  - The reproduction command in a report is the command that really ran.
- **kubectl** is used for reads in v0.1. client-go replaces it where watching or ephemeral containers need it. Every read (`get`, `logs`) has `--request-timeout 60s`; watches (`rollout status`, `wait`, `delete --wait`) keep their own `--timeout` (#54).
- **The tool's own clusters only.**
  - Every sandbox gets its own kubeconfig (mode 0600, in `~/.helmbulance/clusters/<name>/`, or under `$HELMBULANCE_STATE_DIR` when it is set), and the SHA-256 of the cluster CA is recorded when the cluster is created.
  - Reusing a cluster (`--cluster`) checks that fingerprint on the cluster the kubeconfig's `current-context` points at, which must be `kind-<name>`: helm and kubectl follow it, so another cluster merged into the file is refused (#47). So is an entry with `insecure-skip-tls-verify`, under which the CA proves nothing. The name must be one the tool makes (`helmbulance-` and 8 hex characters). helm and kubectl then get a copy of the file with that context, its cluster and its user only (`kubeconfig.pinned`, mode 0600, checked again before it replaces the copy a run may be using), so a `use-context` or a merge into the kept file during the run cannot send them elsewhere (#47). A new cluster gets the same copy as soon as kind has written its kubeconfig, and is deleted if that file fails the checks below. The copy is written to a temporary file and renamed into place, so a second run never reads it empty, which would mean `http://localhost:8080`. A cluster entry whose `server` is not `https` is refused, since the CA proves nothing without TLS, and kubectl's `~/.kube/kuberc` is turned off (`KUBERC=off`), since its defaults can set `--server` on the commands Helmbulance runs.
  - Each entry in the copy may hold only what kind writes: the context `cluster` and `user`, the cluster `certificate-authority-data` and `server`, the user `client-certificate-data` and `client-key-data`, all of them. Anything else (`exec`, `auth-provider`, `token`, `as`, `proxy-url`, `tls-server-name`, a namespace, …) is refused, so a hand-edited kept file cannot make helm or kubectl run a command, act as someone else or go through another host (#155).
  - The fingerprint protects against mistakes, not against someone who can write the state directory, so that is checked too (#155). The state directory (`HELMBULANCE_STATE_DIR`, `~/.helmbulance` by default) is resolved to its real path once, and the files are named by it from then on, since helm and kubectl open the kubeconfig by path on every call. That path and every directory above it, up to `/`, must belong to the user running Helmbulance or root and be writable only by its owner, or sticky like `/tmp`, as OpenSSH's `StrictModes` checks. `clusters/` and `clusters/<name>/` must be real directories of that user with mode 0700. A new cluster is checked before kind writes its kubeconfig, since `MkdirAll` leaves an existing directory as it is. Systems without Unix owners and modes skip the check. It reads modes, not ACLs: an ACL on the state directory that lets others write (macOS `chmod +a`) is not seen. A file system without Unix modes (`/mnt/c` on WSL, exFAT, SMB) or a bind mount owned by another uid is refused.
  - There is no flag to run scenarios against any other cluster. Lifecycle and network scenarios uninstall releases and open connections; they must never meet a real cluster.
- **`--set` values** are masked in every transcript and report.
- **What reports keep out:** the user info and query values of every URL (a chart URL with credentials, a `--traffic-path` with a token), `$HOME` (shown as `~`) and a state dir outside it. When helm writes an `Error:` line, only the last one goes into a summary or evidence; evidence is escaped like other cluster text.

### 3.2 Chart model

The chart is read on the host before any sandbox exists:

- dependencies are never fetched or updated: helm uses the vendored `charts/`, and Helmbulance never passes `--dependency-update`;
- the Helm version in use is reported, and the project's CI runs `govulncheck`;
- not built yet, planned with the protections for third-party charts (§4, v0.3): refusing symlinks and paths outside the chart, and capping the size and file count of chart archives. Today helm and Helmbulance's own readers follow symlinks (#54).

There is no separate chart model in v0.1. The scenarios read what the chart created from the cluster after the install, and from the release's manifest:

| Area | What is recorded |
|---|---|
| Workloads | Containers, whether a readiness probe exists, update strategy, volumes, and env vars read from Secrets; Service ports are read separately |
| Secrets | Which workload reads which key |
| Network | Services (NetworkPolicies come with `network-isolation`, v0.1.x) |
| Storage | PVCs and StatefulSet volume templates |
| Cluster-scoped resources | Read during setup and cleanup to remove objects a sandbox release kept. Scenarios run one at a time, so they do not collide |
| `lookup` | Found statically in every template, subcharts included, then confirmed after install by comparing a client-side render with a server-side dry run |

**Where `lookup` matters.** Argo CD renders charts with `helm template`, where `lookup` returns nothing. Flux's helm-controller runs real Helm installs, where `lookup` works. A `lookup` finding names Argo CD and does not claim to apply to "GitOps" in general.

### 3.3 Sandbox

**v0.1:**
- kind, with its own default node image unless `--node-image` is given. CI tests with kind v0.33.0, and `matrix` pins node images built for v0.33, so `matrix` needs it. There is no kind version check (#55). kind's default CNI, kindnet, enforces NetworkPolicy through kube-network-policies.
- kind's default `standard` StorageClass (local-path, reclaim policy Delete). The reinstall summary says when a volume went with the release because of it.
- The Pod Security level the release's pods reach (restricted, baseline or privileged) is reported as information, with the API server's lines for what keeps them from the next stricter level. It is read at the end of each scenario, while the release exists, with a server-side dry run of the `enforce` label (`kubectl label --dry-run=server`), so the namespace is never labelled and no warning reaches helm's stderr, which `matrix` reads. It is no finding and changes no verdict. Enforcing a level would stop many charts from installing and change what is measured; `warn` and `audit` labels are not set, and kind writes no audit log by default (#115).
- No ingress controller and no metrics-server in v0.1, because no v0.1 scenario needs them. When an HTTP entry point is needed, it will be a Gateway API implementation (ingress-nginx was retired in March 2026).
- kind's defaults for the API server address (`127.0.0.1`); Helmbulance adds no port mappings.

**Not built yet** (#54, #55):
- A canary that proves NetworkPolicy enforcement when the sandbox starts (a deny-all policy and a test pod), so network scenarios report `INCONCLUSIVE` when it fails. It comes with `network-isolation` (v0.1.x).
- Pod Security admission in `warn` and `audit` mode, with the reached level reported as information (#115). Enforcing it would stop many charts from installing and would change what is measured.
- Memory and CPU limits on the node containers.

**Speed and image pulls:**
- Scenarios run one after another in one cluster; there is no parallel mode. A full application with a database and a search engine, installed three or four times at once, does not fit in a laptop's Docker memory, and running out of memory is the largest source of flaky results.
- Images pulled for the first install stay on the node, so later installs start faster.
- `--keep-cluster` and `--cluster` reuse a sandbox across runs.
- A registry mirror can come later. One pull-through mirror serves only one upstream registry, so it is not worth it in v0.1.
- An image that does not exist for the host's architecture cannot be pulled, so the install ends `INCONCLUSIVE`, not as a chart finding. Naming the architecture in the summary is not built yet.

### 3.4 Scenario engine

Each scenario goes through these phases:

| Phase | What it does |
|---|---|
| `Applies(model)` | Decides whether the chart has what the scenario tests |
| `Setup` | Clears what an earlier run left under the same names, and the cluster-scoped objects any scenario's release kept; then installs a fresh release in its own namespace, under its own release name |
| `Control` | Records the baseline, or runs the neutral form of the operation |
| `Act` | Performs the operation |
| `Observe` + `Assert` | Waits until the cluster settles (no change for N seconds, with a timeout), then compares against the control |
| `Cleanup` | Uninstalls, deletes the namespace, and deletes the cluster-scoped objects the release kept with `helm.sh/resource-policy: keep`, which outlive both: the next scenario installs under another release name, and Helm refuses an object that names the old one (#52). Namespaced leftovers (PVCs, Secrets, Jobs, hooks) go with the namespace; a check that reports them is not built yet |

A run's deadline, Ctrl-C or a SIGTERM to helmbulance (`kill`, `timeout`, `docker stop`) stops every tool call with the children that stay in its process group: the group gets SIGTERM, so helm can mark the release failed, and is killed 30 s later. `Cleanup` still runs after that, with its own 10 minute limit. Scenarios, and `matrix` versions, that have not started by then are not started: each is `INCONCLUSIVE`, "not run", so the sandbox cluster goes without waiting for them. A second Ctrl-C or SIGTERM quits at once and leaves the cluster as it is. An interrupted run never exits 0: a scenario cut short while its pods settle is `INCONCLUSIVE`, a finding proved before the interrupt still exits 1, and otherwise the exit code is 3.

#### Scenario definitions

**`upgrade-without-values`** (implemented). Helm reuses the previous values only when an upgrade gets no values flags at all, so a plain "upgrade without values" changes nothing and cannot find the bug. The scenario has two steps:
- **Control:** `helm upgrade` with no values flags. Helm reuses the install values, so nothing should change. Secrets that change here are regenerated on every render.
- **Treatment:** `helm upgrade --reset-values -f <files>`. This leaves out every install-time `--set`, which is what happens when someone upgrades months later from the values file in git. Keys that change or disappear here, and did not already change in the control, are values the chart does not keep once set.
- **Restarts (HB-UV-003):** a workload that rolls out a new pod template revision (`pod-template-hash`, `controller-revision-hash`) in the control, or that was deleted and created again (a new owner UID). Pods that finish, are scaled away or belong to a Job are not restarts. A second no-op upgrade then says whether pods restart on the first upgrade after install or on every upgrade. The finding stands even if the treatment cannot run.

**`chart-upgrade`** (implemented).
- Install the earlier version (`--from-version`), check that the release runs it, upgrade it to itself as the control, then upgrade to the chart under test with the same values. The report names both versions helm installed.
- helm ignores `--version` for a chart on disk, so a local chart needs `--from` with the earlier chart, and a local `--from` must be the version `--from-version` names. Both are checked before anything runs (exit 2).
- Report a failed upgrade with Helm's error, Secrets the new version changes or drops, and workloads that are not healthy afterwards: pods not ready or restarted until the release has stayed clean for 30 s, and Jobs that failed since the upgrade, given up or still retrying. Restarts are compared per pod: a pod that survived the upgrade with its count from before it, a new pod with its count when first seen (read as soon as helm's `--wait` returns) and no lower than its workload's pods had before the upgrade, since a first-start crash both versions have is the app's startup. A finished Job pod is left to the Job's status; a Job pod that is Pending or Running is judged by readiness and restarts like any other pod.

**`rollback`** (implemented, #130). Does `helm rollback` bring the earlier version back working after an upgrade to the chart under test?
- Runs only with `--from-version`, like `chart-upgrade`: left out of a run without it, exit 2 when named without it. It uses its own release and namespace (`hb-rollback`).
- Install the earlier version and check that the release runs it. Control: upgrade it to itself and roll back to the install. A control rollback that fails makes the run `INCONCLUSIVE` (D4), and Secret keys that change there change on every rollback of the earlier version, so they are named and not compared. Treatment: upgrade to the chart under test, then roll back to the control's revision. Every rollback names its revision from `helm history`, never `0`, so the run is the same on both Helm majors and the reproduce lines say exactly what ran.
- `HB-RB-001`: the rollback fails because of the chart (a `pre-rollback` hook, a field conflict), or helm returns success but the newest revision is not `deployed` at the earlier version. `HB-RB-002`: the rollback succeeds, but after the release has stayed clean for 30 s the earlier version is not healthy (judged like `chart-upgrade`), or a Secret value differs from before the upgrade (rated like `chart-upgrade`'s). A rollback whose `--wait` times out while a container of the earlier version crash-loops or cannot start is `HB-RB-002` with the containers as evidence (#28); pods owned by the release's hook Jobs are checked separately: a crashing `pre-rollback` or `post-rollback` hook is HB-RB-001, including when it is still retrying at the rollback timeout, whether it restarts in place or fails in a new pod each time (`restartPolicy: Never`). Hooks of other kinds, Jobs that completed after a failed pod, and pods that Succeeded (a sidecar stopped with 143) are not crashes. A Job with a `helm.sh/hook` annotation is a hook even when the newest revision does not list it, such as a pre-upgrade hook only the chart under test has, so its pods are never taken for the earlier version's containers. If the hook Jobs cannot be read, crashes are not used to claim HB-RB-002; a timeout with nothing crashing, and errors from the run, are `INCONCLUSIVE`.
- An upgrade to the chart under test that fails is `chart-upgrade`'s finding (HB-CU-001), so here it is `INCONCLUSIVE`. What Helm's automatic rollback (`--rollback-on-failure` in Helm 4, `--atomic` in Helm 3) does after it is `HB-RB-003`, which follows in a second pull request.
- Helm does not render on rollback, it re-applies the stored manifest of the target revision, so there is no render-time refusal to tell apart. CRDs in `crds/` are not rolled back by Helm and are out of scope.

**`zero-downtime`** (implemented).
- A pod in the release namespace sends one request every 200 ms to the most HTTP-like Service port (`--traffic-target`, `--traffic-path` to choose). A port or Service named for metrics, monitoring or Prometheus comes last, since it often answers only `/metrics` (#162). ExternalName, headless and `externalIPs` Services are never chosen, and neither is a Service with an EndpointSlice address that is not the IP of a pod in the release namespace (a chart can add its own slice to a Service, or an Endpoints object that is mirrored into one, and kube-proxy sends traffic to its addresses too, whatever they are; the slice's labels prove nothing, since the chart can give its own slice the control plane's managed-by label, and a hostNetwork pod's IP is the node's, so it does not count). The chosen Service's slices are read again right after the operation and at the end of the traffic tail; if it gained such an address, the run is `INCONCLUSIVE` and names it, since the traffic may have left the release's pods. This detects, it does not prevent: a slice that comes and goes between two reads is not seen, and keeping the traffic on the pods (the probe sending to pod IPs it checks itself, as the security model in §4 describes) comes with the v0.3 egress cut. `--traffic-target` is used as given and not read again.
- Control: 20 s with no change, which must be clean; a control with failures runs once more before the run is inconclusive. The traffic pod logs why each request failed, and an inconclusive summary names the most common reason (an HTTP status, a refused connection, a timeout) with a hint that fits it (#162). Treatment: the same traffic during the version upgrade (with `--from-version`), or during a rolling restart of every Deployment, StatefulSet and DaemonSet. The restart takes one workload at a time, waits for its rollout and then for the traffic tail (`rollout status` returns while the last old pod is still stopping), and the second each one starts, on the traffic pod's clock, splits the log into windows. A release with none of the three, or none that the target Service selects, is `SKIPPED`, since nothing behind the Service would be replaced. If a later rollout fails, the windows before it are complete measurements and their failures still count. Only failures from the start of the operation on count, measured on the traffic pod's own clock; a window with no requests is inconclusive.
- Reports failed requests, the longest outage, and the settings that explain it: Recreate, a single replica, no readiness probe, no preStop. In a restart, those settings are listed for the workloads whose windows had failures; with more than one workload, each such window's count and outage is listed too, and a workload that does not serve the target Service is named as one the served pods may depend on. A PASS names how many workloads and pods were replaced, and how many of them are behind the Service. A DaemonSet with maxSurge 0 on the one-node sandbox always leaves its Service without a pod while it is replaced, unless other workloads' pods serve the same Service; failures only in such windows make the run `INCONCLUSIVE`, not a finding, since a cluster with more nodes keeps serving. When other windows fail too, the finding's counts and longest outage leave those windows out, and the summary says how many failures they had.
- Ports that speak TLS or gRPC are never chosen, since the requests are plain `http://`: an `appProtocol` other than `http`, a name with the word `https`, `tls`, `grpc` or `h2c`, or port 443 or 8443 (#57).
- A StatefulSet or DaemonSet with `updateStrategy: OnDelete` is left out of the rolling restart and named in the summary: a restart replaces none of its pods, and `kubectl rollout status` refuses it. When every workload is OnDelete the run is `SKIPPED` (#57).
- The traffic must go on after the operation: the last request must come at least half the tail after the operation ended, or the run is `INCONCLUSIVE`. When the upgrade itself fails, the result is `INCONCLUSIVE` and still says how many requests failed until then and the longest outage (#57).
- This is a measurement, not a load test: about five requests a second, inside the sandbox, to the release's own Service.
- The traffic pod runs `busybox` pinned by digest, as a non-root user with no ServiceAccount token, no capabilities and a read-only root filesystem (Pod Security `restricted`). It still lives in the release namespace, so the chart's own mutating webhooks can change it.

**`helmbulance scan`** (implemented). A static pre-screen: no cluster, seconds. It reports candidates, never findings; each names the `helmbulance test` run that confirms or rejects it. A chart, `--from` path or `-f` file that does not exist exits 2, in `test` and `matrix` too, before any cluster (#68). Otherwise it exits 0 unless `--strict`: then 1 for a candidate, else 3 when a check could not run, as in §2 (#50). An interrupted scan exits 3 with or without `--strict`, or 1 with `--strict` when a candidate was found first (#49).
- **Immutable fields:** render `--from-version` as an install and the chart as an upgrade (`--is-upgrade`), compare selectors, StatefulSet `serviceName`/`volumeClaimTemplates`/`podManagementPolicy`, Job templates, PVC access modes and storage class, a (Cluster)RoleBinding's `roleRef` and a Service's `clusterIP`. An API default written out in one version and left out in the other (`podManagementPolicy: OrderedReady`, `clusterIP: ""`) is not a change (#56). Hooks and Jobs named after `.Release.Revision` are skipped (a real upgrade gives them a new name).
- **First-upgrade restart:** a pod-template checksum of an included template that keeps generated values with `lookup` (helpers it includes are followed one level). The checksum may be written `include (print $.Template.BasePath "/x")` or `include (printf "%s/x" $.Template.BasePath)`, in the template or in a helper it includes; files whose name starts with `_` are not pod templates (#56).
- **Regenerated Secrets:** render twice as an install and once as an upgrade (`--is-upgrade`); keys that differ between the two installs, in a Secret whose template, or a helper it includes at any depth, does not call `lookup`. Helpers are read from the whole chart, subcharts included, as directories or vendored `charts/*.tgz`, and from the archive when the chart is a `.tgz`; a helper's body runs to its own `end`, and only a `lookup` call counts, not the word in a comment or in `nslookup` (#51). The rule is per Secret, not per key: a Secret that calls `lookup` is left out as a whole, and the summary names it, since only a cluster shows which keys `lookup` keeps (`upgrade-without-values` compares them one by one). Deciding per key without a cluster would need helm's `lookup` fed with an earlier render, which the CLI cannot do. Where the candidate goes depends on when Helm renders the Secret. One that Helm renders on upgrade, including a hook with `pre-upgrade` or `post-upgrade`, is HB-SCAN-003, confirmed by `upgrade-without-values`. One that only an install renders, under `.Release.IsInstall` (absent from the upgrade render) or in an install-only hook, keeps its first value under Helm but gets a new one on every Argo CD sync: HB-SCAN-006, confirmed by `gitops-render` (#86). Hooks that run neither at install nor at upgrade (`test`, delete, rollback) are left out. A chart that refuses to render as an upgrade, as Bitnami charts do without the current passwords, still gets the check: hooks are sorted by their annotation, every other Secret is HB-SCAN-003, and the summary says the upgrade render failed.
- **Unquoted values (HB-SCAN-004):** each default that is a quoted whole number (not 0) or `"true"` is set, one at a time and up to 60, to the number or boolean a user writes without quotes (`--set-json`), and the render's fields are compared, not their values; lists of named entries are matched by name. A field under another name, such as an env `valueFrom: 5432` where the string rendered `value: "5432"`, is a candidate: the API server rejects it, or a setting silently stops applying. `"0"` and `"false"` are left out, since templates test them for truth. A render that fails (a values.schema.json type, say) is the right answer and not reported. No scenario confirms it yet (`matrix` calls an install that fails on every version INCONCLUSIVE); the candidate names the `helm install` with the value `--set` unquoted that does. It flags seaweedfs's `extraEnvironmentVars` (`WEED_MASTER_VOLUME_GROWTH_COPY_1: "7"` and four more) and not the same chart with seaweedfs#12137.
- **Duplicate env (HB-SCAN-005):** render with the values given to the scan and list every container (init and ephemeral ones included, in any pod spec) whose `env` names a variable more than once. A chart that sets a variable itself and also renders one from values names it twice when values set it: Helm 4 applies server-side and rejects the install (`duplicate entries for key`), Helm 3 deploys with a warning that one definition may be dropped on apply. `matrix` sees only the Helm 3 warning (HB-K8S-004); on Helm 4 the install fails on every version, which it calls INCONCLUSIVE. The candidate names the `helm install` with the same values. It flags seaweedfs's filer with the store credentials set the way the wiki's Helm Chart Recipes show (`WEED_MYSQL_USERNAME` and `WEED_MYSQL_PASSWORD` twice), and not the same chart with seaweedfs#12139.
- Checked against the cases confirmed on a cluster: yugabyte, JupyterHub, SeaweedFS and docker-registry are flagged; zammad's revision-named Job and this project's OpenArchiver chart are not.

**`helmbulance matrix`** (implemented). A command rather than a scenario, because each Kubernetes version needs its own cluster.
- For each version (kind node images pinned by digest: 1.34, 1.35, 1.36, 1.37): create a cluster, install, wait for the pods, collect the API server's warnings from helm's stderr, delete the cluster.
- Reports a failed install, pods that never become ready, pods that restart while they are watched (the release must stay clean for 30 s; restarts before helm's `--wait` returned are not counted, apps often restart once at first start), Jobs that gave up or are retrying after a failed attempt, and the API server's warnings: a deprecated or removed API ("`<group/version> <Kind>` is deprecated in …", or "… is deprecated" with no Kubernetes version, the default warning for a CRD version marked deprecated) is HB-K8S-003, any other warning (a duplicated environment variable, a deprecated field or annotation) is HB-K8S-004 at low severity (#56). Warnings are read in client-go's klog format and as `Warning: …` lines. A CRD can replace that warning with its own `deprecationWarning` text, which no pattern can tell from other warnings, so such a version is HB-K8S-004.
- A version the chart's `kubeVersion` excludes is SKIPPED: helm refuses it before anything is created, which is the chart's own statement, not a defect. A `--k8s` version with no pinned node image exits 2 before any cluster (#56).
- Known deprecations are also caught statically (`helm lint`, pluto); what the matrix adds is whether the chart actually runs on each version.

**`reinstall`** (implemented).
- Write a random sentinel file into each bound PVC from a one-off pod that mounts it, and read it back once (the control). Unbound claims are skipped: mounting one would bind it.
- Uninstall, install again with the same values (`--wait` covers the release's health), and check the sentinels and the release's own Secrets. A claim with the same name but a new UID was deleted with the release and made again: it is named, not a finding.
- The release's Secrets still in the namespace right after the uninstall and marked `helm.sh/resource-policy: keep` are its survivors. Helm leaves every hook too, but a hook without `keep` is deleted by the next install's `before-hook-creation` and made again, the same as a Secret the uninstall deleted: the chart never asked to keep it. If the Secrets cannot be listed at that point and a key changed, it is unknown whether HB-RI-004 applies, so the result is `INCONCLUSIVE` unless another finding stands.
- A first install whose `--wait` runs out with the release applied (a timeout, or Helm 4 stopping at a resource that reports Failed, but not a failed `pre-install` or `pre-upgrade` hook, which stops before the manifest is applied) does not end the run: a chart that needs something outside the cluster, such as an external database, never becomes ready in the sandbox, and its kept credentials can still be replaced. HB-RI-002 and HB-RI-004 are still reported, with the timeout as evidence. HB-RI-001 is not: a release that never ran wrote nothing with the old credentials, so its changes are named in the summary, or reported as HB-RI-004 when the Secret was kept. Without a finding the result is `INCONCLUSIVE`, since the release never ran, and that includes a second install that refuses at render time; the summary keeps what the run saw.
- Findings:
  - HB-RI-001: a volume kept its data, and a Secret key that a workload mounting it reads changed (severity as in D5). Other changed keys are named in the summary. After a clean reinstall the release is watched until it settles: a pod that is not ready or restarts goes in the evidence. When nothing fails, the title and evidence say no failure was observed in the sandbox, but the D5 severity stays: a readiness probe often does not log in (PostgreSQL uses `pg_isready`), so healthy pods do not prove the new credential works. An app that sets its password again at every start (Redis or Valkey with `requirepass`) may still work (#53).
  - HB-RI-002: a kept volume lost its sentinel.
  - HB-RI-004: a survivor's key changed on the reinstall (severity as in D5, so at least medium). The chart keeps it so that whatever was set up with it, such as an external database or credentials edited in, works again; a pre-install hook is deleted and created again on every install, so it needs `lookup` too. Keys HB-RI-001 already reports and reissued certificates are left out.
  - HB-RI-003: the second install fails. After a ready first install, a render-time refusal (`fail`, `required`) is a PASS that "refuses safely" (D4); an error from the run (a timeout, a killed helm, a pull error, as in §2) is `INCONCLUSIVE`, but a failing call to the chart's own webhook stays a finding.
- PVCs deleted with the release are named in the summary, with a note that the result depends on the StorageClass reclaim policy. New credentials with no data left are a fresh install, not a finding.
- The sandbox has one node, so the sentinel pod can mount a ReadWriteOnce claim next to the app. On several nodes it would need the app's node.

**`gitops-render`** (implemented).
- Install the chart and read the release's own Secrets, hooks included. Render the same values three times: once with `helm template --dry-run=server --is-upgrade`, where `lookup` reads the cluster and `.Release.IsInstall` is false, as on `helm upgrade`, and twice with `helm template` and no cluster, as Argo CD renders. Argo CD never passes `--is-upgrade`, so its render is always an install.
- helm template prints every hook, but `helm upgrade` does not run one that is only for install (`helm.sh/hook` with `pre-install` or `post-install` and no upgrade event), so the server-side render leaves those Secrets out: Helm keeps what they made, while Argo CD maps them to PreSync and runs them on every sync (#45).
- A Secret key is a finding when the client-side render changes it while Helm keeps it: the server-side render gives the release's value, or leaves the Secret out, as a chart that renders it only `if not (lookup …)` does. A key the server-side render changes too is regenerated under Helm as well; `upgrade-without-values` reports that one. The PASS summary counts only the keys it compared and found equal, and names those it left to `upgrade-without-values`.
- Findings:
  - HB-GR-001: the keys a sync would overwrite, with their readers and the D5 severity. Keys whose two client-side renders differ are named as changing on every sync; for a local chart, the template lines that call `lookup` are listed. A Secret made by an install-only hook is named as one, and its fix leaves out ignoreDifferences: Argo CD applies hooks on every sync, outside the diff.
- Values are compared as keyed hashes, like every Secret comparison here. The server-side render prints Secrets in clear, so it is not a reproduce line.
- Flux's helm-controller installs through the Helm SDK, where `lookup` works, so this is about tools that render with `helm template`.

**`network-isolation`** (v0.1.x, after v0.1 on 8 November, as [0001](decisions/0001-v0.1-path.md) plans).
- **Treatment:** the release with its NetworkPolicies. **Control:** the same release with the chart's NetworkPolicy objects removed for a moment. No values key is needed.
- **Port discovery:** read `/proc/net/tcp{,6}` from an ephemeral container in each pod's network namespace. `containerPort` is informational only, and the ports that matter are often not declared.
- **Output:** an access matrix (source namespace × destination port). A finding is raised only for listeners that no Service exposes and for ports of bundled datastores reachable from foreign namespaces. A reachable Service port on its own is not a defect.

**`secret-rotation`** (v0.2).
- Change one value that flows into a Secret, then upgrade.
- Every pod that reads that Secret must get a new UID, and no credential errors may follow.
- Restarting is not sufficient. Some databases keep the password they were created with, so after a restart the app is locked out. The health and credential oracles must also pass.

**`immutable-fields`** (v0.2).
- Render the old and new values.
- Find changes to immutable fields, such as StatefulSet volume templates, `serviceName` and selectors.
- Confirm each one with a single upgrade.

**`process-kill`** (v0.2).
- An ephemeral container joins the target container's process namespace, runs as the target's user, and kills a process that is not PID 1.
- PID 1 ignores signals it has no handler for, even from root inside its own namespace. Killing PID 1 would also only test the container restart, not the liveness probe.
- The pod is replaced afterwards, because ephemeral containers cannot be removed.

**`probe-abuse`** (v0.3).
- Applies load or simulates an outage of a dependency.
- Checks whether probes fail for reasons outside the process.
- The load generator has a CPU limit, and the same load is applied to an endpoint the probes do not use (the control). Only a probe that fails where the control does not is a finding. Otherwise a laptop's CPU starvation would look like a chart bug.

### 3.5 Oracles and their limits

| Oracle | How | False positives | False negatives | Guard |
|---|---|---|---|---|
| Secret drift (implemented) | HMAC-SHA256 of every key of the Secrets the release has rendered in any revision of the run (`helm get manifest` and `helm get hooks`), so a "generate once" Secret kept with `resource-policy: keep` stays compared, with a random per-run key that is never stored, read once two reads in a row agree. Changed and removed keys are findings, rated by key class (D5): high for a credential that a workload with a PersistentVolumeClaim reads, low for TLS material, medium otherwise. HB-UV-002's Impact and Fix follow the class too: when only keys that are neither credentials nor TLS changed, they say the values went back to the chart's defaults, and do not advise `lookup`, which would make such a value impossible to change (#53). Added keys, values moved to another key, keys listed with `--expected-rotation` and Secrets the release never rendered are named in the summary, with one exception: an added credential key that a workload with a PersistentVolumeClaim reads after the upgrade, but did not read before it, is a finding (HB-UV-002, HB-CU-002) when that workload also stopped reading a key, or a whole Secret, that it read before. That workload moved to a new value while its data was set up with the old one, as when a dropped `--set existingSecret` makes the chart generate a password (#54). A workload the upgrade added, or one that reads the new key next to the old one, moved nothing, so the key stays a note. A move whose old key a workload still reads is a removed key. When no key was compared, the summary says Secret drift was not tested. | Rotation scenarios, where change is expected; a Secret the chart renders and the app then rewrites once, later than the settle window | Generated values kept in ConfigMaps; keys removed from `stringData`, which stay in the Secret; Secrets the release does not render, such as passwords a hook Job writes with `kubectl create secret` | Only the release's own Secrets, after a settle window (done); expected-change set per scenario; ConfigMaps scanned too (v0.2) |
| Data survival (implemented) | Random sentinel file written into each bound PVC by a one-off pod (root with only `CHOWN`, `DAC_OVERRIDE`, `FOWNER`, owner set to the volume's, removed after the last read), read back once before the uninstall as a control; claims tracked by UID | A cache volume the app empties on purpose at start (HB-RI-002) | App re-initialised its data even though the file survived; data on `emptyDir`; unbound claims (skipped); a credential read only by a workload without a kept volume while the data it encrypts lives on a kept volume (an Airflow fernet key, a Superset `SECRET_KEY`) — named later, not yet a finding | Combined with the credential oracle (HB-RI-001 only for keys a workload with a kept volume reads); a reissued certificate a keeper reads is named, not reported; a `--wait` timeout still collects the Secret and sentinel evidence, so a broken reinstall is a finding, not `INCONCLUSIVE`; `INCONCLUSIVE` if a sentinel cannot be written, or the workload mapping fails, while a volume was kept and Secrets changed; report `emptyDir` data paths (later) |
| Health | Ready conditions, restart counts and termination reasons, compared with the control | Start-up crash loops that also happen in the control | A dead process stays "Ready" when there is no liveness probe | Compare deltas, not absolute counts; settle window |
| Credential errors | Log patterns per known component, within a time window | Errors caused by Helmbulance's own probes | Apps that do not log auth failures; logs lost on restart | Evidence only, never a verdict on its own; probe pod IPs excluded; `--previous` logs read |
| Rollout coverage | Pods that read a changed Secret got new UIDs | Apps that hot-reload mounted Secrets | A restart that does not fix the mismatch (database keeps the old password) | Always combined with health and credentials |
| Reachability | Probe pod connects from the release namespace and from a foreign namespace | Policy enforcement is asynchronous right after install | Undeclared ports | CNI canary, bounded retries, port discovery |

Every finding ID has a fixture pair, a small chart with the defect and a fixed twin (`testdata/charts/`), or is listed under `uncovered` in `testdata/verdicts.yaml` with the reason; `go test ./tools/verdicts` fails otherwise ([verdict-quality.md](verdict-quality.md)). An oracle change is only accepted when the bad fixture still fails and the good one still passes. The fixtures already caught one bug in the tool itself: a fixture that wrote its key through `stringData` passed when it should have failed, because Kubernetes keeps a key that is removed from `stringData`.

### 3.6 Reporter

- **Formats:**
  - Markdown for people.
  - JSON with a `schemaVersion` and the overall `verdict`, the worst result of the run (FAIL, then INCONCLUSIVE, then PASS). `test` and `matrix` exit by it, except an interrupted run without a FAIL exits 3 even if its completed results are PASS; `scan` only with `--strict`, since without it a FAIL is a candidate and the exit code is 0. Lists are `[]` when empty, never `null`.
  - SARIF 2.1.0 (`report.sarif`), written with the other two (#99). Uploading it to code scanning comes in v0.2.
- **SARIF details:** each finding points at the template line or values key it comes from, because code scanning needs a location. Fingerprints are stable (scenario, resource and key). Findings do not carry a line or key yet, so for now a result points at the chart's `Chart.yaml` when the chart is a directory under the working directory, and has no location otherwise. Its fingerprint hashes the scenario, the finding ID, the chart and which finding with that ID in the scenario it is; the resource and key join it with the locations. Private repositories need GitHub Code Security to show SARIF, so the Markdown report is also offered as a job summary and pull request comment.
- **Exit codes:** `0` clean, `1` findings, `2` tool error, `3` incomplete (for example, the chart did not install).
- **Redaction:**
  - Secret `data` and `stringData`, and Helm release Secrets in full.
  - URL user info and query values.
  - Every `--set` value of four or more characters, wherever it appears in captured output.
  - Logs and events are not collected yet, so there is nothing more to redact.
  - In a reproduce line, each `--set` value is a shell variable, such as `auth.password="${HB_SET_AUTH_PASSWORD:?…}"`, one per key (`a.b` and `a_b` get `HB_SET_A_B` and `HB_SET_A_B_2`): the line runs once the variable holds the value, and the shell stops with that message when it does not.
- **Escaping:** text that comes from the cluster (names, events, logs) is escaped and length-limited before it reaches Markdown, a terminal or a pull request comment.
- **Failure bundle (planned, not built):** when a scenario fails or is inconclusive, `kind export logs`, events, `describe` output and the Helm history saved next to the report. Until then, keep the cluster with `--keep-cluster`, run the Reproduce commands on it, then read the logs and events (#55).

### 3.7 Optional AI hypotheses (v0.3)

A model may read the chart and propose hypotheses, for example "the readiness probe calls the database, so a slow query makes it fail". Each hypothesis maps to a scenario with parameters, and is reported only if that scenario reproduces it.

The AI step is off by default and never runs commands. Values are redacted before anything is sent to a provider, and the user is asked first.

## 4. Security model

| Mode | Who wrote the chart | What protects the machine |
|---|---|---|
| Local, own chart (default for a local directory) | The user | The same as `ct install`: the user trusts their own chart. kind nodes are privileged containers, so the container runtime is **not** a security boundary. |
| Local, third-party chart (v0.3: automatic for OCI, URL and repository charts, `--untrusted` otherwise; until then `--allow-remote` runs them unprotected) | Someone else | Pod Security `baseline` enforced through an `AdmissionConfiguration` file on the node, which the chart cannot change through the API. The chart is installed through an impersonated identity that cannot edit admission webhooks or policies, namespace labels, RBAC bindings or Helmbulance's own namespace. Host access (privileged, `hostPath`, `hostNetwork`, `hostPID`) is refused. Running inside a VM with no host mounts is recommended, and required for charts that need host access. |
| CI (GitHub Actions) | The pull request author | The runner VM is disposable, but its tokens and the Actions cache are not. The workflow runs on `pull_request`, on pushes to `main` and every night on `main` (`schedule`, the verdict table three times), never on `pull_request_target`; the nightly run has the same read-only token and no secrets. The test job gets `contents: read` only, no secrets, no `id-token`, `persist-credentials: false`, and saves no caches. SARIF upload runs in a separate job that only reads the artefact. The Action warns on self-hosted runners that are not ephemeral. |

**Network scenarios that send load or unusual traffic (v0.3)** run only when egress is cut **outside** the cluster:
- the Docker network of the sandbox, or the host firewall;
- CoreDNS resolves only `cluster.local`;
- the Docker gateway, `host.docker.internal`, link-local and LAN ranges are dropped.

NetworkPolicy cannot provide this, because the chart can add its own policies and `hostNetwork` pods ignore them. Where the cut cannot be set up, these scenarios refuse to run.

Targets are checked when the connection is made, not when a name is resolved. A target must be the IP of a running pod in the release namespace. ExternalName and selector-less Services, `externalIPs` and load balancer addresses are refused, so a chart cannot point Helmbulance's traffic at a third party. Rate and duration caps are compiled into the probe binary; flags can only lower them.

**Supply chain of the tool.** Today: `matrix`'s node images and the busybox image are pinned by digest, and the Action downloads kind, kubectl and helm with fixed versions and checks their sha256. It builds Helmbulance from source with `go build`, and users are told to pin it by commit SHA. On a `v*` tag, the release workflow builds Linux and macOS archives (amd64 and arm64), writes `SHA256SUMS`, attests each archive's build provenance, and attaches them to a draft GitHub release for a person to publish. Planned for v0.2, not built (#55):
- signing release binaries with cosign (keyless) and producing an SBOM;
- images under `ghcr.io/git-hospital/`, and a probe image whose digest is embedded in the binary;
- the Action verifying the binary signature before it runs it.

## 5. No hosted infrastructure

Helmbulance runs where the user runs it: on their machine or in their CI. There is no Helmbulance server, no account and no collected data.

The project itself needs only free GitHub services:
- the repository;
- Releases for binaries;
- GitHub Container Registry for the probe image;
- the Action on Marketplace;
- optionally, Pages for documentation.

A hosted service (submit a chart, get a report) is a possible later product, not part of this plan. If it ever exists, every job runs in its own microVM, charts are fetched outside the VM, and results coming out of it are treated as untrusted.

## 6. Relation to existing tools

| Tool | What it does | How Helmbulance relates |
|---|---|---|
| Trivy, Checkov, kube-linter, Kubescape, Polaris | Static checks of rendered manifests | Complementary: they read YAML, Helmbulance observes behaviour. Their results may be included in the report later. |
| chart-testing (`ct`) | Lint, install, `helm test`; with `--upgrade`, an upgrade from the previous version | `ct install --upgrade` checks that the upgrade succeeds. Helmbulance checks what it does to the release: Secrets kept, values left out, pods restarted for nothing, traffic during it, and the uninstall, reinstall and GitOps render that `ct` does not run. Helmbulance does not run `helm test` hooks. |
| helm-unittest | Assertions on rendered templates | No cluster. Helmbulance's fixtures could use it for render-level checks. |
| Terratest, kuttl, Chainsaw, Testkube | Frameworks to write your own end-to-end tests | They need a test written for each chart. Helmbulance ships ready scenarios with control and treatment built in. Exporting scenarios in kuttl or Chainsaw format is a possible bridge. |
| Chaos Mesh, Litmus | Fault injection in long-running clusters | Platforms installed in a cluster. Helmbulance is a short-lived test run focused on chart lifecycle. |
| helm-diff | Shows what an upgrade changes | Shows the diff. Helmbulance shows whether the change breaks something. |

## 7. Repository layout

```
cmd/helmbulance/        CLI entry point
internal/execx/         command runner with masked transcripts
internal/sandbox/       kind lifecycle and ownership checks
internal/helm/          helm CLI driver
internal/kube/          cluster reads
internal/oracle/        oracles
internal/scenario/      scenarios
internal/scan/          the static pre-screen (helmbulance scan)
internal/matrix/        one cluster per Kubernetes version (helmbulance matrix)
internal/report/        Markdown, JSON and SARIF
tools/verdicts/         runs testdata/verdicts.yaml against the fixtures
testdata/charts/        fixture charts: one defect and one fixed twin each
testdata/corpus/        values for the known-bug corpus
docs/                   architecture, the Action, the corpus, decisions, case studies
```

## 8. Milestones

| Version | Scope |
|---|---|
| v0.1 | CLI, kind sandbox, `upgrade-without-values`, `chart-upgrade`, `zero-downtime`, `reinstall`, `gitops-render`, `scan`, `matrix`, Markdown, JSON and SARIF, fixtures, an experimental composite GitHub Action that runs the CLI |
| v0.1.x | `network-isolation` (TCP, CNI canary, port discovery) |
| v0.2 | `rollback` (HB-RB-001 and HB-RB-002 on `main`, automatic rollback HB-RB-003 next), `secret-rotation`, `immutable-fields`, `process-kill`, SARIF upload to code scanning, double-run confirmation of failures, signed releases with SBOM |
| v0.3 | `probe-abuse` with egress isolation outside the cluster, third-party chart mode, application plug-ins, optional AI hypotheses |
| Ongoing | Running against popular community charts and sending fixes upstream (README, "Found in public charts") |
| Later | A hosted service, only if there is demand |
