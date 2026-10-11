# Helmbulance: five directions, feasibility and red team

11 October 2026, code at 82afec5 (build and tests pass). **[M]** measured, **[S]** read in source, **[E]** estimate.

## Shared numbers

- Laptop: 8 GiB RAM, 8 CPUs [M]; Docker VM ~4 GiB, kind node idle 0.6-1.0 GiB, one real-chart scenario 3-8 min [E]; `report.json` timings will replace these.
- Argo CD v3.5.4: 7 workloads (core: 4), no resource requests [M]; core idle 0.3-0.5 GiB [E]; bundles Helm 4.2.1 [S].
- Flux v2.9.6: 7 controllers requesting 50-100m CPU, 64 MiB each; source and helm controllers need 150m/128 MiB [M].
- Helm 3 bug fixes ended 9 September 2026; security fixes end 10 February 2027 [S] ([helm.sh](https://helm.sh/blog/helm-v3-end-of-life)).
- Top 50 charts by stars [M]: 13,804 versions; edges: all pairs 3,578,833, consecutive 13,754, minor-to-minor 3,639 (last two years: 1,166). 7 of 12 Bitnami charts stopped in August 2025.

## 1. Jepsen for Helm charts

- **Map:** new engine: `internal/ops`, `explore`, `shrink` (ddmin); oracles become invariants checked after each op; `helm.Client` gains rollback and `--version`. `Scenario` stays.
- **Cost [E]:** 30-90 s per op; 2-8 h per chart; 6-9 person-weeks.
- **Red team:** lifecycles are sequential and mostly deterministic; Jepsen pays off through concurrency. Two-op bugs are found by fixed scenarios at 1/50 of the cost. *Refuted if* real issues often need three or more ordered ops.
- **Experiment:** minimal sequences for 20 day-two issues from top-50 trackers. **Pass:** 5 or more need three ops or an uncovered order.
- **Prerequisites:** ops refactor, M1, D4, D5, double-run confirmation.
- **Verdict: later.**

## 2. Deployment-path differential

- **Map:** new layer: a `Deployer` interface (helm3-csa, helm4-ssa, argocd, flux) replaces direct `env.Helm` calls; sandbox add-ons; a scenario-by-path report. Helm 3 vs 4 is nearly free: `helm.Client.Binary` exists.
- **Cost [E]:** add-ons need 0.6-1.0 GiB, so three parallel paths fit only charts under ~0.8 GiB; otherwise 10-20 min per chart. Helm 3/4: 1-1.5 person-weeks; Argo CD, Flux, differ: 5-8; CNIs: 2.
- **Red team:** Argo CD documents that it "cannot know" install from upgrade, so maintainers may answer "unsupported". *Refuted if* a divergence the chart does not document gets a fix merged. Argo CD's own pages disagree on recreating hooks (`helm.md` vs `sync-waves.md:53`) [S]; only a cluster settles openfaas. Signal: 160 open argo-cd "helm hook" and 97 helm/helm "helm 4" issues [M, keyword search].
- **Experiment:** openfaas 15.0.13 and the fixtures under Helm 3, Helm 4 and Argo CD, two no-change syncs. **Pass:** a divergence Helm-only scenarios miss, reproduced twice, under 3 GiB.
- **Prerequisites:** B1, M4, pinned manifests.
- **Verdict: now** for Helm 3/4; **next** for Argo CD, then Flux.

## 3. Upgrade-history map

- **Map:** batch mode: `chart-bump` scenario, Artifact Hub edge picker, Actions jobs (D3), dataset.
- **Cost [E, 2-8 min per edge]:** 1,166 edges = 39-155 laptop-hours or 2-8 h on 20 runners; all pairs 27 years. 4-6 person-weeks, plus 3-6 for a paper.
- **Red team:** maintainers fix only the latest version, `ct install --upgrade` already tests previous to current, and old images are gone. *Refuted if* recent edges show current, unreported defects that ct passes.
- **Experiment:** 10 charts, 3 same-major edges each, on Actions. **Pass:** 60% installable, median 6 min or less, one defect ct misses.
- **Prerequisites:** third-party mode or D3, image check, values per version.
- **Verdict: later**; the latest-edge pilot joins the corpus work next.

## 4. Upgrade flight simulator

- **Map:** a new product. §3.1 forbids other clusters, so the user exports the capture; the sandbox needs version matching, CRD stubs.
- **Cost [E]:** 5-15 min per rehearsal; 6-10 person-weeks, plus support.
- **Red team:** what breaks production upgrades (data, operators, real Secrets) cannot leave production, so rehearsals pass falsely; it serves platform teams, not upstream fixes. *Refuted if* most failures reproduce from release shape alone.
- **Experiment:** classify 15 public upgrade failures, reproduce 3. **Pass:** 8 shape-reproducible, 2 reproduced.
- **Prerequisites:** B2, a capture format, a §3.1 decision.
- **Verdict: drop** until users ask.

## 5. Taxonomy, contract, fix with proof

- **Map:** taxonomy is docs; `helmbulance.yaml` generalises D5's `expectedRotation` (fixes M3); a proof bundle runs a scenario on base and patched charts, the fixture-twin pattern upstream.
- **Cost [E]:** taxonomy 1-2, contract 2-3, proof bundle 1-2, AI patches 2-3 person-weeks.
- **Red team:** nobody adopts a file or badge from an unknown tool; AI-written PRs meet growing bans. *Refuted if* one proof bundle gets a fix merged.
- **Experiment:** a 15-class taxonomy, two public issues per class, and a one-command bundle for docker-registry's `existingSecret` fix. **Pass:** fail-before and pass-after reproduce twice; `repro.sh` runs without Helmbulance.
- **Prerequisites:** D5, M3, the outbound gate.
- **Verdict: now** for taxonomy and bundle; **next** contract; **later** AI and badge.

## Build sequence

1. Trusted oracles: B1-B3, M1, D4, D5.
2. Shared base: ops, a versioned `Observation` JSON, `Deployer`.
3. Helm 3/4, proof bundle, taxonomy (v0.1).
4. Argo CD, Flux, contract.
5. Top-50 latest-edge pilot.
6. Explorer and shrinker if experiment 1 passes; preflight on demand.

## Ownership (suggestion at the time; superseded by the plan in decision 0001)

- **Ege, engine:** ops, oracles, `Observation`, scenarios, sandbox add-ons, releases.
- **Kadirhan, differential layer:** `internal/diff`, Helm 3/4, proof bundle, corpus, taxonomy, gate; later the shrinker.
- **Seam:** `Observation` and `Deployer`, fixed in week 1, so neither waits; decision 0001 now uses this split.

## Most distinctive, soonest

**Direction 2, starting with Helm 3 vs Helm 4.** Cheap on today's code, timely (Helm 3 security support ends 10 February 2027), unclaimed (`prior-art.md`).
