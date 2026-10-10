# Helmbulance architecture

Status: design, before v0.1. This document sets the boundaries the code will be built inside; it changes when the code teaches us something.

## 1. What the system has to do

Given a Helm chart and one or more values files:

1. Bring up a disposable Kubernetes cluster that behaves like a production one, wherever it matters for the test: a CNI that enforces NetworkPolicy, a default StorageClass, an ingress or Gateway controller, metrics, and Pod Security admission.
2. Install the chart and understand what it deployed: workloads, ports, probes, Secrets, volumes and policies.
3. Run day-two scenarios against the live release. Some are lifecycle operations (upgrade, uninstall, rotate). Some are faults (kill a process, delete a pod). Some are hostile traffic (connect from another namespace, flood an endpoint, spoof headers).
4. Decide, with evidence, whether each scenario exposed a defect, and report it in a form that people and CI can use.
5. Never harm anything outside the sandbox.

## 2. Principles

- **Reproduced or not reported.** A finding exists only if the cluster reproduced it. Hypotheses from rules or from an AI model are inputs to scenarios, never findings.
- **Control before treatment.** Each assertion is checked once on a fresh, untouched release (the control) and once after the action (the treatment). A port that is closed in the control proves nothing about the NetworkPolicy. Our case study needed exactly this: port 4000 was open without the policy and closed with it.
- **App-agnostic first.** Scenarios work from what Kubernetes can observe: pods, events, Secrets, volumes, ports. Knowledge of a specific application is an optional plug-in.
- **The chart is untrusted code.** A chart can create privileged pods, mount `hostPath` and run any image. The sandbox is the security boundary, not the chart's good behaviour.
- **Deterministic and explainable.** Same inputs, same verdict. Every verdict carries the commands, the timings and the evidence that produced it.

## 3. Components

```
                 ┌──────────────────────── helmbulance CLI (Go) ────────────────────────┐
 chart + values ─▶  Planner ──▶ Sandbox manager ──▶ Scenario engine ──▶ Reporter      │──▶ Markdown / JSON / SARIF
                 │     │              │                    │   ▲                        │
                 │  Chart model   kind / k3d / VM      Probes & attack pods            │
                 │  (render, diff)  + addons           (in-cluster, sandbox only)      │
                 └──────────────────────────────────────────────────────────────────────┘
```

### 3.1 CLI

- Written in Go: Helm, client-go and kind are Go libraries, so the CLI uses them in-process instead of shelling out, and it ships as one static binary.
- Entry points: `helmbulance test ./chart -f values.yaml [--scenario …]`, `helmbulance list-scenarios` and `helmbulance explain <finding-id>`.
- Uses its own kubeconfig file for the sandbox. It never reads or changes the user's kubeconfig, and it refuses to run against any cluster it did not create, unless the user passes an explicit `--target-context` together with a confirmation flag.

### 3.2 Chart model (planner input)

Renders the chart and builds a typed model of what it creates:

- **Workloads:** containers, ports, probes, security contexts, update strategy, volumes, and the env vars that come from Secrets.
- **Secrets:** the keys, which workload reads each key, and whether a value is generated. Generated values are detected by rendering twice and diffing the output.
- **Network:** Services, Ingress/Gateway routes and NetworkPolicies.
- **Storage:** PVCs and StatefulSet `volumeClaimTemplates`.
- **Uses of `lookup`:** detected by rendering with and without cluster access and diffing. This is the GitOps hazard (Argo CD and Flux render without cluster access).

The planner selects only the scenarios that apply. For example, `secret-rotation` runs only when some workload reads a Secret.

### 3.3 Sandbox manager

The sandbox has two jobs: be realistic enough for the test, and contain whatever the chart and the attacks do.

