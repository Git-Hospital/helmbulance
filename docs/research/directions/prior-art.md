# Helmbulance: prior-art and novelty check (5 directions)

Date: 2026-10-11. Read-only research (web search + `gh` repo stats). Nothing posted anywhere.
Stats from `gh repo view` on 2026-10-11. Limits: web search snippets, not full-text reads, for most papers; the Springer EMSE page was behind a login redirect, so Minna et al. is cited from arXiv/MSR records. "Nothing found" means nothing found with the searches listed, not proof of absence.

Helmbulance itself: https://github.com/egezamb/helmbulance (0 stars, pushed 2026-10-10, no release).

## Direction 1: Lifecycle-sequence exploration with shrinking ("Jepsen for Helm charts")

Closest work
- Acto, SOSP 2023 (Gu et al., UIUC/Cornell/IBM). Push-button end-to-end testing of Kubernetes operators: repeatedly asks an operator to move the managed system between declared states, checks with oracles; 56 new bugs in 11 operators. https://cs.cornell.edu/~legunsen/pubs/GuETAlActoSOSP23.pdf , https://par.nsf.gov/biblio/10467388 . Repo xlab-uiuc/acto: 137 stars, last push 2026-08-13, no GitHub release. https://github.com/xlab-uiuc/acto
  - Does: generates sequences of state transitions (CR field changes) from the operator's CRD schema, state-centric oracles.
  - Does NOT: know Helm, releases, rollback, uninstall/reinstall, chart-version bumps, Helm client behaviour. The unit under test is an operator reconciling a CR, not a chart. I found no shrinking/minimisation in the snippets I could see (unverified; check the paper section on test generation).
- Sieve, OSDI 2022 (Sun et al., UIUC/VMware). Perturbs a controller's view of cluster state (crash, stale view, missed event) and compares runs with and without; 46 bugs in 10 controllers. https://www.usenix.org/conference/osdi22/presentation/sun . Repo sieve-project/sieve: 349 stars, last push 2024-09-26 (dormant). https://github.com/sieve-project/sieve
  - Does NOT: operator/user action sequences, Helm. Fault injection into controllers, not lifecycle operations.
- DUPTester / DUPChecker, SOSP 2021 (Zhang, Yuan, Lu et al.): study of 123 upgrade failures in 8 distributed systems; DUPTester found 20 new upgrade failures by running containers of different versions (full-stop, rolling, new-node-join). https://www.cs.purdue.edu/homes/yonglezh/pub/upgrade-sosp21.pdf , DOI 10.1145/3477132.3483577 (listed at https://unpaywall.org/10.1145%2F3477132.3483577 ).
  - Does: upgrade testing of the system's own data formats/protocols across versions. Does NOT: deployment packaging, Helm values, chart upgrade paths.
- Kivi, USENIX ATC 2024 (Liu, Lim, Beckett, Godfrey). SPIN model checking of Kubernetes controllers and configs, minimal counterexamples. https://www.usenix.org/conference/atc24/presentation/liu-bingzhe , https://arxiv.org/pdf/2311.02800 . Also Kamera (simulation + model checking of controller code, KubeCon NA 2025): https://thenewstack.io/kamera-uses-simulation-to-verify-kubernetes-controller-logic/
  - Does: model-level verification of controller logic. Does NOT: run real charts or Helm.
