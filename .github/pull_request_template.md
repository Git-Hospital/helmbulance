<!-- Keep what applies; say why when an item does not. -->

Fixes #  <!-- or "Part of #", which does not close the issue -->

## Problem

<!-- What is wrong, and the command that shows it on `main`. -->

## Change

## Checked

- [ ] `go vet ./...` and `go test ./... -count=1` pass.
- [ ] **A test or fixture that fails on `main`** and passes here. Name it.
- [ ] **Before and after:** for a change in a verdict, the output on `main` and on this branch (kind, helm version), or why a cluster was not needed.
- [ ] **Mutations:** the lines of the fix each broken on purpose, and the test that catches each one.
- [ ] A new finding ID has a fixture pair, or a line under `uncovered` in `testdata/verdicts.yaml` with the reason (the coverage lock, #27).
- [ ] `docs/architecture.md` says what changed, if a verdict, a finding or a scenario did.

## Questions

<!-- Each person asked here is also requested as a reviewer (0003). -->
