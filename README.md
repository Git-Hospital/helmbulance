# Helmbulance

**Behavioural tests for Helm charts.** Helmbulance installs a chart into a throwaway Kubernetes cluster and runs the things that go wrong in production: upgrades without the original values, uninstall and reinstall, password rotation, network isolation, broken processes, hostile traffic. It reports what actually broke, with evidence and a suggested fix.

> **Status: early.** The project starts from a real case study (below). The CLI and GitHub Action are being built; nothing is released yet.

## Why

Static scanners (Trivy, Checkov, kube-linter, Kubescape, Polaris) read rendered YAML. `ct install` checks that a chart installs and that `helm test` passes. Both are useful, and neither tells you what happens on day two.

The most expensive chart bugs are behavioural. They only show up when something happens to a running release:

- An upgrade without the original `--set` flags silently drops an encryption key, and the app keeps running without it.
- A password rotation restarts the app but not the database it talks to.
- A NetworkPolicy exists, but the cluster's CNI never enforces it.
- A liveness probe calls the database, so a slow database restarts every app pod at once.

None of these produce an invalid manifest, so no linter flags them. Each one is found in minutes by doing the thing and watching what happens.

## How it works

1. **Install** the chart into an ephemeral [kind](https://kind.sigs.k8s.io/) cluster with each values file you give it.
2. **Run scenarios** against the live release. Each scenario is a small script: act (upgrade, delete, rotate, connect, flood), then assert on the cluster's state.
3. **Report** every failure with the command that reproduces it, the evidence (events, logs, rendered diff), the impact, and a suggested patch.

AI review can propose findings. Helmbulance only reports a finding once the cluster has reproduced it. In our case study, one of the AI reviewers' claims turned out to be wrong when tested, and another turned out to be worse than expected. Both were only settled by testing on a cluster.

## Scenarios

| Scenario | Question it answers | Status |
|---|---|---|
| `upgrade-without-values` | Does `helm upgrade` without the original values regenerate or drop secrets and keys? | planned for v0.1 |
| `reinstall` | After `helm uninstall` and `helm install`, do data and credentials come back? | planned for v0.1 |
| `network-isolation` | Are internal ports and bundled databases unreachable from other namespaces? Is the NetworkPolicy actually enforced? | planned for v0.1 |
| `secret-rotation` | When a password changes, does every component that uses it restart? | planned |
| `process-kill` | If a process inside the container dies, does the liveness probe notice? | planned |
| `probe-abuse` | Do probes depend on things outside the process, such as the database, so that outside load or an outage makes them fail? | planned |
| `immutable-fields` | Does a values change hit an immutable field (StatefulSet volume templates, selectors) and break the upgrade? | planned |
| `gitops-render` | Does the chart render the same without cluster access, as Argo CD and Flux do (`lookup` returns nothing there)? | planned |

## Case study: Open Archiver

We wrote a Helm chart for [Open Archiver](https://github.com/LogicLabs-OU/OpenArchiver), a self-hosted email archive (upstream request: [#242](https://github.com/LogicLabs-OU/OpenArchiver/issues/242)). We built it in milestones, and every milestone was tested on kind and reviewed for architecture and security. These are the problems that testing on a live cluster found, and how each was solved:

| Problem | How it showed up | Fix |
|---|---|---|
| **Storage encryption key silently lost.** An upgrade without the key in values removed it from the Secret. The app does not fail without a key: it writes new mail unencrypted and serves the old files as unreadable bytes. | Upgraded without the value, then inspected the Secret and the app's behaviour. | Keep the stored key through `lookup`, refuse a different key at render time, and add an option that stops the pod when the key is missing. |
| **Database password double-encoded in the app.** A password with `@ : / # ?` worked when written plainly and failed when correctly URL-encoded, the opposite of what users expect. | Ran the app's own helper against a real PostgreSQL with a matrix of passwords. | Upstream fix: [OpenArchiver#493](https://github.com/LogicLabs-OU/OpenArchiver/pull/493). In the chart, the password is passed as `PGPASSWORD` and never inside the URL. |
| **Password rotation broke the job queue.** Changing the Valkey password restarted the app but not Valkey, so every queue connection failed. | Rotated the password with `helm upgrade` and checked both pods and the app's logs. | A checksum annotation on every pod that reads the password. |
| **PostgreSQL password change locked the app out.** PostgreSQL reads its passwords only when it creates the database, so the chart updated the Secret and the app could no longer log in. | Upgraded with a new password. | The chart refuses the change and explains how to rotate the password safely. |
| **Internal API port reachable cluster-wide.** The app's backend listens on a port that the Service does not expose, but every pod could still reach it. | Connected from another namespace, with and without the policy. | A NetworkPolicy that admits only the web port. The bundled services accept connections only from the app and have no egress. |
| **App connected as a database superuser.** A SQL injection would have reached `COPY … TO PROGRAM`. | Queried `pg_roles` on the live database. | A separate non-superuser app role that owns the database. The superuser can log in through the local socket only. |
| **One more finding, in responsible disclosure.** | Reproduced on kind. | Details after the upstream fix. |
| **A reported problem that was not real.** A reviewer claimed PostgreSQL would be SIGKILLed on every stop. | Deleted the pod and timed it: a clean shutdown in one second, because containerd honours the image's `STOPSIGNAL`. | No change. This is why findings need a cluster to confirm them. |

The chart will be published separately; this repository will link to it.

## Roadmap

- **v0.1:** CLI (`helmbulance test ./chart -f values.yaml`) with the first three scenarios, a Markdown report, and a GitHub Action.
- **v0.2:** the remaining scenarios, suggested patches in the report, and running against charts from Artifact Hub.
- **Later:** scanning popular community charts and sending fixes upstream, and a hosted CI service for teams that ship charts to customers.

## Contributing

Issues and ideas are welcome, especially failures you have seen in production that a scenario could catch. Open an issue describing what happened and how you found out.

## Team

- [Ege Zambelli](https://github.com/egezamb)
- [Kadirhan Emre](https://github.com/kassvl)

## License

[Apache-2.0](LICENSE)
