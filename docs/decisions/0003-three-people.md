# 0003: Three people: a reviewer joins

**Status:** accepted, 27 October 2026. **Decided by:** Ege, with Kadirhan informed. **Amends:** the working agreement and D7 in [0001](0001-v0.1-path.md).

## Context

0001 describes two people who review each other's pull requests. Reviews are now the bottleneck before v0.1 on 8 November: several of Ege's PRs have waited for one since 23 October. Arda ([@ardasemihyy](https://github.com/ardasemihyy)) joins as a third reviewer, to answer faster and to give each change a second pair of eyes that did not write it.

## Roles

- **Ege:** the CLI and the scenarios (`cmd/`, `internal/`), as in 0001.
- **Kadirhan:** CI, the GitHub Action, the corpus and the outbound gate (`.github/`, `action.yml`, `docs/corpus.md`), as in 0001.
- **Arda: reviewer.** Arda reviews pull requests from both; code Arda writes goes through a pull request like anyone else's.

Ege and Kadirhan stay the maintainers: they decide scope, the documents below, releases and anything posted upstream.

## Rules

These replace the two-person rules in 0001 wherever they differ.

- **Every code change goes through a pull request with a squash merge,** small and with a test.
- **Review.**
  - The author requests both other people.
  - One approval from someone other than the author is enough to merge, which is what the ruleset on `main` enforces.
  - A review comes within 48 hours. Whoever cannot make it says so on the PR.
- **Reviews that need a maintainer.** A change to any of these needs an approval from Ege or Kadirhan, not only Arda's:
  - `docs/architecture.md` and `docs/decisions/` need both maintainers, as before;
  - `.github/` and `action.yml` need Kadirhan, or Ege when Kadirhan wrote the change;
  - a change in what a verdict means (when a scenario says `FAIL`, `INCONCLUSIVE` or `PASS`) needs the maintainer who did not write it.
- **Who merges.** The author merges after approval. Reviewers do not merge someone else's PR, and do not push to another person's branch without asking; they suggest changes in comments.
- **After a push,** earlier approvals are dismissed. The reviewer re-approves once the new commits are checked. All review threads must be resolved before merge.
- **Merging without review.** As in 0001, the author may merge without an approval when `main` is broken, the change fixes a security problem, or **neither** of the other two has answered for 48 hours and the change blocks work. CI must be green, and the PR says "Merged without review:" and why. This never applies to `docs/architecture.md` or `docs/decisions/`.
- **Upstream.** Only Ege and Kadirhan post to upstream projects. Anyone may check a draft against the outbound gate in 0001.
- **No cap on open upstream threads** (Ege, 27 October). The D2 and working-agreement limit of three threads waiting for a first maintainer response is dropped. Every post still passes the outbound gate.
- **Security-class results** go privately, as in 0001, and are never discussed in a public issue, PR or review comment before the fix.

## Access

- Everyone on the repository needs at least write access, since the ruleset counts approvals only from people with write access.
- Admin stays with the maintainers. The organisation's default repository permission was `admin`, which gave every member admin on every repository; on 27 October Ege lowered it to `write`. Ege and Kadirhan are organisation owners; Arda is a member with write access.

## Updates to 0001

- **"How two people work on it":** read "the other developer" as "another person on the project" for reviews.
- **The ruleset paragraph is out of date.** The ruleset (24940714) now requires, besides a pull request and green CI:
  - one approval from someone other than the author;
  - stale approvals dismissed on push;
  - resolved review threads;
  - squash merges only.

  No one can bypass it.
