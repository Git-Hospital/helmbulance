# Helmbulance technical design: fact base and options (as of 2026-10-11)

Scope note: ~25 tool calls, partly rate-limited (GitHub search API hit 403). Items I could not source are in each Gaps list, not asserted.

## 1. Implementation language and Helm integration (Go SDK vs CLI, Helm 4, ct, terratest)

### Takeaway
Helm 4 is stable (4.0.0 on 2025-11-12; 4.3.0 on 2026-09-09) with a breaking SDK/CLI, new server-side-apply default for fresh installs, and a rebuilt plugin system. Both reference tools (ct, terratest) shell out to the `helm` binary rather than link the SDK; that is the low-risk path, because Helm 3 and 4 will coexist in the wild for a long time.

### Cited Findings
- Helm 4.0.0 was published 2025-11-12; latest releases are v4.3.0 (2026-09-09) and v3.22.0 (2026-09-10), so both lines are maintained — [helm/helm releases](https://github.com/helm/helm/releases/tag/v4.0.0)
- Helm 4 headline features: WASM-capable plugin system, post-renderers are now plugins, server-side apply, kstatus-based waiting, content-based chart cache, slog logging, reproducible chart archives, SDK supporting multiple chart API versions. Release notes warn of backward-incompatible CLI flags/output/SDK; apiVersion v2 charts still work, "changes (e.g., server side apply) may impact the experience" — [Helm v4.0.0 release notes](https://github.com/helm/helm/releases/tag/v4.0.0), [helm.sh overview](https://helm.sh/docs/overview/)
- SSA: new installs default to server-side apply; upgrades/rollbacks follow the release's previous method, so Helm 3 releases stay client-side unless `--server-side` is passed. `helm upgrade --server-side` accepts true/false/auto (default auto = previous release's method); `--force-conflicts` exists — [helm.sh overview](https://helm.sh/docs/overview/), [helm upgrade reference](https://raw.githubusercontent.com/helm/helm-www/main/docs/helm/helm_upgrade.md)
- Flag renames in v4: `--atomic` -> `--rollback-on-failure` (defaults `--wait` to "watcher"), `--force` -> `--force-replace`; `--no-update`, `--create-pods` removed — [community changelog summary (secondary, by a Helm maintainer's fork)](https://github.com/scottrigby/helm/blob/v4-changelog/helm-v4-changelog-summary.md), [helm upgrade reference](https://raw.githubusercontent.com/helm/helm-www/main/docs/helm/helm_upgrade.md)
- Values semantics on upgrade (helm upgrade docs): `--reuse-values` = last release's values merged with `-f/--set`; `--reset-values` = chart built-in defaults; `--reset-then-reuse-values` = reset to the NEW chart's defaults, apply last release's user values, then merge CLI overrides; ignored if `--reset-values` or `--reuse-values` is given. Default (none of the three) = new chart defaults + only values passed now, i.e. previous `--set` values are dropped, which is the "upgrade-without-values" hazard — [helm upgrade reference](https://raw.githubusercontent.com/helm/helm-www/main/docs/helm/helm_upgrade.md); SDK field `ResetThenReuseValues` exists in v4 `pkg/action/upgrade.go` — [helm/helm source](https://github.com/helm/helm/blob/main/pkg/action/upgrade.go)
- Dry-run: v4 `--dry-run` takes `none|client|server`. Client dry-run avoids cluster contact; `lookup` only queries a live cluster with `--dry-run=server` (or a real install/upgrade). Missing object returns empty; API errors (RBAC) fail template processing. v4 SDK: `DryRunStrategy`, `interactWithServer(strategy)` gates cluster access during rendering — [Helm template functions guide](https://helm.sh/docs/chart_template_guide/functions_and_pipelines/), [upgrade.go](https://github.com/helm/helm/blob/main/pkg/action/upgrade.go)
- `--dry-run` output includes Secrets; `--hide-secret` exists — [helm upgrade reference](https://raw.githubusercontent.com/helm/helm-www/main/docs/helm/helm_upgrade.md)
- Go module: main is `helm.sh/helm/v4` on go 1.26; Helm 4 moves to versioned packages (`pkg/chart/v2`, `pkg/release/v1`) — [go.mod](https://github.com/helm/helm/blob/main/go.mod), [changelog summary](https://github.com/scottrigby/helm/blob/v4-changelog/helm-v4-changelog-summary.md)
- Plugins: types cli, getter, postrenderer; WASM (Extism) runtime optional; existing plugins "work as before" per the official overview, while a secondary summary claims legacy plugins must migrate. Contradiction unresolved — [helm.sh overview](https://helm.sh/docs/overview/) vs [changelog summary](https://github.com/scottrigby/helm/blob/v4-changelog/helm-v4-changelog-summary.md)
- chart-testing (ct): latest v3.15.0 (2026-10-06). Shells out: `tool.Helm` wraps a `ProcessExecutor` and runs `helm repo add`, `helm dependency build`, `helm lint`, `helm install/upgrade` etc. as subprocesses; kubectl likewise. It imports `helm.sh/helm/v3/pkg/ignore` only for .helmignore parsing. Interfaces (`Helm`, `Kubectl`) are mockable. It offers install, upgrade (previous-release -> current) and version-increment checks, and tests changed charts via git diff — [ct pkg/tool/helm.go](https://github.com/helm/chart-testing/blob/main/pkg/tool/helm.go), [ct pkg/chart/chart.go](https://github.com/helm/chart-testing/blob/main/pkg/chart/chart.go), [ct releases](https://github.com/helm/chart-testing/releases)
- ct requires only Helm >= 3.0.0 per its version check (v3-era); whether it supports Helm 4 was not verified — [chart.go](https://github.com/helm/chart-testing/blob/main/pkg/chart/chart.go)
- terratest `modules/helm` is also a CLI wrapper (`RunHelmCommandAndGetOutputContextE(t, ctx, options, "install", args...)`), with files install/upgrade/rollback/template/delete; terratest `modules/core/v2.0.0` released 2026-09-23 — [terratest modules/helm](https://github.com/gruntwork-io/terratest/tree/main/modules/helm), [terratest releases](https://github.com/gruntwork-io/terratest/releases)
- helm/kind-action v1.15.1 (2026-10-06): inputs version (default kind v0.33.0), node_image, wait (default 60s), kubectl_version, config — [kind-action README](https://github.com/helm/kind-action)

### Inferences
- Recommended: Go binary (single static CLI, fits Kubernetes ecosystem, client-go for assertions, reuse of kind's Go library `sigs.k8s.io/kind/pkg/cluster` is possible) that SHELLS OUT to `helm` for install/upgrade/uninstall/template/get values. Reasons: (a) users' Helm 3 vs 4 behaviour differs (SSA, flags) and the test should run with the user's own helm; (b) the SDK major bump (`helm.sh/helm/v4`) makes dual support costly; (c) ct and terratest set the precedent. Use the SDK (v3 or v4) only for offline chart loading/static analysis (`loader.Load`, values schema, render with fake `Capabilities`), where version drift is tolerable.
- Run each scenario matrix against both Helm 3.22 and 4.3 as a feature: "works in Helm 3 but breaks on 4 upgrade" (SSA field-ownership conflicts, `--force` rename) is itself a finding class. This is a hypothesis; I did not find a documented list of SSA conflict pitfalls.
- Reusable from ct: release-name randomisation, changed-chart discovery, `ProcessExecutor` abstraction, previous-version upgrade logic. Reusable from terratest: little beyond ideas (it is a Go test library, needs `*testing.T`).

### Gaps
- Exact Helm 4 behaviour of `--reuse-values` with nil/removed keys (known Helm 3 bug class) not verified.
- Whether `helm.sh/helm/v4` SDK is stable/semver-guaranteed not verified.
- ct Helm 4 compatibility not verified.

## 2. Ephemeral clusters: kind cost, alternatives, parallelism, caching, time budget

### Takeaway
`ubuntu-latest` gives 4 vCPU / 16 GB / 14 GB SSD for public repos but only 2 vCPU / 8 GB for private repos; kind control-plane startup on Actions is tens of seconds (one logged example: 17 s ready, node image pull included). Budget per chart is dominated by image pulls and chart readiness, not by cluster creation; I found no rigorous benchmark.

### Cited Findings
- GitHub-hosted standard `ubuntu-latest` x64: public repos 4 vCPU / 16 GB RAM / 14 GB SSD; private repos 2 vCPU / 8 GB / 14 GB — [GitHub runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
- A setup-kind README log shows a cluster ready after 17 s including pulling kindest/node (a single example, not a benchmark) — [divergent3d/setup-kind](https://github.com/divergent3d/setup-kind)
- kind latest: v0.33.0 (2026-08-26); v0.32.0 2026-06-02; v0.31.0 2025-12-18 — [kind releases](https://github.com/kubernetes-sigs/kind/releases)
- kind known issue: too many open files / inotify limits (Ubuntu defaults 8192 watches / 128 instances) break multi-node or many-cluster setups; docs recommend raising `fs.inotify.max_user_watches=524288`, `max_user_instances=512` — [kind known issues](https://kind.sigs.k8s.io/docs/user/known-issues/)
- kind multi-version: pre-built node images per k8s version; `kind load docker-image` / `image-archive` (multiple archives supported since v0.24, a single archive is "more efficient") — [kind v0.24.0 release](https://github.com/kubernetes-sigs/kind/releases/tag/v0.24.0)
- kind supports local registry and offline use (docs pages local-registry, working-offline) — [kind docs](https://kind.sigs.k8s.io/docs/user/local-registry/)
- vcluster (loft-sh) very active: pushed 2026-10-08, ~11.3k stars, latest v0.38.0-alpha.2; README now describes a Docker deployment mode (control plane and nodes as containers on one Docker host, "fastest way to get a real Tenant Cluster on a laptop or CI runner") and shared-nodes tenant clusters running workloads as pods in a namespace of a host cluster — [vcluster repo](https://github.com/loft-sh/vcluster)
- k3d latest release v5.9.0 (2026-06-02) — [k3d releases](https://github.com/k3d-io/k3d/releases)
- Docker image caching on Actions: no first-party preload for kindest/node; the practical pattern is a separate step with `actions/cache` of a saved image tar then `docker load`/`kind load` (search-derived, unbenchmarked) — [Zenn pattern](https://zenn.dev/temori/articles/413e1a68e97fcc)

### Inferences
- Parallelism options: (A) one kind cluster, namespace per scenario: cheapest (one 15-40 s startup), but scenarios with cluster-scoped objects (CRDs, ClusterRoles, webhooks, PSA labels, node-level kill) collide, and NetworkPolicy/CNI change affects everyone. (B) cluster per chart/scenario: strong isolation, but on a 4 vCPU/16 GB runner maybe 2-3 single-node kinds before memory/inotify pain (estimate, not measured). (C) vcluster per scenario on one host kind: own API server and CRDs per scenario, isolates cluster-scoped objects but adds a layer that can itself change behaviour (syncer, network policy semantics depend on host CNI). Suggested default: one single-node kind per `helmbulance test` run, namespace per scenario for namespaced-only charts, and automatically fall back to a fresh cluster when the chart ships CRDs/cluster-scoped resources. Opinion, needs a prototype.
- Rough time budget guess for planning only (UNMEASURED): cluster 20-40 s, install with preloaded images 10-60 s, each scenario 10-90 s, so 3 scenarios + report ~3-6 min per chart; dominated by image pulls on cold runners. Measure before promising numbers in the README.
- Private-repo users get 2 vCPU/8 GB: scenario concurrency should default to 1 there.

### Gaps
- No sourced measurement of kind create time, resource footprint (RAM per node), or k3d/vcluster-docker startup on ubuntu-latest.
- Disk: 14 GB SSD limit vs large charts' images (e.g. kube-prometheus-stack) not quantified.
- kind-action caching features not verified (README grep showed no cache input).

## 3. NetworkPolicy enforcement on kind

### Takeaway
Since kind v0.24.0 (Aug 2024) the default CNI path enforces NetworkPolicy via kube-network-policies, a userspace NFQUEUE enforcer that only intercepts packets for pods targeted by at least one policy. So "kind does not enforce policies" is outdated; Helmbulance should still run an explicit canary probe to prove enforcement before attributing a failure to the chart.

### Cited Findings
- kind v0.24.0 release: "Out-of-the-box support for network policy via sigs.k8s.io/kube-network-policies"; default node image k8s v1.31.0; advanced users can disable kindnetd and install their own CNI, but kind offers no support for third-party CNI — [kind v0.24.0](https://github.com/kubernetes-sigs/kind/releases/tag/v0.24.0)
- kind docs: `networking.disableDefaultCNI: true`; "power user feature with limited support, but many common CNI manifests are known to work, e.g. Calico"; kube-proxy modes iptables/nftables (1.31+)/ipvs — [kind configuration](https://kind.sigs.k8s.io/docs/user/configuration/)
- kube-network-policies: userspace controller using NFQUEUE, nftables rules redirect packets to the controller which evaluates a policy pipeline (ANP, BANP, standard NetworkPolicy) and returns a verdict; it only captures packets for pods targeted by at least one policy (performance); also supports Admin/Baseline Admin Network Policies via separate manifests — [architecture doc](https://github.com/kubernetes-sigs/kube-network-policies/blob/main/site/content/docs/concepts/architecture.md), [repo README](https://github.com/kubernetes-sigs/kube-network-policies)
- netassert v2 (controlplaneio): YAML test spec (src/dst k8sResource or host, tcp/udp, targetPort, attempts, timeoutSeconds, expected exitCode), implemented with ephemeral containers (tcp "scanner" needs no privileges; udp needs a packet sniffer with raw capability injected into the target pod); repo pushed 2026-10-07 — [netassert README](https://github.com/controlplaneio/netassert)
- cyclonus (mattfenwick): `analyze` (explain policies), `probe` (single test), `generate` (conformance-style test case generator); README says it is for research/experiments and that active network policy verification continues in kubernetes-sigs/network-policy-api; last push 2024-07-11; used by Antrea and Cilium CI — [cyclonus](https://github.com/mattfenwick/cyclonus)
- kube-score checks `pod-networkpolicy` (every Pod targeted by a NetworkPolicy) and `networkpolicy-targets-pod` (a NetworkPolicy targets at least one pod) — [kube-score README_CHECKS](https://github.com/zegl/kube-score/blob/master/README_CHECKS.md)

### Inferences
- "Policy not enforced" vs "policy wrong" proof design (my design): (1) Enforcement canary: in a scratch namespace create a pod + service + deny-all ingress policy, probe from a second pod; if the probe still connects, the CNI does not enforce -> mark network-isolation scenario INCONCLUSIVE ("cluster cannot enforce"), never a chart finding. (2) Only then test the chart: probe from a pod in another namespace to each Service/Pod port of the release, expect denial where chart claims isolation, and expect allowed traffic from legit peers (chart's own pods). Compare against static reading of the chart's NetworkPolicy to separate "no policy rendered", "policy selects no pods" (selector drift, kube-score's second rule), "policy allows 0.0.0.0/0", and "policy blocks required traffic (DNS egress, kube-apiserver)".
- Use ephemeral probe pods (or `kubectl debug`/ephemeral containers like netassert) rather than depend on netassert/cyclonus binaries; netassert's spec format is a good model for expressions. Cyclonus's generate suite is useful to validate the CNI itself, but heavy and stale.
- Time cost for Calico/Cilium on kind: not measured; kube-network-policies default avoids it. Offer `--cni calico|cilium` later for semantic differences (e.g. FQDN/L7 policies, egress to node/host).
- kube-network-policies semantics risks: userspace NFQUEUE, so host-network pods, node-originated traffic (kubelet probes) and hairpin/service-VIP paths may behave differently from Calico/Cilium (UNVERIFIED: I did not find a documented limits page; the "troubleshooting" and "testing" docs exist in the repo's site/content/docs).

### Gaps
- Official list of kube-network-policies limitations and its conformance status not retrieved.
- Calico/Cilium install time on kind not sourced.

## 4. Generic vs chart-specific assertions (static detection + dynamic confirmation)

### Takeaway
A good portion of day-2 hazards can be detected without per-chart config by rendering the chart twice (render diff) plus grepping templates for known-risk functions, then confirmed on the cluster by observing the real upgrade. The Bitnami common library shows the chart-side convention (lookup the existing Secret, else generate, and `PASSWORDS ERROR` if upgrading without current passwords), which Helmbulance can recognize.

### Cited Findings
- Bitnami common library: `common.secrets.passwords.manage` uses `lookup "v1" "Secret"` to reuse an existing Secret's data, otherwise `randAscii`/`randAlphaNum`; `common.errors.upgrade.passwords.empty` fails the render when `.Release.IsUpgrade` and required password values are empty with "PASSWORDS ERROR: You must provide your current passwords when upgrading the release." — [bitnami/common _secrets.tpl](https://github.com/bitnami/charts/blob/main/bitnami/common/templates/_secrets.tpl), [_errors.tpl](https://github.com/bitnami/charts/blob/main/bitnami/common/templates/_errors.tpl)
- Bitnami PostgreSQL README: `passwordUpdateJob.enabled=true` changes DB passwords on upgrade; it reads previous passwords from the deployed Secret, but when using `auth.existingSecret` or `helm template` you must set `passwordUpdateJob.previousPasswords.*` — [bitnami/postgresql README](https://github.com/bitnami/charts/blob/main/bitnami/postgresql/README.md)
- Bitnami is now under Broadcom ("Bitnami Secure Images"); Debian-based previous images live in the `bitnamilegacy` registry — [bitnami/charts README](https://github.com/bitnami/charts); implication: many scanned Bitnami charts reference images that may not pull (see section 7)
- GitOps: Argo CD renders with `helm template` and applies with kubectl, so `lookup` returns empty; Helm itself stopped rendering `lookup` in plain `helm template` (needs `--validate` / `--dry-run=server`); Argo CD issue #3640 and an enhancement for a server dry-run flag are open (status unconfirmed); workaround: chart option to disable lookup or pass value via Application `valuesObject` — [Argo CD #3640](https://github.com/argoproj/argo-cd/issues/3640), [Nautobot chart issue #679](https://github.com/nautobot/helm-charts/issues/679), [Argo CD Helm docs](https://argo-cd.readthedocs.io/en/release-2.12/user-guide/helm/)
- Flux helm-controller: drift detection compares the manifest FROM HELM STORAGE with cluster state via server-side dry-run apply, i.e. it does not re-render templates, so `lookup`-derived values are fixed at install/upgrade time; ignore rules via JSON pointers; correction can reapply — [Flux HelmRelease spec](https://github.com/fluxcd/helm-controller/blob/main/docs/spec/v2/helmreleases.md). That Flux evaluates `lookup` at install/upgrade is consistent with it using the Helm SDK (inference; no doc quote found)
- Checkov: K8s policies include CKV_K8S_8 "Liveness Probe Should be Configured" across Pod/Deployment/DaemonSet/StatefulSet/ReplicaSet — [Checkov policy index](https://www.checkov.io/5.Policy%20Index/kubernetes.html)
- kube-score: `pod-probes` (safe probe configuration) and `pod-probes-identical` (readiness and liveness not identical) — [kube-score checks](https://github.com/zegl/kube-score/blob/master/README_CHECKS.md)
- Artifact Hub research: Minna/Massacci/Tuma analyzed misconfigurations in Artifact Hub charts using Checkov and KICS and LLM-suggested fixes; found LLM fixes can be correct but may delete unrelated config and snippet-only context is insufficient — [arXiv 2403.09537](https://arxiv.org/pdf/2403.09537), [Springer EMSE](https://link.springer.com/article/10.1007/s10664-025-10688-0)

### Inferences (proposed detector table; design opinion)
| Static signal (render + AST of templates) | Dynamic confirmation |
|---|---|
| `randAlphaNum`/`randAscii`/`genCA`/`genSignedCert` inside a `kind: Secret` template, not wrapped by `lookup` | upgrade with same values; diff `data` of each Secret before/after; any change = finding |
| `lookup` present at all | render with `helm template` (client) vs `--dry-run=server`; differing output = "gitops-render" finding for Argo CD; stored-manifest check for Flux |
| Secret mounted/envFrom'd by pod template with no `checksum/` annotation (or no reloader annotation) | mutate Secret value (or change values to rotate), `helm upgrade`, compare pod UIDs/restart counts and whether the app sees the new value |
| StatefulSet `volumeClaimTemplates` / selector / `serviceName` templated from values | upgrade changing that value; expect Helm to fail with "Forbidden: updates to statefulset spec" (immutable-fields scenario) |
| Deployment `selector.matchLabels` containing version/chart labels (e.g. `helm.sh/chart`) | upgrade with chart version bump; expect immutable selector error |
| `required`/`fail` guards | install with only defaults; failing at install is by design, but failing only on `.Release.IsUpgrade` is the Bitnami pattern: repeat upgrade with no values (hazard #1) |
| Probes (liveness path/port not exposed by container, identical probes) | probe-abuse: make readiness fail and check traffic removal; kill liveness endpoint and check restart behaviour; compare declared port vs container listening ports |
| Missing/empty NetworkPolicy while values suggest `networkPolicy.enabled` | enforcement canary + cross-namespace probe (section 3) |
| Jobs/hooks (`helm.sh/hook`) with no delete policy | reinstall twice; "already exists" |
- Mapping to existing tools: kube-score/Polaris/Checkov/Kubescape check STATIC manifest hygiene (probes configured, netpol exists, resources). They do not detect upgrade-time behaviour, so Helmbulance's niche is the diff-across-lifecycle rather than rule overlap. Do not duplicate their rules; optionally ingest them.
- Needed per-chart knowledge (what makes assertions valuable): which Service port is the app's, what "healthy" means, which Secret key is a password, how to connect with a client. Options: (1) auto-discover from rendered objects (Services, Ingress, `helm test` hooks, Secret key names matching `*password*|*token*`), (2) optional `helmbulance.yaml` sidecar per chart (values for probes, expected Secret behaviour, accepted exceptions), (3) community "profiles" per well-known chart family (Bitnami common-lib charts, bjw-s app-template). Opinion: ship (1) with explicit confidence labels and (2) as an escape hatch; profiles only when scanning at scale.

### Gaps
- Polaris and Kubescape rule IDs were not fetched; only kube-score and one Checkov rule verified.
- Whether Polaris/Kubescape can run on Helm-rendered output with chart-level metadata (needed to point to template line numbers) not checked.
- No source found for how common `checksum/` annotation absence is across Artifact Hub charts.

## 5. Scenario definition format and extensibility

### Takeaway
Declarative YAML test frameworks for Kubernetes (kuttl, Chainsaw) are widely used but both are about asserting resource state; Chainsaw superseded kuttl in many projects and ships a kuttl migrator. Helmbulance scenarios are mostly procedural lifecycle flows (install, snapshot, mutate, upgrade, diff), so built-in scenarios should be code, with a declarative format only for user extensions.

### Cited Findings
- Chainsaw (kyverno): latest v0.2.15 (2026-05-06), ~623 stars at fetch time, code pushed 2026-09-03. Declarative YAML tests with steps; operations apply/assert/command/create/delete/error/patch/script/sleep/update; assertion trees with JMESPath; bindings, templating, try/catch/finally, JUnit reports, multi-cluster, `chainsaw migrate kuttl`. Adopters state it replaced kuttl and bash scripts — [Chainsaw README](https://github.com/kyverno/chainsaw), [Chainsaw docs](https://kyverno.github.io/chainsaw/latest/)
- kuttl (kudobuilder): latest v0.27.0 (2026-09-23), repo still pushed 2026-10-05 and not archived — [kuttl repo](https://github.com/kudobuilder/kuttl); the community-reported shift to Chainsaw is in Chainsaw docs testimonials (adopter claims, not neutral) — [Chainsaw docs](https://kyverno.github.io/chainsaw/latest/)
- ct: config in YAML (`ct.yaml`) + CLI flags; test behaviour fixed in code; per-chart `ci/*-values.yaml` files are the extension mechanism (each file = one install) — [ct repo](https://github.com/helm/chart-testing) (ci-values convention is from ct docs; I did not re-fetch the file, treat as well-known)
- netassert test spec is a flat YAML list of connectivity tests — [netassert](https://github.com/controlplaneio/netassert)
- Helm 4 plugin system: cli/getter/postrenderer types + WASM — [helm.sh](https://helm.sh/docs/overview/)

### Inferences
- Recommended layering: (1) built-in scenarios in Go implementing a small interface `Scenario{ID, Applies(chart) bool, Run(ctx, env) []Finding}`; (2) declarative `helmbulance.yaml` per chart for values sets, expected exceptions/suppressions, endpoints and probes; (3) later, an "exec" extension protocol (subprocess speaking JSON on stdin/stdout, like Helm plugins or `kubectl-*`) so users can add scenarios in any language; avoid Go-plugin or WASM complexity in v0.1. Also let a scenario emit a Chainsaw test as repro artifact (see section 6) rather than reinventing assertion syntax; do not depend on Chainsaw as the engine because its model (apply resources, assert) lacks "snapshot, upgrade, diff".
- Testkube was not researched; it is an orchestration platform (cluster-side) and probably not a fit for ephemeral CLI use (unverified).

### Gaps
- Testkube internals and terratest extension patterns (beyond being Go code) not researched.
- ct `ci/` values-file convention not freshly fetched.

## 6. Reporting: Markdown, JSON, SARIF, reproduction, evidence

### Takeaway
SARIF is a poor native fit for runtime findings: GitHub requires a physical file location per result, shows only the first location and in PR checks only when lines are in the diff. It is still usable by anchoring findings to the offending template file/line (e.g. `templates/secret.yaml:12`), but treat it as optional output; Markdown (job summary / PR comment) and JSON are primary.

### Cited Findings
- GitHub SARIF support: SARIF 2.1.0; every result needs `message.text`, `locations[]` (at least one; only the first is used), `partialFingerprints`, and `physicalLocation.artifactLocation.uri` plus region; each rule needs `id`, `shortDescription`, `fullDescription`, `help.text`; empty strings unsupported for required properties; results appear in PR checks only if all lines are in the diff, including the first; limits: 10 MB gzipped per file, 20 runs per file, 25,000 results per run (top 5,000 by severity shown beyond that), 25,000 rules per run; exceeding limits rejects the file — [GitHub SARIF support](https://docs.github.com/en/code-security/code-scanning/integrating-with-code-scanning/sarif-support-for-code-scanning)
- Chainsaw emits JUnit-compatible reports and has `kubectl` helpers describe/events/get-pod-logs for failure context — [Chainsaw docs](https://kyverno.github.io/chainsaw/latest/)
- `helm template`/`--dry-run` output can include Secrets; `--hide-secret` exists — [helm upgrade reference](https://raw.githubusercontent.com/helm/helm-www/main/docs/helm/helm_upgrade.md)

### Inferences
- Finding record (suggested JSON schema): id, scenario, severity, confidence=`reproduced` only, chart@version, helm version, k8s version, CNI, steps (exact commands), expected vs observed, evidence refs (rendered manifest diffs, `kubectl get events`, pod logs with restart counts, `helm get values/manifest` per revision), and a minimal repro script (`repro.sh` using kind + helm only) so maintainers can verify without Helmbulance.
- Redaction is mandatory: Secrets and `helm get values` may contain credentials; hash or mask Secret data in evidence (store only "changed / unchanged" plus a salted hash prefix).
- SARIF mapping: map to the chart template file/line via Helm's template source comments (`# Source: chart/templates/x.yaml`) from `helm template --debug`; for runtime-only findings (no file) fall back to `Chart.yaml` line 1. Use `partialFingerprints` from scenario+chart+template path to dedupe. Expect alerts to look odd on non-PR runs (non-diff lines); SARIF suits scheduled scans of a chart repo, not PR annotations.
- GitHub Action: write the Markdown to `$GITHUB_STEP_SUMMARY`, upload JSON + evidence bundle as artifact (cluster logs are lost with the runner), optional `upload-sarif`.

### Gaps
- Do not know how GitHub handles locationless results precisely (docs say not displayed; reject-vs-drop unspecified).
- GitHub step-summary size limits not checked.

## 7. Scanning third-party charts at scale (Artifact Hub, sandboxing, cost, dependencies, false positives)

### Takeaway
Artifact Hub's public API gives search with stars, official/verified flags, production-organization counts, values-schema presence and security-report summaries, enough to rank charts without scraping. Executing arbitrary charts is the hard part: a chart can run hooks, pull any image and request any privileges, so isolation must be at the VM/cluster level, never on a shared developer machine.

### Cited Findings
- Artifact Hub package search `GET https://artifacthub.io/api/v1/packages/search?kind=0&sort=stars&limit=..&offset=..` returns JSON without auth (verified live 2026-10-10) with fields: stars, official, `production_organizations_count`, `has_values_schema`, signed, `security_report_summary` {low,medium,high,critical,unknown}, deprecated, version, app_version, license, repository {url, cncf, verified_publisher, display_name}. Example top hit: kube-prometheus-stack (1235 stars, version 92.3.0, 30 production orgs) — [Artifact Hub API](https://artifacthub.io/api/v1/packages/search?kind=0&sort=stars&limit=3&offset=0), [OpenAPI spec](https://github.com/artifacthub/hub/blob/master/docs/api/openapi.yaml)
- The OpenAPI spec lists 429 responses on endpoints (rate limiting exists; limits not stated in what I read) — [openapi.yaml](https://github.com/artifacthub/hub/blob/master/docs/api/openapi.yaml)
- Bitnami images moved to Bitnami Secure Images/`bitnamilegacy`; chart readme refers to the legacy registry for Debian-based images — [bitnami/charts](https://github.com/bitnami/charts)
- Prior large-scale Artifact Hub analysis exists (LLM mitigation paper above) and mined charts with Checkov/KICS — [arXiv 2403.09537](https://arxiv.org/pdf/2403.09537)
- vcluster-style stronger isolation exists (own control plane per tenant; docker mode) — [vcluster](https://github.com/loft-sh/vcluster)

### Inferences
- Selection: filter `kind=0` (Helm), not deprecated, rank by `production_organizations_count` and stars, prefer verified_publisher/official, prefer charts with `values.schema.json` (cleaner inputs), cap to charts whose rendered images are public. Persist `version` so findings are tied to a reproducible chart version/digest.
- Sandbox: run each chart in its own throwaway VM or ephemeral runner (GitHub-hosted job per chart is natural: fresh VM, no secrets, `permissions: {}`), kind inside it; deny egress except registries if possible; block hostPath/privileged via Pod Security Admission `restricted`/`baseline` on test namespaces where chart allows, and record when a chart needs more (itself a finding class). Hooks (`pre-install` jobs) execute inside the cluster, so containment is the cluster + VM, not Helm. Cap runtime (timeout per scenario), CPU/memory (ResourceQuota/LimitRange), disk (14 GB runner limit). Never mount the Docker socket into chart pods (kind nodes are privileged containers: container escape on the runner VM is the residual risk, so no credentials on that VM).
- Cost: hosted public-repo Actions minutes are free for public repos; private cost not computed here. At ~5 min/chart, 500 charts = ~42 compute hours (arithmetic on my unmeasured budget).
- External dependencies: classify via render: needs CRDs (install via declared dependency or skip), Ingress controller, cloud LB (`type: LoadBalancer` stays Pending: treat as expected, not failure), StorageClass (kind ships `standard` local-path provisioner), cloud IAM/secret stores, external DB creds. Outcome statuses: PASS / FINDING(reproduced) / SKIPPED(reason) / INCONCLUSIVE(env limitation). Never emit a finding from a chart that failed baseline install for environment reasons (baseline-install gate).
- False-positive controls: run baseline control twice (determinism: Secrets that change on every plain re-render but not on upgrade vs. real regeneration); repeat each reproduced finding on a fresh cluster before reporting; pin helm and k8s versions in the report; check that the issue is not documented/intended (README mention of `existingSecret`/`PASSWORDS ERROR` style).
- Upstream fixes (outward-facing): human review gate before any issue/PR; follow each project's contribution guidelines and AI-disclosure norms (not researched).

### Gaps
- Artifact Hub rate-limit numbers and total count of Helm packages not retrieved.
- No data on what fraction of top charts install cleanly on kind with defaults.
- Security model of kind-in-Actions escape risk not sourced.

## 8. LLM-assisted scenario proposal (prior work, pitfalls, propose-then-confirm loop)

### Takeaway
I found no published work on LLM-generated Kubernetes/Helm behavioural tests or a hallucination benchmark. The nearest peer-reviewed work (Minna et al.) uses LLMs on Artifact Hub charts to suggest fixes and finds they can be correct yet delete unrelated config and are sensitive to context. That supports the stated principle: the LLM proposes, the cluster decides.

### Cited Findings
- Minna, Massacci, Tuma (EMSE 2025; arXiv 2024): LLMs differ significantly in quality; giving only a YAML snippet is insufficient compared with all resources; LLM refactorings can break the app by removing unrelated settings; manual validation on a subset was needed — [Springer](https://link.springer.com/article/10.1007/s10664-025-10688-0), [arXiv 2403.09537](https://arxiv.org/pdf/2403.09537)
- Search for LLM-generated Kubernetes test papers returned nothing directly relevant (two searches) — [search result set](https://arxiv.org/pdf/2403.09537)

### Inferences (design of propose -> confirm loop; opinion)
1. Input to the LLM: rendered manifests + values.yaml + values.schema + templates (whole chart, as the paper warns snippets are insufficient) + a closed vocabulary of scenario primitives and detectors.
2. Output must be structured JSON hypotheses `{hypothesis, scenario_id, params, expected_observation, falsifier}` against the fixed scenario catalog, not free-form shell, so the executor controls what runs (also protects against prompt-injection from chart READMEs/templates, which are untrusted data).
3. Executor runs the hypothesis on a clean cluster; a finding is only created if the machine-checked observation (Secret hash changed, pod restarted, connection succeeded) matches `expected_observation`, reproduced twice. Unconfirmed hypotheses are dropped and counted, not reported.
4. The LLM may then write the human-readable explanation, but only from the evidence bundle; explanation text is not itself evidence. Track hypothesis-to-confirmed ratio as a quality metric and keep a regression corpus of known bugs (e.g. Bitnami upgrade-password behaviour, Argo CD lookup cases) to evaluate prompts.
5. Cost control: static detectors first (free), LLM only for charts where detectors have low coverage (to guess connection endpoints, credentials keys, what "ready" means).

### Gaps
- No sourced benchmark for hallucination rates in K8s/Helm test generation; any rate would be speculation.
- Possible adjacent tools (e.g. k8sgpt, kubectl-ai, HolmesGPT) not researched here.
