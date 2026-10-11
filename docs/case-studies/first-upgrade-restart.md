# The first upgrade restarts pods for nothing

**Reported upstream:** [JupyterHub #3996](https://github.com/jupyterhub/zero-to-jupyterhub-k8s/issues/3996), [Semaphore #34](https://github.com/semaphoreui/charts/issues/34).

## The pattern

Many charts restart their pods when a Secret changes by putting a hash of the rendered Secret template in the pod template:

```yaml
annotations:
  checksum/secret: {{ include (print $.Template.BasePath "/secret.yaml") . | sha256sum }}
```

When that Secret template also keeps generated values with `lookup` (reuse the stored value, otherwise `randAlphaNum`), the hash goes wrong once:

1. **On install,** `lookup` finds nothing. The real Secret gets one random value. The `include` for the hash renders the template a second time, which draws another random value, so the hash does not describe the real Secret.
2. **On the first upgrade,** `lookup` returns the stored value. The hash now matches the real Secret, and that is a change, so every pod that carries it is replaced, although nothing changed.
3. **From then on,** the hash is stable.

The Secret itself never changes, so a Secret-only check misses this. Helmbulance's `upgrade-without-values` scenario compares pod UIDs across an upgrade with no new values (finding `HB-UV-003`). That control step is exactly the first upgrade after install.

## How the charts were found

1. **Static pre-screen of about 480 popular Artifact Hub charts.** It looked for pod-template annotations that hash an included template whose body, or a helper it includes, uses both `lookup` and a random generator. 20 charts matched, among them Harbor, JupyterHub, Authelia, Netbox, Zitadel, NeuVector, SeaweedFS, Semaphore and several Bitnami charts.
2. **Confirmed on kind for two of them.**

| Chart | Version | What restarts | Impact |
|---|---|---|---|
| JupyterHub | 4.4.2 | hub and proxy | short hub outage; single-user servers keep running |
| Semaphore | 16.2.2 | the app | a real outage: one replica, `Recreate` |

For JupyterHub, by hand:

| | hub pod | `checksum/secret` | `cookie_secret` |
|---|---|---|---|
| after install | `90d2a615…` | `ce4d9d3a…` | `29b3f6df…` |
| 1st upgrade, no changes | **replaced** | **changed** | unchanged |
| 2nd upgrade, no changes | unchanged | unchanged | unchanged |

The other 18 candidates are unverified; the pre-screen only ranks them.

## The fix

Hash the inputs that come from values, not the rendered Secret. Generated values never change after install, so they do not need to trigger a restart:

```yaml
annotations:
  checksum/values: {{ toJson .Values.someSection | sha256sum }}
```

The `keeps-secret` fixture in this repository follows this pattern.