- Jepsen: 7,521 stars, v0.3.14, pushed 2026-09-28. https://github.com/jepsen-io/jepsen . Distributed-database consistency under faults; generator + checker + history; not Kubernetes packaging. etcd has a Jepsen-inspired Go framework (same snippet source): https://ossaidevjapan24.sched.com/event/1jKDA/bug-hunting-in-distributed-systems-using-robustness-tests-to-test-your-code-better-chun-hung-tseng-swisscom-arka-saha-broadcom
- Idempotence testing for IaC, Middleware 2013 (Hummer et al.): model-based test generation from state-transition graphs for Chef scripts, run in lightweight VMs. https://dl.ifip.org/IFIP-LNCS/hal-01480784v1 . This is the nearest "neighbour domain" precedent for sequence generation on configuration management.
- Shrinking: QuickCheck state machines / Hypothesis stateful and delta debugging are general techniques. I found no instance applied to Helm, Terraform or Ansible (search: "property-based testing infrastructure as code stateful shrinking Terraform Helm", "Jepsen delta debugging Kubernetes Helm"). Adjacent only: Mutiny (DSN 2024, fault injection in Kubernetes datastore) https://par.nsf.gov/biblio/10546553-mutiny-how-does-kubernetes-fail-what-can-we-do-about ; Anduril (fault-schedule search to reproduce failures) https://arxiv.org/abs/2404.11169v1 .
- Chart-level lifecycle tests today: chart-testing `ct install --upgrade` (previous chart revision to current, current to current) https://github.com/helm/chart-testing (1,645 stars, v3.15.0, pushed 2026-10-09); Terratest helm module install/upgrade/rollback in Go https://github.com/gruntwork-io/terratest ; metal-toolbox/helm-upgrade-test (0 stars) https://github.com/metal-toolbox/helm-upgrade-test . All fixed scripts, no generation, no shrinking.

Verdict: done for a neighbour domain (Kubernetes operators: Acto; distributed-system upgrades: DUPTester; Chef/IaC: Hummer 2013), not for Helm charts. Helm-specific lifecycle operations plus shrinking: nothing found.
Caveat: Acto's authors could extend to Helm quickly; their test-generation machinery (schema-driven value deltas) is the most transferable idea. Natural collaborator or competitor: Tianyin Xu's group (xlab-uiuc).

## Direction 2: Deployment-path differential testing (Helm 3/4, Argo CD, Flux, CNIs)

Closest work
- No test suite or paper comparing GitOps controllers against Helm CLI found (searches: "Argo CD vs Helm differences lookup hooks PreSync randAlphaNum", "Flux helm-controller differences from Helm CLI", gh repo search "helm argocd flux differential" returned nothing).
- The divergences are documented only as prose and issues:
  - Hook mapping, no install/upgrade distinction, pre-delete unsupported: Argo CD Helm docs https://argo-cd.readthedocs.io/en/release-2.9/user-guide/helm/ (older versions show same table; re-check current version).
  - Argo CD renders with `helm template` + apply, so `lookup` fails: https://github.com/argoproj/argo-cd/issues/5202 (open, updated 2025-10-22); enhancement proposal https://github.com/argoproj/argo-cd/issues/21745 (open, updated 2026-05-27); "document unsupported functions" https://github.com/argoproj/argo-cd/issues/14943 (open); "detect unsupported functions and warn" https://github.com/argoproj/argo-cd/issues/14944 (open since 2023). Red Hat RFE: https://issues.redhat.com/browse/RFE-3070
  - randAlphaNum / random data causing perpetual OutOfSync: described in Argo CD diffing docs mirror https://pasha-argocd.readthedocs.io/en/latest/user-guide/diffing/ and a secondary skill note https://skills.sh/kaynetik/skills/argocd (secondary source; I did not find an official statement in the snippet).
  - Flux helm-controller uses Helm SDK actions with its own drift detection, not dry-run: https://fluxcd.io/docs/migration/helm-operator-migration/ ; drift detection mode default disabled: https://v2-5.docs.fluxcd.io/flux/components/helm/api/v2/
  - Helm 4: SSA default for new installs, existing releases keep their previous apply method, kstatus `--wait` needs `watch` RBAC (HIP-0022), duplicate env vars rejected under SSA, flag renames: https://computingforgeeks.com/migrate-helm-3-to-helm-4-kubernetes/ , https://docs.camunda.io/docs/self-managed/deployment/helm/operational-tasks/helm-v4/ , https://enix.io/en/blog/helm-4/ (blogs and vendor docs, not Helm's own docs; verify against https://github.com/helm/helm , v4.3.0, 30,308 stars).
- Stats: argo-cd 24,361 stars, v3.5.4 https://github.com/argoproj/argo-cd ; fluxcd/helm-controller 511 stars, v1.6.5 https://github.com/fluxcd/helm-controller
- Differential testing precedent exists in other domains (e.g. browser engine vs Chrome), none found for Kubernetes deployers.

