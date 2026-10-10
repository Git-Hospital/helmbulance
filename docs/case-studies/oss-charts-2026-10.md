# Open-source charts, first run (October 2026)

The first time Helmbulance ran against charts it did not write. Both charts generate a Secret value with `randAlphaNum`. Only one of them keeps it.

Setup: Helmbulance at commit 7c9cd34, Helm v4.2.1, kind v0.33 (Kubernetes 1.37), arm64. Scenario: `upgrade-without-values`.

| Chart | Version | Verdict | Why |
|---|---|---|---|
| [twuni/docker-registry](https://github.com/twuni/docker-registry.helm) | 3.0.0 | **FAIL** | `haSharedSecret` is generated on every render and is not preserved. |
| [openfaas/openfaas](https://github.com/openfaas/faas-netes) | 15.0.13 | **PASS** | The `basic-auth` Secret is created by a `pre-install` hook, so upgrades never re-render it. |

## docker-registry: `haSharedSecret` changes on every upgrade

**What Helmbulance reported.** `HB-UV-001`: `<release>-docker-registry-secret/haSharedSecret` changed in the control step. The control step is a `helm upgrade` with no values flags, where Helm reuses the install values. Nothing in the configuration changed, and the value still changed. The deployment reads it as `REGISTRY_HTTP_SECRET`.

**Confirmed by hand.** We installed the chart and upgraded it with no changes:
- The Secret's value changed.
- The registry pod was replaced (`…-8954487cd-…` → `…-65f6f8686f-…`).

The pod is replaced because the Deployment carries `checksum/secret`, a hash of the rendered `secret.yaml`, and that hash changes with every new random value.

**Impact.**
- **Restarts:** every `helm upgrade` restarts the registry, even when nothing changed.
- **Uploads during a rollout:** the registry uses the HTTP secret to sign upload state. During a rolling update with more than one replica, old and new pods hold different secrets, so uploads that move between them fail. The registry documentation asks for the same secret on every instance behind a load balancer.
- **Argo CD:** Argo CD renders with `helm template` and sees a new value on every sync. Upstream issue [#187](https://github.com/twuni/docker-registry.helm/issues/187) (open since September 2025) reports the resulting endless OutOfSync.

**Workaround today.** Set `secrets.haSharedSecret` explicitly.

**Upstream context.** A fix that used `lookup` ([#56](https://github.com/twuni/docker-registry.helm/pull/56)) was declined in 2022. The maintainer's reasons were testability (it needs a live cluster) and a split source of truth. A fix that fits that position:
- reference an existing Secret (an `existingSecret`-style value);
- or document `secrets.haSharedSecret` as required for production and multi-replica installs.

Exactly the trade-off Helmbulance exists to make visible.

## openfaas: correct negative

`generateBasicAuth: true` also uses `randAlphaNum`. Because the Secret is a `pre-install` hook resource, Helm creates it once and never re-renders it on upgrade. Helmbulance compared the Secrets after both upgrades, found no change, and reported `PASS`. The scenario does not flag every chart that calls `randAlphaNum`, only the ones whose values actually change.

## What this run taught us about the tool

- The generic impact text overstates some findings. "Passwords a database stored at first start" does not apply to the registry's HTTP secret. Impact and severity should depend on how the value is used, which the chart model can partly infer: env var name, mounted file, database image. Tracked for v0.2.
- Restarts caused by an upgrade with no changes are worth their own oracle. Here, pod replacement was the most visible symptom, and the Secret diff alone does not show it.
