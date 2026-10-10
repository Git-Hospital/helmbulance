# Helmbulance: market, competitive landscape and real-world pain for behavioural (day-2) Helm chart testing

All figures read 10-11 Oct 2026 via `gh api` / Artifact Hub API unless a date says otherwise. Read-only research; nothing was posted.

## (a) Who already does any part of this (competitors and adjacent tools)

### Takeaway
No tool found markets itself as day-2 / behavioural / upgrade-semantics testing of Helm charts against a live release. The closest, `helm/chart-testing` (`ct install --upgrade`), only runs `helm test` after an in-place upgrade from the previous chart revision using the previous revision's CI values files. It does not test upgrade-without-values, reinstall, network isolation, rotation, kill, or GitOps rendering. Everything else is either static linting/policy scanning, a generic test harness you must script yourself, or chaos tooling.

### Cited Findings
Stars/dates from GitHub API (`gh api repos/OWNER/REPO`), 10 Oct 2026.

Closest chart testers
- helm/chart-testing (`ct`): 1,645 stars, last push 2026-10-09, latest v3.15.0 (2026-10-06), Apache-2.0, official Helm org. — [repo](https://github.com/helm/chart-testing)
- `ct install --upgrade` validates `helm test` passes for "previous chart revision => current chart version (if non-breaking SemVer change)" and "current chart version => current chart version"; flag `--skip-missing-values` handles CI values deleted between revisions. So the upgrade path reuses CI values files, it does not probe "upgrade without values", and it is skipped for SemVer-breaking bumps. — [ct_install.md](https://github.com/helm/chart-testing/blob/main/doc/ct_install.md)
- helm-unittest: 1,401 stars, latest v1.2.1 (2026-10-03), MIT; renders templates and asserts on manifests, no cluster (covers gitops-render-style static checks only, not behaviour). — [repo](https://github.com/helm-unittest/helm-unittest)
- Terratest: 7,971 stars, last push 2026-10-06, Apache-2.0 (Gruntwork); has helm and k8s Go modules; a library you code scenarios in yourself. — [repo](https://github.com/gruntwork-io/terratest)
- kuttl: 826 stars, v0.27.0 (2026-09-23); declarative YAML test steps against a cluster; generic, no Helm-specific scenarios. — [repo](https://github.com/kudobuilder/kuttl)
- Kyverno Chainsaw: 623 stars, latest release v0.2.15 on 2026-05-06, last push 2026-09-03 (Kyverno itself 8,236 stars); declarative cluster e2e; generic. — [chainsaw](https://github.com/kyverno/chainsaw), [kyverno](https://github.com/kyverno/kyverno)

Network / chaos
- netassert (controlplaneio): 391 stars, v2.1.8 (2026-10-07); asserts network reachability between pods from a YAML spec; covers the "is it reachable" half of network-isolation but you must write the spec and deploy the chart yourself. — [repo](https://github.com/controlplaneio/netassert)
- cyclonus (NetworkPolicy conformance prober): 125 stars, last push and last release v0.5.6 both 2024-07-11 (effectively unmaintained for 2+ years); it tests CNI NetworkPolicy implementations, not charts. — [repo](https://github.com/mattfenwick/cyclonus)
- Chaos Mesh: 7,930 stars, v2.8.4 (2026-08-18); LitmusChaos: 5,725 stars, 3.32.0 (2026-09-17). Both are generic fault injection (pod kill etc.), not chart-aware, no pass/fail contract for a chart. — [chaos-mesh](https://github.com/chaos-mesh/chaos-mesh), [litmus](https://github.com/litmuschaos/litmus)

Static scanners / policy (no live release behaviour)
- Kubescape 11,790 stars, v4.0.15 (2026-09-29); Trivy 38,337 stars, v0.75.0 (2026-10-01); Checkov 9,060 stars, 3.3.26 (2026-10-07); Polaris 3,393 stars, v10.2.5 (2026-09-18); kube-linter 3,524 stars, v0.8.3 (2026-03-10); kubeconform 3,218 stars, v0.8.0 (2026-06-04). All lint rendered manifests against rules. — [kubescape](https://github.com/kubescape/kubescape), [trivy](https://github.com/aquasecurity/trivy), [checkov](https://github.com/bridgecrewio/checkov), [polaris](https://github.com/FairwindsOps/polaris), [kube-linter](https://github.com/stackrox/kube-linter), [kubeconform](https://github.com/yannh/kubeconform)
- Datree: repo ARCHIVED, last push 2024-04-23, last release 1.9.19 (2023-07-23), 6,329 stars. — [repo](https://github.com/datreeio/datree)
- Artifact Hub (2,108 stars, v1.23.0 on 2026-07-07): security report uses Trivy per package version; discovers images from a dry-run install with default values or the `artifacthub.io/images` annotation; latest version scanned daily, old ones weekly; skips `latest`-tagged images. Image CVEs only, no behaviour. — [repo](https://github.com/artifacthub/hub), [security report docs](https://artifacthub.io/docs/topics/security_report)
- helm/helm: 30,308 stars, v4.3.0 (2026-09-09). — [repo](https://github.com/helm/helm)

Scenario coverage matrix (my reading of the docs above; "partial" = could be scripted by the user)
- upgrade-without-values: none; ct only reuses CI values. immutable-fields: ct's own test suite has a deliberately failing selector-change case ([mirror of ct docs](https://docsearch.algolia.com/mcp/docs/repo/helm/chart-testing)), only when versions differ and values files exist. gitops-render: helm-unittest/kubeconform render without a cluster but do not compare against a cluster render. network-isolation: netassert/cyclonus partial. process-kill: Chaos Mesh/Litmus partial. secret-rotation, reinstall, probe-abuse: none found.

Commercial / startups
- Search for "Helm chart upgrade testing", "day-2", "behavioural testing" products found nothing that matches; only ct, helm-test hooks and an MCP "operate" tool for running upgrades (not testing). — [web search result: ct docs mirror](https://docsearch.algolia.com/mcp/docs/repo/helm/chart-testing), [dot-ai Helm day-2 changelog](https://glama.ai/mcp/servers/@vfarcic/dot-ai/blob/da9254ad6bd2a4385f15005926d7f220b67ef8a8/changelog.d/251.feature.md)
- One ISV-side upgrade-test example: GitLab's agent chart MR "Add tests for a real-world upgrade scenario to look for breaking changes" shows maintainers hand-rolling upgrade tests in CI. — [MR](https://gitlab.com/gitlab-org/charts/gitlab-agent/-/merge_requests/76)
- Snyk, Wiz and other commercial scanners: I did not verify any product page; treated as static/CVE/misconfig scanners (assumption, not sourced).

### Inferences
- The whitespace is real but narrow: the competitor set is "static + generic harness", and nobody ships opinionated, pre-written day-2 scenarios with a reproduce-before-report rule.
- Datree's archival and cyclonus's stagnation show that policy/test tools in this niche have a poor monetisation/maintenance record; plan for an OSS-first, maintainer-effort story, not a revenue story.
- ct is the tool to integrate with or position beside (same org, CI-native, 1.6k stars), not to attack.

### Gaps
- Snyk/Wiz/Sysdig/Prisma chart-check product pages not read; kube-linter/Polaris specific Helm checks not compared; CNCF status of each tool not verified (Helm graduated per CNCF; Kyverno/Chainsaw level not checked).
- No evidence either way on whether an unlisted startup exists; one negative web search pass only.

## (b) Size of the problem and audience

### Takeaway
Artifact Hub lists 18,197 Helm charts across about 6,225 Helm repositories (21,683 packages of all kinds), and Helm is the most preferred Kubernetes package manager (75%, CNCF/LF 2024 survey). I could not obtain hard counts for charts with bundled DBs, `lookup`, `randAlphaNum` or StatefulSets.

### Cited Findings
- Artifact Hub stats API (2026-10-10): 21,683 packages total, 503,182 snapshots, 6,997 repositories, 3,711 organizations, 44,012 users. — [stats API](https://artifacthub.io/api/v1/stats)
- Helm charts (kind=0) in search: 18,197 packages; 6,225 Helm repositories; 652 "official"; 117 CNCF-project packages; 8,420 from verified publishers (from `pagination-total-count` headers). — [API](https://artifacthub.io/api/v1/packages/search?kind=0&limit=1)
- CNCF/Linux Foundation survey of 689 respondents (published Mar 2025, 2024 data): Helm preferred by 75%. — [LF report](https://www.linuxfoundation.org/hubfs/Research%20Reports/cncf_annual_survey24_031225a.pdf), [Cloud Native Now summary](https://cloudnativenow.com/topics/cloudnativedevelopment/cncf-survey-surfaces-steady-pace-of-increased-cloud-native-technology-adoption/)
- A DEV Community post claims the Jan 2026 CNCF survey ranks Helm second-most-used CNCF project after Kubernetes (no percentage; secondary source, unverified). — [post](https://scour.ing/p/https:%2F%2Fdev.to%2Fmetalbear%2Fa-beginners-guide-to-helm-5fj0)
- Bitnami charts repo: 10,415 stars; bitnami/containers 4,468 stars (still pushed 2026-10-10). — [charts](https://github.com/bitnami/charts), [containers](https://github.com/bitnami/containers)
- `randAlphaNum` is a recognised, documented source of perpetual OutOfSync in Argo CD. — [Argo CD docs](https://argo-cd.readthedocs.io/en/stable/user-guide/helm/)
- Argo CD does not allow cluster access while rendering, so `lookup` returns empty; the long-open request "Helm lookup Function Support" has 112 comments and 254 reactions (open since 2021-01-07); a 2025 proposal exists (argo-cd #21745, open). — [#5202](https://github.com/argoproj/argo-cd/issues/5202), [#21745](https://github.com/argoproj/argo-cd/issues/21745)
- Helm itself has an open ask to warn when `lookup` returns nil (#32101, closed). — [helm #32101](https://github.com/helm/helm/issues/32101)

### Inferences
- A typical large "scan popular charts" sweep is feasible: ~18k charts, but the ~650 official + 8.4k verified-publisher subset is a sensible first target list.
- Shares of bundled DBs / lookup / randAlphaNum / StatefulSet could be measured by Helmbulance itself (download chart tarballs via the Artifact Hub API and grep); this would make a publishable stat and a marketing hook.

### Gaps
- No sourced count for charts with DB subcharts, `lookup`, `randAlphaNum`, StatefulSets. A rough keyword search on Artifact Hub returned a number I did not trust, so omitted.
- Helm download counts, 2025/2026 CNCF Helm percentage, publisher counts specific to Helm (only all-kind org/user totals) not found.
- Replicated State of Self-Hosted 2025 results not found (only the intro page), so no ISV survey numbers.

## (c) Evidence that these failure classes hurt real users

### Takeaway
Concrete, linked issues exist for every scenario class, many with long threads. Ages vary; some are 2020-2023 and are marked. Verified by reading titles/bodies via `gh api`.

### Cited Findings

Upgrade without values / regenerated or lost passwords (incl. Bitnami "PASSWORDS ERROR")
1. bitnami/mongodb: "mongodb password get changed on every ugrade" (2025-08-01); `helm upgrade -f values.yaml` without `--set` changed the generated password. — [bitnami/charts#35374](https://github.com/bitnami/charts/issues/35374)
2. bitnami/mariadb: "Don't overwrite generated secret values on chart upgrade" (2021-09-04, 22 comments). — [#7403](https://github.com/bitnami/charts/issues/7403)
3. bitnami/minio: generated admin password changes each time, "in loop", deployed through Argo CD (2023-11-23). — [#21223](https://github.com/bitnami/charts/issues/21223)
4. bitnami/argo-cd: "Admin password changed unexpectly after each upgrade" (2022-06-30). — [#10974](https://github.com/bitnami/charts/issues/10974)
5. bitnami/postgresql 12.2.1: upgrade fails with "PASSWORDS ERROR" (the required-current-passwords guard), 22 comments (2023-02-21). — [#15073](https://github.com/bitnami/charts/issues/15073)
6. bitnami/mysql 9.5.0: upgrade breaks with execution error at `templates/secrets.yaml:3:21` (2023-02-28). — [#15175](https://github.com/bitnami/charts/issues/15175)
7. Bitnami search "PASSWORDS ERROR" + "current passwords" in bitnami/charts returned 63 issues (GitHub search count, 2026-10-10). — [search](https://github.com/search?q=repo%3Abitnami%2Fcharts+%22PASSWORDS+ERROR%22+%22current+passwords%22&type=issues)

Reinstall (uninstall + install: data vs credentials)
8. bitnami/keycloak: after `helm uninstall` the PVC survives but the postgres secret is deleted; reinstall generates a new password the existing data dir does not accept, Keycloak cannot start (2023-06-09). — [#17079](https://github.com/bitnami/charts/issues/17079)
9. bitnami/rabbitmq: after delete + reinstall pods never get past "Waiting for Mnesia tables" (2020-09-25, 35 comments; old). — [#3783](https://github.com/bitnami/charts/issues/3783)

GitOps / `lookup` / `randAlphaNum` under Argo CD or Kustomize
10. goharbor/harbor-helm: "ArgoCD: drift: randAlphaNum" - core-secret defaults to random strings, constant re-sync notifications (2025-10-25, open). — [harbor-helm#2263](https://github.com/goharbor/harbor-helm/issues/2263)
11. DataDog/helm-charts: cluster-agent token regenerated on every Argo CD refresh; asks to use `lookup` (2022-04-21, open, 12 reactions). — [#604](https://github.com/DataDog/helm-charts/issues/604)
12. DataDog/helm-charts: the token differs per template invocation within a single install since `randAlphaNum` is called multiple times (2024-12-11, open). — [#1632](https://github.com/DataDog/helm-charts/issues/1632)
13. jenkinsci/helm-charts: initial admin password secret overwritten on each Kustomize build, login breaks in GitOps (2024-02-24, open). — [#1026](https://github.com/jenkinsci/helm-charts/issues/1026)
14. argoproj/argo-cd: "Helm lookup Function Support", 112 comments, 254 reactions (2021, open). — [#5202](https://github.com/argoproj/argo-cd/issues/5202)

Immutable fields (StatefulSet / selectors / clusterIP)
15. prometheus-pushgateway: upgrade fails "failed to replace object: StatefulSet ... is invalid" because label changes hit the immutable selector/volumeClaimTemplates when `persistentVolume.enabled` (2025-02-06). — [prometheus-community/helm-charts#5279](https://github.com/prometheus-community/helm-charts/issues/5279)
16. grafana loki-stack 2.6.2 to 2.6.4: "change to immutable field" in promtail DaemonSet `spec.selector` (2022-04-16). — [grafana/helm-charts#1256](https://github.com/grafana/helm-charts/issues/1256)
17. elastic/helm-charts: `helm upgrade` fails with 'cannot patch "elasticsearch-master" with kind StatefulSet' (2020-02-21, old). — [elastic/helm-charts#492](https://github.com/elastic/helm-charts/issues/492)
18. bitnami/postgresql: upgrade fails on immutable `clusterIP` (2020-08-18, old); same root cause in helm/helm "upgrade nginx spec.clusterIP: field is immutable", 72 comments. — [bitnami#3447](https://github.com/bitnami/charts/issues/3447), [helm/helm#6378](https://github.com/helm/helm/issues/6378)
19. helm/charts stable/mariadb: stateful sets break with Helm v3 (2019). — [helm/charts#19231](https://github.com/helm/charts/issues/19231)

Secret / config rotation not restarting dependents
20. kube-prometheus-stack: extra Grafana datasources update the ConfigMap but do not redeploy Grafana (2022-09-02, follow-up 2023-02-13). — [#2424](https://github.com/prometheus-community/helm-charts/issues/2424), [#3029](https://github.com/prometheus-community/helm-charts/issues/3029)
21. prometheus-mysql-exporter: restart-on-password-change only works for chart-created secrets, not `existingConfigSecret` (2024-01-22). — [#4165](https://github.com/prometheus-community/helm-charts/issues/4165)
22. bitnami/mongodb-sharded: after changing the admin password "sporadic issues" with pods (2022-01-17). — [bitnami/charts#8683](https://github.com/bitnami/charts/issues/8683)

NetworkPolicy
23. kube-prometheus-stack: Alertmanager NetworkPolicy only allows Prometheus traffic and omits config-reloader web port, so "alertmanager cluster down" fires (2025-08-28). This is the over-restrictive direction; I did not find a clean example of a policy that failed to isolate in the issue search. — [#6090](https://github.com/prometheus-community/helm-charts/issues/6090)
24. grafana loki: "Loki network policy doesn't make sense" (2021-03-26, open). — [grafana/helm-charts#343](https://github.com/grafana/helm-charts/issues/343)

Probes depending on dependencies / cascading restarts
25. nextcloud/helm: liveness on `/status.php`, which depends on DB, Redis and the volume, "restarts healthy pods during dependency outage"; all three probes hardcoded (2026-10-06, open, very fresh). — [nextcloud/helm#890](https://github.com/nextcloud/helm/issues/890)
26. bitnami/postgresql-ha: pgpool pods fail liveness and die when postgresql pods restart; reproduced on minikube with `rollout restart` (2022-06-17). — [#10801](https://github.com/bitnami/charts/issues/10801)

Searches that returned nothing usable: sentry-kubernetes/charts, mastodon/chart (query returned empty), gitlab (GitHub search cannot index gitlab.com), cert-manager (jetstack query rejected by GitHub), ingress-nginx immutable (only #5884 "error when applying patch: field is immutable", 2020). — [ingress-nginx#5884](https://github.com/kubernetes/ingress-nginx/issues/5884)

### Inferences
- Every Helmbulance scenario maps to at least two real issues; the highest-signal (recent, open) targets for a first "contribution" demo are harbor-helm#2263, DataDog#604/#1632, jenkins#1026, nextcloud#890, pushgateway#5279.
- Bitnami's pattern is the central teaching case: either the chart regenerates (#35374, #21223) or it hard-fails with PASSWORDS ERROR (#15073). Both are detectable by one upgrade-without-values scenario.
- The pain predates Helmbulance by 5+ years and recurs with each chart rewrite, so it is structural, not a one-off.

### Gaps
- No example found for: NetworkPolicy that silently failed to isolate; password rotation not restarting for Bitnami specifically beyond #8683; Sentry, Mastodon, GitLab, cert-manager specific cases. Correction: kind v0.24.0 and later enforces NetworkPolicy out of the box through kube-network-policies (see technical-design.md); an enforcement canary is still needed.

## (d) Ecosystem events that open a window

### Takeaway
Broadcom's Bitnami change (announced 2025-07-16, effective 2025-08-28, public catalog deletion postponed to 2025-09-29) pushed thousands of installs onto legacy/forked/rewritten charts, and Helm 4 (2025-11-12) added server-side apply and warns that upgrade behaviour may change. Both increase demand for upgrade-safety verification.

### Cited Findings
- Bitnami announcement: from 2025-08-28 no new Bitnami container images or OCI Helm charts published to Docker Hub; source stays on GitHub under Apache 2.0; Debian-based images moved to `bitnamilegacy`; free hardened images only on `latest` for development; deletion of `docker.io/bitnami` postponed to Sept 29 with 24h brownouts on Aug 28, Sep 2, Sep 17. — [bitnami/charts#35164](https://github.com/bitnami/charts/issues/35164)
- Broadcom community post and secondary coverage describe paid "Bitnami Secure Images" subscription. Price figures ($50K-$72K/yr) come from a Licenseware/DEV post and are unverified. — [Broadcom blog](https://community.broadcom.com/blogs/beltran-rueda-borrego/2025/08/18/how-to-prepare-for-the-bitnami-changes-coming-soon), [Linuxiac](https://linuxiac.com/bitnami-ends-free-stable-images-users-forced-to-migrate-or-pay/), [DEV](https://dev.to/lyndon_brown_/bitnamis-free-catalog-says-goodbye-avoid-brownouts-and-a-72k-surprise-4kp3)
- CNCF statement (2025-09-24): Helm project is unaffected; users should migrate or mirror. — [CNCF blog](https://www.cncf.io/blog/2025/09/24/cncfs-helm-project-remains-fully-open-source-and-unaffected-by-recent-vendor-deprecations/)
- User impact examples: ImagePullBackOff on pinned tags; GitLab's infra team opened remediation issues ([gitlab-com/gl-infra#27125](https://gitlab.com/gitlab-com/gl-infra/production-engineering/-/issues/27125)); MOSIP advisory ([MOSIP](https://community.mosip.io/t/advisory-bitnami-catalog-changes-and-impact-on-mosip-deployments/2311)); nextcloud/helm needs replacement subcharts ([#754](https://github.com/nextcloud/helm/issues/754)).
- Replacements: Chainguard published 40+ first-party charts forked from Bitnami ([Chainguard guide](https://www.chainguard.dev/supply-chain-security-101/a-practical-guide-to-migrating-helm-charts-from-bitnami)); CloudPirates-io/helm-charts repo created 2025-08-12, now 611 stars, pushed 2026-10-10 ([repo](https://github.com/CloudPirates-io/helm-charts)); TrueCharts 1,355 stars ([repo](https://github.com/truecharts/public)); CloudNativePG operator 9,423 stars as DB-subchart replacement ([repo](https://github.com/cloudnative-pg/cloudnative-pg), suggested in nextcloud #754).
- Helm 4.0.0 released 2025-11-12 at KubeCon; adds server-side apply, kstatus-based wait, plugin-based post-renderers; Helm 3 bug fixes until 2026-07-08, security fixes until 2026-11-11. Latest in repo now v4.3.0 (2026-09-09). — [Helm blog](https://helm.sh/blog/helm-4-released), [helm releases](https://github.com/helm/helm/releases)
- v4.0.0 release notes: "Existing charts should continue to install, upgrade... Please test the installation and upgrade of charts... Changes (e.g., server side apply) may impact the experience." — [v4.0.0 release notes](https://github.com/helm/helm/releases/tag/v4.0.0)
- Whether SSA is default is reported inconsistently (one source: default for new installs only; others: default). Not resolved. — [DEV summary](https://dev.to/ilya-lesikov/helm-v4-key-changes-from-v3-2i26), [AlternativeTo](https://alternativeto.net/news/2025/11/helm-4-0-0-released-with-wasm-plugin-system-and-server-side-apply)

### Inferences
- Helm 3 security EOL on 2026-11-11 (a month away) means a forced migration wave; "does my chart still upgrade under Helm 4 SSA" is a natural Helmbulance scenario (immutable-fields + field-ownership conflicts).
- Bitnami's lock-in turned many to forks/rewrites that are young and untested for upgrades (CloudPirates is ~14 months old), a fresh scan target set.

### Gaps
- No evidence found of a CNCF/Kubernetes SIG initiative on chart quality (not found in these searches; not conclusive). Helm community repo exists (497 stars) but nothing chart-quality specific was read.
- Bitnami adoption numbers (pulls/installs) not found.

## (e) Who would use it and who might pay

### Takeaway
Primary evidence supports maintainers and platform teams feeling the pain; I found no survey or talk directly stating that chart maintainers want behavioural upgrade testing, and no evidence of willingness to pay.

### Cited Findings
- Maintainers hand-build upgrade tests (GitLab agent chart MR above) and ask for features that only exist as workarounds (Datadog #604 asks for lookup-based stable tokens; Nextcloud #890 asks for configurable probes). — [GitLab MR](https://gitlab.com/gitlab-org/charts/gitlab-agent/-/merge_requests/76), [DataDog#604](https://github.com/DataDog/helm-charts/issues/604), [nextcloud#890](https://github.com/nextcloud/helm/issues/890)
- Helm's own release guidance asks users to test upgrades before moving to v4 — [release notes](https://github.com/helm/helm/releases/tag/v4.0.0)
- Datree (policy tool for platform teams) was archived, a warning on paid demand in this niche. — [repo](https://github.com/datreeio/datree)

### Inferences
- Likely beneficiaries: (1) community chart maintainers (free, via PRs), (2) ISVs shipping charts to customers (upgrade failure is a support cost), (3) platform teams vetting third-party charts before adoption and Helm 4/Bitnami migrations. Paid potential, if any, probably sits with (2) and (3) as hosted scans/CI gates; unvalidated.
- Contribution-tool strategy has precedent in the reaction counts on the issues above (e.g. 254 reactions on argo-cd#5202), which show audience interest in these problems.

### Gaps
- No KubeCon talks, blog posts or surveys found stating maintainer demand for upgrade/behavioural testing (one search; not exhaustive). No pricing or willingness-to-pay data. Reddit / Hacker News threads were not searched.
