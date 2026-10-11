# yugabyte 2026.1.2: a patch release that breaks upgrades

**Reported upstream:** [yugabyte/charts#240](https://github.com/yugabyte/charts/issues/240).

## How it was found

1. **Pre-screen.** Helmbulance pre-screened the most-starred charts on Artifact Hub (about 480 of them). For each one it compared the latest version with the version before it, rendered as an upgrade (`--is-upgrade`), and looked for changes to fields Kubernetes does not allow to change:
   - Deployment and StatefulSet selectors;
   - StatefulSet `volumeClaimTemplates` and `serviceName`;
   - Job templates.

   Six charts were flagged:
   - **Three were major-version bumps.** Breaking changes are expected there.
   - **Two were false positives.** Their Jobs are named after the release revision, so a real upgrade creates a new Job instead of changing an existing one.
   - **One was a patch release:** yugabyte 2026.1.1 → 2026.1.2.
2. **`chart-upgrade` on kind.** The scenario installed 2026.1.1 with a one-master, one-tserver universe and upgraded 2026.1.1 to itself (the control, which passed). Then it upgraded to 2026.1.2:

```
HB-CU-001: Upgrading from 2026.1.1 fails
helm upgrade failed: UPGRADE FAILED: ... StatefulSet.apps "yb-master" is invalid:
spec.selector: Invalid value: {"matchLabels":{"app":"yb-master"}}: field is immutable
(same for yb-tserver)
```

3. **Reproduced by hand** with plain `helm install` and `helm upgrade`: same error.

## Cause

| Chart versions | StatefulSet selector with the default `oldNamingStyle: true` |
|---|---|
| 2024.2.x, 2025.2.x | `{app}` |
| 2026.1.0, 2026.1.1 | `{app, release}` |
| 2026.1.2 | `{app}` |

2026.1.0 added `release` to the selector, which broke upgrades from 2025.2. The fix in 2026.1.2 ([PLAT-22174], "Restores the immutable StatefulSet selector … so helm upgrade no longer fails") removed it again. That fixed the old path and broke the new one: every universe installed with 2026.1.0 or 2026.1.1 now fails to upgrade to 2026.1.2.

The fix's test plan was `helm unittest`, which checks rendered templates. A template test cannot see that the cluster already holds an object with the other selector. Only an upgrade against a live release shows it, and that is what `chart-upgrade` does.

## Workaround, and what we could not verify

`kubectl delete sts yb-master yb-tserver --cascade=orphan` before the upgrade lets `helm upgrade` succeed: Helm recreates the StatefulSets with the new selector, and they adopt the existing pods and volumes. We verified that much.

On kind, the 2026.1.2 pods then could not write to their data directory. 2026.1.2 also switches the pods to a non-root user and relies on `fsGroup` to fix volume ownership, and kind's local-path storage does not apply `fsGroup`. We therefore did not report that part as a chart bug. It is a limit of the sandbox, and a reason for Helmbulance to offer a CSI driver that applies `fsGroup`.

## What it taught us about Helmbulance

- **A cheap static pre-screen finds candidates in minutes; the cluster decides.** Two of the six candidates were false positives. A first version of the pre-screen produced another one by rendering the new version as an install instead of an upgrade.
- **The sandbox's storage needs to apply `fsGroup`.** Otherwise permission changes between versions cannot be tested. Tracked for v0.2.
- **Large images and flaky registries make runs slow.** Loading images into the node from a local archive worked when pulls failed. The registry mirror moves up the roadmap.