**Local mode (v0.1):**
- A [kind](https://kind.sigs.k8s.io/) cluster with a pinned node image and the addons the scenarios need:
  - a NetworkPolicy-enforcing CNI (kindnet enforces it in current releases; Calico or Cilium is optional);
  - a local-path StorageClass;
  - an ingress or Gateway controller;
  - metrics-server;
  - Pod Security admission labels.
- **Isolation:** the chart is the user's own and runs on the user's machine, like `ct install` does today. The sandbox namespace gets no route outside the cluster except image pulls.
- **Image cache:** a pull-through registry mirror shared between runs, so repeated runs do not hit Docker Hub rate limits.

**CI mode (v0.2):**
- The same as local mode, on the GitHub Actions runner. The runner VM is disposable, which is the isolation.

**Hosted mode (later, only for running other people's charts):**
- A container is not a security boundary for untrusted charts: a privileged pod inside kind is root on the Docker host. Every job gets its own microVM (Firecracker or Kata) or a disposable cloud VM.
- The VM is destroyed after the job.
- No credentials exist inside the VM.
- Egress is limited to an allow-list registry mirror.

### 3.4 Scenario engine

Each scenario is a Go type with five phases:

| Phase | What it does |
|---|---|
| `Applies(model)` | Decides whether this chart has what the scenario tests |
| `Setup` | Installs a fresh release in its own namespace and waits until it is ready |
| `Control` | Records the baseline: pod UIDs, restart counts, Secret hashes, PVC sentinels, reachable ports |
| `Act` | Performs the operation: upgrade, uninstall/install, rotation, kill, connect, flood |
| `Observe` + `Assert` | Waits for the system to settle (with a timeout) and compares against the control |

Scenarios that only read (network reachability, probe checks) can share a release. Scenarios that change the release run in parallel namespaces, each with its own install. A run is bounded by a global time budget.

**Generic oracles** answer "did it break?" without knowing the app:

| Oracle | How |
|---|---|
| Secret drift | SHA-256 of every Secret key before and after. Only hashes are kept, never values. |
| Data survival | Before the action, an ephemeral container writes a sentinel file into each PVC. After the action, it reads the file back. |
| Health | Ready conditions, restart counts and termination reasons (OOMKilled, Error), plus pod events. |
| Credential mismatch | Repeated authentication errors in the logs after the action, with patterns per known component (`WRONGPASS`, `password authentication failed`, HTTP 401/403 bursts). |
| Reachability | A probe pod connects to each Service and container port, from the release namespace and from a foreign namespace. |
| Rollout coverage | After a Secret changes, every pod that reads it got a new UID. |

**Application plug-ins** (optional) add app-level oracles, for example "can log in", "search returns", or a backup and restore round trip. A plug-in is declarative: HTTP or exec steps with expected results.

### 3.5 Attack and probe pods

- Hostile traffic runs from purpose-built images (`helmbulance/probe`) inside the sandbox. It never runs from the host, so it reaches only what the cluster can reach.
- The attack pod runs as non-root and has no Kubernetes API access. A NetworkPolicy lets it reach only the namespace under test.
- Attacks are bounded:
  - a maximum request rate and duration;
  - only toward Services of the release under test;
  - target addresses checked against the sandbox's pod and service CIDRs before anything is sent.
- **v0.2 attack catalogue:**
  - request floods on unauthenticated paths;
  - header spoofing;
  - slow clients;
  - connections to non-exposed container ports;
  - egress attempts from every workload.
- No exploit payloads. The goal is to show misconfiguration (a probe that can be failed from outside, a port that is open, egress that is allowed), not to break the application.

### 3.6 Fault injection

Process-level faults use ephemeral debug containers (`kubectl debug`-style) that share the target pod's process namespace and send a signal. Nothing is added to the chart. Pod-level faults (delete, evict) and node-level faults (drain) use the API.

### 3.7 Reporter

- **Finding format:**
  - ID, title, severity, scenario;
  - control and treatment evidence;
  - the reproduction command;
  - impact;
  - a suggested fix (a values change or a template snippet).
- **Output formats:**
  - Markdown for people;
  - JSON as the stable machine format;
  - SARIF, so findings appear in GitHub code scanning and on pull requests.
- Secret values never appear in reports, only key names and hashes.
- Exit codes: 0 for clean, 1 for findings at or above `--fail-on`, 2 for a tool error. CI can rely on them.

### 3.8 Optional AI hypotheses

A model may read the chart and the application source and propose hypotheses, such as "the readiness probe calls the database, so a slow query makes it fail". Each hypothesis maps to a scenario with parameters, and is reported only if that scenario reproduces it.

The AI step is off by default and provider-agnostic. It never runs commands itself.

## 4. Safety rules for the tool itself

- Cluster operations run only against sandboxes Helmbulance created. It checks the cluster's ownership label before every run.
- Attack targets are checked against the sandbox CIDRs. Anything outside them is refused.
- Pinned, signed images are used for probe and attack pods.
- Secret values are never written to disk, logs or reports.
- Release artefacts are signed with cosign and ship with an SBOM. The GitHub Action pins its dependencies by commit SHA.

## 5. Hard problems we already know about

- **Flakiness.** Slow image pulls and slow first starts look like failures. Mitigations:
  - readiness-based waits with generous, configurable timeouts;
  - an image cache;
  - the control run doubles as a "does it even start" gate. If the control fails, scenarios are skipped and the chart is reported as not installable, not as having findings.
- **Realism versus speed.** A full cluster with addons takes about a minute, and the first start of some apps takes several. Scenarios share a cluster and run in parallel namespaces. The cluster can be reused between runs with `--keep-cluster`.
- **CNI differences.** NetworkPolicy behaviour depends on the CNI. The report states which CNI was used. A finding about enforcement is phrased as "not enforced on <CNI>", not as a universal claim.
- **App-specific truth.** Generic oracles cannot tell if search results are correct. That is what application plug-ins are for. We keep their scope small.

## 6. Repository layout

```
cmd/helmbulance/        CLI entry point
internal/chartmodel/    rendering, diffing, the typed model
internal/sandbox/       kind lifecycle, addons, registry mirror
internal/scenario/      engine, phases, scheduling
internal/oracle/        generic oracles
internal/report/        Markdown, JSON, SARIF
scenarios/              one package per scenario
images/probe/           probe and attack image
plugins/                application plug-ins (declarative)
docs/                   architecture, scenario reference, case studies
```

## 7. Milestones

| Version | Scope |
|---|---|
| v0.1 | Local CLI, kind sandbox, chart model, the `upgrade-without-values`, `reinstall` and `network-isolation` scenarios, Markdown and JSON reports |
| v0.2 | GitHub Action and SARIF; the probe and attack image; `secret-rotation`, `process-kill` and `probe-abuse` |
| v0.3 | `immutable-fields` and `gitops-render`, application plug-ins, optional AI hypotheses |
| Later | Hosted runs with microVM isolation, a GitHub App, a public report of popular charts |
