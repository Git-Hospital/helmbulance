# Research and roadmap proposal

Desk research done on 11 October 2026, before the CLI skeleton landed. It covers the market and competing tools, evidence that the scenario classes hurt real users, how comparable open-source tools grew (and how maintainers react to automated or AI-assisted reports), and technical design options. It ends with a phased roadmap and a list of kill or pivot signals.

| File | Language | Content |
|---|---|---|
| [2026-10-11-research-and-roadmap.tr.md](2026-10-11-research-and-roadmap.tr.md) | Turkish | The synthesis: findings, the roadmap with owners and exit criteria, upstream etiquette |
| [notes/market-and-pain.md](notes/market-and-pain.md) | English | Competitors, audience size, 26 linked issues across the scenario classes, Bitnami and Helm 4 events |
| [notes/oss-growth-playbook.md](notes/oss-growth-playbook.md) | English | OSS-Fuzz, Coccinelle, CodeQL, curl, FFmpeg, Hacktoberfest; disclosure norms; channels and realistic star counts |
| [notes/technical-design.md](notes/technical-design.md) | English | Helm 3/4 semantics, kind on runners, NetworkPolicy enforcement, static detectors, SARIF, scanning third-party charts, LLM loop |

Every factual claim carries a source link. Sentences marked as inference or estimate are the authors' judgement, not sourced facts. Star counts and Artifact Hub figures are snapshots from 10-11 October 2026.

## Status: written against 9c5e05b

The research saw a README-only repository. Commit 3ae143f (CLI skeleton, `upgrade-without-values`, `docs/architecture.md`) came later, so statements such as "no code yet" are out of date.

## Where the research and `docs/architecture.md` differ

**Update:** commit 7c9cd34 rewrote `docs/architecture.md` and settled all six points, so nothing here needs action:

1. Resolved: §3.1 now runs Helm and kind as commands.
2. Resolved: §3.2 names Argo CD and says `lookup` works under Flux.
3. Resolved: §3.5 uses an HMAC with a per-run key.
4. Decided: SARIF stays, from v0.2, with findings pointing at the template line or values key (§3.6).
5. Decided: `gitops-render` is in v0.1; `network-isolation` is v0.1, or v0.2 if time runs short (§3.4, §8).
6. Resolved: §3.6 lists exit codes 0 to 3.

The open items after the rewrite are in `docs/reviews/2026-10-11-architecture-review.md` and `docs/decisions/0001-v0.1-path.md` (separate PR).

The original notes, written against 3ae143f, follow for the record.

1. **Shelling out vs in-process.** Architecture §3.1 says Helm, client-go and kind are used in-process. The code in 3ae143f runs `helm` and `kind` as commands (`internal/execx`, `internal/sandbox`), and the research recommends exactly that: chart-testing and terratest both shell out, the run then uses the Helm the user actually has, and a Helm 3.22 / 4.3 matrix is cheap. §3.1 may need to follow the code.
2. **Flux and `lookup`.** Architecture §3.2 (and the README) say Argo CD and Flux render without cluster access. Argo CD does (`helm template`), but Flux's helm-controller performs a real install or upgrade, so `lookup` works there; Flux's drift detection compares the stored manifest instead of re-rendering ([helm-controller HelmRelease spec](https://github.com/fluxcd/helm-controller/blob/main/docs/spec/v2/helmreleases.md), [helm-controller#1583](https://github.com/fluxcd/helm-controller/issues/1583)). `gitops-render` needs separate expectations for the two.
3. **Secret hashes.** Architecture §3.4 keeps a plain SHA-256 per Secret key. The code uses an HMAC with a per-run key, which is safer: an unsalted hash of a short password can be reversed by brute force. §3.4 and §3.7 ("only key names and hashes") may need to follow the code.
4. **SARIF.** Architecture §3.7 plans SARIF so findings show up in code scanning and on pull requests. GitHub needs a physical file location per result, shows only the first one, and annotates a PR only for lines inside its diff ([SARIF support](https://docs.github.com/en/code-security/code-scanning/integrating-with-code-scanning/sarif-support-for-code-scanning)). Runtime findings have no natural line, so the research suggests Markdown in the job summary and JSON as the primary outputs, with SARIF optional.
5. **v0.1 scope.** Architecture §7 puts `network-isolation` in v0.1. The research suggests `gitops-render` instead and `network-isolation` later: Argo CD's `lookup` request alone has 254 reactions ([argo-cd#5202](https://github.com/argoproj/argo-cd/issues/5202)), it needs no chart knowledge and it reuses the Secret snapshot oracle, while the issue search found no community chart whose NetworkPolicy silently failed to isolate. The Open Archiver network finding is still a strong demo, so this is a trade-off, not a correction.
6. **Exit codes.** Architecture §3.7 lists 0, 1 and 2; `cmd/helmbulance/main.go` also defines 3 (`exitIncomplete`: a scenario could not finish, for example the chart did not install). §3.7 may need to follow the code.

## Time-sensitive item

The KubeCon + CloudNativeCon Europe 2027 CFP (Barcelona, 15-18 March 2027) closes on 11 October 2026 at 23:59 CEST; notifications on 7 December ([CFP page](https://events.linuxfoundation.org/kubecon-cloudnativecon-europe/program/cfp/)). The report's suggestion is a single 5-minute lightning talk about the documented failure classes rather than the tool's results.
