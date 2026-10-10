# 0002: Long-term direction

**Status:** proposed. **Decides:** Ege and Kadirhan. **Inputs:** [`prior-art.md`](../research/directions/prior-art.md) (closest existing work, with sources) and [`feasibility.md`](../research/directions/feasibility.md) (cost, red team and a one-week experiment per direction; numbers marked measured, read in source or estimated).

## The question

What would make Helmbulance a tool nobody else has, rather than one more checker? Five directions were evaluated. Each one reuses the same core: a control and a treatment, and reporting only what the cluster reproduced.

## Summary

| Direction | Closest prior art | What is left for us | Proposed verdict |
|---|---|---|---|
| 1. Lifecycle exploration with shrinking ("Jepsen for Helm charts") | Acto (SOSP 2023), Sieve (OSDI 2022), DUPTester (SOSP 2021), Kivi (ATC 2024): operators, controllers and distributed-system upgrades, not Helm charts | Helm operations (rollback, reinstall, chart bump, Helm 3 to 4), shrinking a failing sequence to the minimum | **Later**, after its experiment |
| 2. Deployment-path differential testing | Nothing found as a tool or paper. Argo CD vs Helm differences exist only in docs and open issues ([argo-cd#5202](https://github.com/argoproj/argo-cd/issues/5202), [#21745](https://github.com/argoproj/argo-cd/issues/21745), [#14943](https://github.com/argoproj/argo-cd/issues/14943), [#14944](https://github.com/argoproj/argo-cd/issues/14944)) | A runnable matrix: the same chart through Helm 3, Helm 4, Argo CD and Flux, with the results compared | **Now** for Helm 3 vs Helm 4; **next** for Argo CD, then Flux |
| 3. Upgrade-history map of popular charts | [Zerouali et al., MSR 2023](https://conf.researchr.org/details/msr-2023/msr-2023-technical-papers/24/Helm-Charts-for-Kubernetes-Applications-Evolution-Outdatedness-and-Security-Risks) and [Minna et al., EMSE 2025](https://link.springer.com/article/10.1007/s10664-025-10688-0) are static studies | Behavioural, cluster-observed upgrade edges as a public dataset | **Later**; a pilot on the latest upgrade of the top 50 charts comes with the corpus work |
| 4. Upgrade rehearsal from a production release's shape | [Replicated preflight](https://docs.replicated.com/vendor/preflight-support-bundle-about) checks the environment only; helm-diff, Pluto, vcluster and Velero cover pieces | Rehearsing a planned upgrade from a captured release shape | **Drop** until users ask. It conflicts with §3.1, and what breaks real upgrades (data, operators, real Secrets) cannot leave production. "Preflight" is Replicated's term, so avoid the name |
| 5. Day-two taxonomy, behaviour contract, fix with proof | [OWASP Kubernetes Top 10](https://owasp.org/www-project-kubernetes-top-ten/), kube-score and Polaris rules: security and static manifests, not day-two behaviour | A numbered catalogue of day-two chart weaknesses; chart-declared invariants that hold across upgrades; proof bundles | **Now** for the taxonomy and proof bundles; **next** for the contract; **later** for AI-written patches and any badge |

## Why direction 2 first

**It is unclaimed.** No tool or paper runs one chart through several deployers and compares the results.

**It is timely.**
- Helm 3 security fixes end on 11 November 2026.
- Helm 4 installs with server-side apply.
- A release first installed by Helm 3 keeps client-side apply when Helm 4 upgrades it (`helm upgrade --help` in Helm 4: `--server-side` defaults to `auto`, which "defaults the value from the previous chart release's method").
- Many users will cross that line in the coming months.

**It is cheap on today's code.** helm is already run as a command (`helm.Client.Binary`). A Helm 3 vs Helm 4 comparison needs a second binary and a way to compare two observations, not a new engine.

**It has a concrete first case.** openfaas creates its `basic-auth` Secret in a `pre-install` hook with `randAlphaNum` (`chart/openfaas/templates/secret.yaml`).
- Helm CLI upgrades leave it alone, which is why the first [case study](../case-studies/oss-charts-2026-10.md) reports PASS.
- Argo CD maps `pre-install` to `PreSync` and documents that it cannot tell an install from an upgrade: every operation is a sync ([Argo CD Helm docs](https://argo-cd.readthedocs.io/en/stable/user-guide/helm/)).
- Argo CD's own pages disagree on whether the hook Secret is re-created, so only a cluster run settles it. That run is the first experiment below.

**Red team.** Argo CD documents that it "cannot know" an install from an upgrade, so maintainers may answer "Argo CD is unsupported".
- That argument is refuted if a divergence the chart does not document gets a fix merged.
- Until then, findings from this direction are framed as "behaves differently under Argo CD", with evidence, not as chart bugs.

## Experiments, one week each

| Direction | Experiment | Pass |
|---|---|---|
| 2 | openfaas 15.0.13 and the fixtures under Helm 3, Helm 4 and Argo CD, two syncs with no change | A divergence that Helm-only scenarios miss, reproduced twice, in under 3 GiB of memory |
| 5 | A first taxonomy with two public issues per class, and a one-command proof bundle for docker-registry's `haSharedSecret` (an explicit-value fix such as `existingSecret`, not `lookup`, which the maintainer declined in [#56](https://github.com/twuni/docker-registry.helm/pull/56), see the [case study](../case-studies/oss-charts-2026-10.md); whether they would accept it is unknown) | Fail-before and pass-after reproduce twice; `repro.sh` runs without Helmbulance |
| 1 | Write the minimal operation sequence for 20 day-two issues from the trackers of the top 50 charts | At least 5 need three or more operations, or an order no fixed scenario covers |
| 3 | 10 charts, 3 same-major upgrade edges each, on GitHub Actions | 60% installable, median of 6 minutes or less, at least one defect that `ct install --upgrade` misses |

The top 50 Artifact Hub charts by stars have 13,804 published versions. That is 13,754 consecutive upgrade edges, or 1,166 minor-to-minor edges from the last two years. A full map is out of reach. The pilot in direction 3 decides whether a partial one is worth it.

## Build order

1. **Trusted oracles:** B1 to B3, M1, D4 and D5 from the review and decision 0001.
2. **Shared base:** lifecycle operations as reusable steps, a versioned `Observation` JSON, and a `Deployer` interface.
3. **Helm 3 vs Helm 4**, proof bundles and the taxonomy, in v0.1.
4. **Argo CD and Flux** deployers, and the behaviour contract (it generalises the `expectedRotation` list from D5).
5. **The top 50 latest-upgrade pilot.**
6. **Exploration and shrinking,** only if experiment 1 passes. Rehearsal only on demand.

## Constraints

**Heavy runs go to GitHub Actions:** Argo CD with Flux, large charts, and images published only for amd64 (the corpus draft found mongodb, argo-cd and harbor images among them; to be re-checked when the corpus is merged).
- A developer laptop with 8 GiB of RAM holds a kind node and a small chart, not three deployers at once.
- Public repositories get larger standard runners (4 vCPU, 16 GiB at the time of writing, see [GitHub-hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)) at no cost.

**Third-party charts follow D3 of decision 0001** until the hardened mode exists.

## Ownership (proposed; Ege and Kadirhan decide)

See the plan in decision 0001:
- Ege owns the engine (operations, oracles, `Observation`, scenarios, sandbox).
- Kadirhan owns invocation and the differential layer (`Deployer` implementations, the comparison, proof bundles, corpus, taxonomy, gate).
- The two meet at `Deployer` and `Observation`.