Verdict: nothing found as a tool or paper; the facts are scattered in docs and open issues. Those issues are an excellent seed corpus (each is a ready test case). Natural collaborators: Argo CD and Flux maintainers (the lookup issues are open since 2020 numbering #5202 and could use a reproducible matrix); competitors: none identified.

## Direction 3: Ecosystem-scale upgrade-history map

Closest work
- Minna, Massacci, Tuma, "Analyzing and Mitigating (with LLMs) the Security Misconfigurations of Helm Charts from Artifact Hub", MSR 2024 registered report, then Empirical Software Engineering 2025 (journal record: https://link.springer.com/article/10.1007/s10664-025-10688-0 ; arXiv https://arxiv.org/pdf/2403.09537 ; MSR https://2024.msrconf.org/details/msr-2024-registered-reports/4/Analyzing-and-Mitigating-with-LLMs-the-Security-Misconfigurations-of-Helm-Charts-fr ). Static analysis of Artifact Hub charts with Checkov, KICS etc., plus LLM fixes. Does NOT: install anything, look at upgrades or version history. (I could not read the Springer page; description is from the arXiv/MSR abstract. Note the EMSE paper is about security misconfigurations, not upgrades.)
- Zerouali et al., "Helm Charts for Kubernetes Applications: Evolution, Outdatedness and Security Risks", MSR 2023: 9,482 charts from Artifact Hub, versions, dependencies, outdated container images. https://conf.researchr.org/details/msr-2023/msr-2023-technical-papers/24/Helm-Charts-for-Kubernetes-Applications-Evolution-Outdatedness-and-Security-Risks . Closest to a version-history study, but static metadata, no behavioural testing.
- Spillner, HelmQA, "Quality Assessment and Improvement of Helm Charts for Kubernetes-Based Cloud Applications" (2019 preprint): static quality metrics over public charts. https://arxiv.org/pdf/1901.00644
- "Configuration Defects in Kubernetes" (2025 preprint): 719 defects from 2,260 scripts, 15 categories, linter found 26 new confirmed defects. https://arxiv.org/pdf/2512.05062 (dataset: https://ieee-dataport.org/documents/kubernetes-configuration-defect )
- "Kubernetes Misconfigurations in the Wild: Taxonomy, Evolution, and Automated Repair with LLMs" (arXiv 2609.27030, submitted 2026-09-22; 2,662 Stack Overflow issues; Kubecurity): https://arxiv.org/abs/2609.27030 . Security-focused, static.
- Bitnami: PR gate runs `ct` lint/install on kind; deeper "verification, functional and upgrade tests" run in an internal CI on GKE/TKG/AKS, not public: https://docs.bitnami.com/kubernetes/faq/get-started/understand-charts-release-process/ . bitnami/charts 10,415 stars https://github.com/bitnami/charts . Outputs are not published as a dataset.
- chart-testing `--upgrade`: previous revision to current only, per chart, only when SemVer non-breaking: https://github.com/billimek/chart-testing/blob/master/doc/ct_install.md (fork copy of upstream docs) ; upstream https://github.com/helm/chart-testing
- Vendor breaking-change policies show the problem is real but unmeasured at scale: Camunda treats values.yaml keys as public API https://helm.camunda.io/camunda-platform-helm/policies/breaking-changes ; ownCloud https://doc.owncloud.com/ocis/7.0/deployment/container/orchestration/tab-pages/breaking-changes.html
- Artifact Hub itself only runs Trivy image reports per version (latest daily, older weekly, nothing older than a year): https://artifacthub.io/docs/topics/security_report . Repo artifacthub/hub 2,108 stars, v1.23.0 https://github.com/artifacthub/hub

Verdict: done for a neighbour (static/ecosystem studies of Helm charts: Zerouali 2023, Minna 2025, Spillner 2019), not for behavioural upgrade testing. No dataset of cluster-observed upgrade breakage found (searches: "Helm chart upgrade breaking changes dataset Artifact Hub version history", "Helm chart upgrade testing empirical study").
Natural collaborators: Zerouali/Mens group (MSR 2023), Minna/Massacci (their pipeline already mines Artifact Hub charts); Artifact Hub maintainers.

## Direction 4: Upgrade "flight simulator" / preflight

Closest work
- Replicated Troubleshoot: preflight = collectors + redactors + analyzers from YAML, run before install to check the customer environment (cluster version, resources, etc.), also support bundles. https://docs.replicated.com/vendor/preflight-support-bundle-about , https://blog.replicated.com/preflight-checks/ . Repo replicatedhq/troubleshoot 587 stars, v0.134.1, pushed 2026-10-09 https://github.com/replicatedhq/troubleshoot . Checks the environment, does NOT execute the chart or rehearse an upgrade. Name collision: "preflight" is Replicated's term and CLI binary name.
- helm-diff: 3,502 stars, v3.15.15 https://github.com/databus23/helm-diff . Compares rendered manifests vs live release. Does NOT run anything, so cannot see hook effects, Secret regeneration at runtime, or SSA conflicts.
- Pluto 2,588 stars, v5.24.4 https://github.com/FairwindsOps/pluto ; kubepug similar: deprecated/removed API versions. Static.
- `helm upgrade --dry-run=server`: server-side validation, no behaviour (Helm docs; not separately searched).
- vcluster 11,340 stars, v0.37.2 https://github.com/loft-sh/vcluster ; Velero restore into a test cluster (documented as a use case; restore drills with a write fence and GitOps suspension: https://oneuptime.com/blog/post/2026-08-21-clone-restore-vcluster-oci-snapshot-reapply-config/markdown , blog, not primary). Both clone state; neither knows Helm release semantics.
- Signadot (per-PR sandboxes) and Speedscale (traffic replay): application-level change validation, not chart upgrade. https://signadot.com/docs/overview , https://speedscale.com/kubernetes-traffic-replay/
- Replicated KOTS 951 stars, v1.132.0 https://github.com/replicatedhq/kots : packaged-app installer with preflights; no upgrade rehearsal found.
- "Upgrade rehearsal" as a product: nothing found beyond practitioner advice (https://yomotherboard.com/question/whats-the-best-process-for-upgrading-helm-charts-with-significant-changes/ ). Searches: "rehearse Kubernetes upgrade ephemeral cluster clone production release", "helm upgrade rehearsal preflight kind" (gh repos: nothing).

Verdict: done for neighbours in pieces (static diff: helm-diff; deprecated APIs: Pluto; environment checks: Replicated preflight; state cloning: vcluster/Velero), but the combination (capture release shape without secret values, replay the planned upgrade with the same apply method and Helm version, observe) was not found. Treat as "nothing found" for the exact combination. Name advice: avoid "preflight". Natural competitor/collaborator: Replicated (they own the word and the vendor-to-customer channel).

## Direction 5: Taxonomy and behaviour contract

Closest work
- Weakness catalogues, all security or static-manifest oriented:
  - OWASP Kubernetes Top 10 (2025: K01 Insecure Workload Configurations ... K10 Inadequate Logging and Monitoring) https://owasp.org/www-project-kubernetes-top-ten/ . None concerns upgrade/day-two behaviour.
  - Configuration Defects in Kubernetes (15 categories) https://arxiv.org/pdf/2512.05062 ; Kubernetes Misconfigurations in the Wild taxonomy https://arxiv.org/abs/2609.27030 . One category, "Incorrect Helming", is about hard-coded values in templates, the nearest to a Helm-specific item (via search summary; confirm in paper).
  - Linters: kube-score 3,103 stars v1.20.0 https://github.com/zegl/kube-score ; Polaris 3,393 stars v10.2.5 https://github.com/FairwindsOps/polaris ; kube-linter 3,524 stars v0.8.3 https://github.com/stackrox/kube-linter ; kubeconform 3,218 stars https://github.com/yannh/kubeconform ; Checkov/KICS (used in Minna). Rules are single-manifest, static.
  - "Helm chart smells" as a named catalogue: no paper found (search: "Helm chart smells empirical study").
- Behaviour contract:
  - helm-unittest 1,401 stars, v1.2.1 https://github.com/helm-unittest/helm-unittest : assertions on rendered templates. Static render.
  - Kyverno Chainsaw 623 stars, v0.2.15 https://github.com/kyverno/chainsaw ; kuttl 826 stars, v0.27.0 https://github.com/kudobuilder/kuttl : declarative assertions on live cluster state. Generic mechanism, no chart-declared contract and no cross-upgrade invariants. Contract testing in Signadot is service API contracts, not chart invariants.
  - Values schema (`values.schema.json`) declares input shape only; Camunda's policy treats values keys as public API in prose (https://helm.camunda.io/camunda-platform-helm/policies/breaking-changes ).
  - Nothing found declaring "this key is stable across upgrades" in machine-readable form (searches: "Kyverno Chainsaw conftest kuttl contract testing Helm chart invariants", gh "helm chart conformance contract").
- Artifact Hub mechanisms: Verified publisher (via `artifacthub-repo.yml`, automated) and Official (request via GitHub issue) https://artifacthub.io/docs/topics/repositories/ ; security report (Trivy) https://artifacthub.io/docs/topics/security_report ; `artifacthub.io/*` Chart.yaml annotations (e.g. images, links) https://artifacthub.io/docs/topics/repositories/helm-charts/ . I found no documented route for third-party reports or custom badges; open issue list for "badge" showed nothing relevant (checked with gh against artifacthub/hub). So a badge would need Artifact Hub maintainers' agreement (CNCF project) or live outside it (shields.io-style endpoint in README, which charts could link from annotations).

Verdict: done for a neighbour domain (security/misconfiguration taxonomies and linters for Kubernetes manifests; live-cluster assertion frameworks), not for day-two chart behaviour. A numbered day-two catalogue and a chart-declared cross-upgrade contract: nothing found.

## Naming collisions and people
- "preflight": Replicated (CLI `preflight`, troubleshoot.sh). Avoid.
- "Chainsaw", "Pluto", "Kivi", "Acto", "Sieve" are taken; "Jepsen for X" is common marketing shorthand and fine as a tagline, not a name.
- "Helmbulance" has no collisions found in this pass (not exhaustively searched).
- Teams: xlab-uiuc (Acto, Sieve, Tianyin Xu); Zerouali/Mens (MSR 2023, Helm chart ecosystem); Minna/Massacci/Tuma (Artifact Hub mining); Replicated; Argo CD and Flux maintainers; Helm maintainers (Helm 4, HIP-0022); Artifact Hub (CNCF).

## Summary table

| Direction | Closest prior art | Gap left for Helmbulance | Novelty verdict |
|---|---|---|---|
| 1. Lifecycle sequences + shrinking | Acto (SOSP 2023), DUPTester (SOSP 2021), Sieve (OSDI 2022), Kivi (ATC 2024); Hummer 2013 for IaC; ct --upgrade | Helm operations (rollback, reinstall, chart bump, 3 to 4), shrinking, cluster-reproduced Helm findings | Done for a neighbour (K8s operators, distributed-system upgrades, Chef), not for Helm charts |
| 2. Deployment-path differential | Prose docs + open issues (argo-cd #5202, #21745, #14943, #14944); Helm 4 migration blogs | A runnable cross-deployer matrix (Helm 3/4, Argo CD, Flux, CNIs) | Nothing found as tool or paper; facts scattered |
| 3. Ecosystem upgrade-history map | Zerouali MSR 2023 (static), Minna EMSE 2025 (static security), Spillner 2019, Bitnami internal upgrade CI (not public) | Behavioural, cluster-observed upgrade edges across Artifact Hub, public dataset | Done for a neighbour (static Helm studies), not behavioural |
| 4. Upgrade flight simulator | Replicated preflight/Troubleshoot, helm-diff, Pluto, vcluster/Velero | Capture release shape without secrets, rehearse with same apply method/Helm version | Nothing found for the combination; pieces exist in neighbours |
| 5. Taxonomy + contract + badge | OWASP K8s Top 10, kube-score/Polaris/kube-linter, config-defect papers, Chainsaw/kuttl, Artifact Hub badges (no third-party route found) | Numbered day-two catalogue, chart-declared cross-upgrade invariants, badge | Done for a neighbour (security/static manifests), not day-two behaviour |
