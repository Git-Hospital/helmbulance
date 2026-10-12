package scenario

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egezamb/helmbulance/internal/kube"
	"github.com/egezamb/helmbulance/internal/oracle"
)

// ChartUpgrade installs an earlier version of the chart and upgrades it to the chart under test,
// with the same values, the way users upgrade.
//
// Control: an upgrade from the earlier version to itself. Secrets that change there are
// regenerated on every upgrade and are not blamed on the new version. Treatment: the upgrade to
// the new version. It reports an upgrade that fails, Secret values the new version changes or
// drops, and workloads that are not healthy afterwards.
type ChartUpgrade struct{}

const chartUpgradeNamespace = "hb-chart-upgrade"

func (ChartUpgrade) Name() string { return "chart-upgrade" }

func (ChartUpgrade) Description() string {
	return "Installs an earlier chart version (--from-version) and upgrades to the chart under test with the same values."
}

func (s ChartUpgrade) Run(ctx context.Context, env *Env) (res Result) {
	res.Scenario = s.Name()
	if env.FromVersion == "" {
		res.Verdict = Inconclusive
		res.Summary = "skipped: give --from-version to say which version users upgrade from"
		return res
	}
	from := env.From
	if from == "" {
		from = env.Chart
	}
	const release, ns = "hb-chart", chartUpgradeNamespace
	defer func() {
		if err := env.Helm.Uninstall(ctx, release, ns); err != nil {
			fmt.Fprintf(env.Log, "cleanup: %v\n", err)
		}
		if err := env.Kube.DeleteNamespace(ctx, ns); err != nil {
			fmt.Fprintf(env.Log, "cleanup: %v\n", err)
		}
	}()

	mark := len(env.Runner.Transcript())
	if err := env.Helm.Install(ctx, release, ns, from, env.Values, "--version", env.FromVersion); err != nil {
		return inconclusive(res, "the earlier version did not install", err)
	}
	repro := []string{reproLine(env.Runner.Transcript()[mark].Line)}
	if _, err := installedFrom(ctx, env, release, ns); err != nil {
		return inconclusive(res, "could not confirm the earlier version", err)
	}

	snapshot := func() (oracle.SecretSnapshot, error) {
		secrets, err := env.Kube.Secrets(ctx, ns)
		if err != nil {
			return nil, err
		}
		return env.Hasher.Snapshot(secrets), nil
	}
	installed, err := snapshot()
	if err != nil {
		return inconclusive(res, "could not read Secrets after install", err)
	}

	// Control: same version again.
	if err := env.Helm.Upgrade(ctx, release, ns, from, env.Values, "--version", env.FromVersion); err != nil {
		return inconclusive(res, "upgrading the earlier version to itself failed", err)
	}
	control, err := snapshot()
	if err != nil {
		return inconclusive(res, "could not read Secrets after the control upgrade", err)
	}
	podsBefore, err := env.Kube.Pods(ctx, ns)
	if err != nil {
		return inconclusive(res, "could not read pods", err)
	}

	// Treatment: the version under test.
	mark = len(env.Runner.Transcript())
	upgradeErr := env.Helm.Upgrade(ctx, release, ns, env.Chart, env.Values)
	repro = append(repro, reproLine(env.Runner.Transcript()[mark].Line))
	if upgradeErr != nil {
		res.Verdict = Fail
		res.Summary = "the upgrade from " + env.FromVersion + " fails"
		res.Findings = append(res.Findings, Finding{
			ID:       "HB-CU-001",
			Title:    "Upgrading from " + env.FromVersion + " fails",
			Severity: "high",
			Evidence: []string{"`helm upgrade` failed: " + helmMessage(upgradeErr)},
			Impact: "Users on " + env.FromVersion + " cannot upgrade with their existing values. A " +
				"failed upgrade can leave the release in a failed state that needs a manual rollback.",
			Fix: "If the error names an immutable field (selector, StatefulSet volumeClaimTemplates or " +
				"serviceName), keep it unchanged or document the migration (for example deleting the " +
				"StatefulSet with --cascade=orphan). If it is a values or schema error, accept the old " +
				"value or document the change in the release notes.",
			Reproduce: repro,
		})
		return res
	}

	upgraded, err := snapshot()
	if err != nil {
		return inconclusive(res, "could not read Secrets after the upgrade", err)
	}
	consumers, err := env.Kube.SecretConsumers(ctx, ns)
	if err != nil {
		fmt.Fprintf(env.Log, "could not map Secret consumers: %v\n", err)
	}

	regenerated := oracle.Diff(installed, control)
	if changes := withoutRefs(oracle.Diff(control, upgraded), regenerated); len(changes) > 0 {
		res.Findings = append(res.Findings, Finding{
			ID:       "HB-CU-002",
			Title:    "The new version changes or drops Secret values of an existing release",
			Severity: "high",
			Evidence: describe(changes, consumers),
			Impact: "After the upgrade the application gets different credentials or keys than the " +
				"ones its data was created with, or loses them.",
			Fix: "Keep the Secret names and keys of earlier versions, or migrate the stored value " +
				"(read the old Secret with `lookup` and carry it over) and say so in the release notes.",
			Reproduce: repro,
		})
	}

	if unhealthy := unhealthyAfter(ctx, env, podsBefore); len(unhealthy) > 0 {
		res.Findings = append(res.Findings, Finding{
			ID:        "HB-CU-003",
			Title:     "Workloads are not healthy after the upgrade",
			Severity:  "high",
			Evidence:  unhealthy,
			Impact:    "The upgrade completed, but parts of the application are not ready or keep restarting.",
			Fix:       "Read the pod logs and events in the failure bundle; typical causes are a removed setting the app still needs, a migration that fails, or a changed port or probe.",
			Reproduce: repro,
		})
	}

	if len(regenerated) > 0 {
		fmt.Fprintf(env.Log, "note: %d Secret key(s) change on every upgrade of %s; see upgrade-without-values\n", len(regenerated), env.FromVersion)
	}
	if len(res.Findings) > 0 {
		res.Verdict = Fail
		res.Summary = fmt.Sprintf("%d problem(s) upgrading from %s", len(res.Findings), env.FromVersion)
	} else {
		res.Verdict = Pass
		res.Summary = "upgraded from " + env.FromVersion + " with Secrets kept and workloads healthy"
	}
	return res
}

// unhealthyAfter waits for the release to settle and lists pods that are not ready or restarted
// more than before the upgrade. Pods of a Deployment get a new ReplicaSet on upgrade, so restarts
// are only compared for owners that keep their name (StatefulSets, DaemonSets).
func unhealthyAfter(ctx context.Context, env *Env, before []kube.Pod) []string {
	restartsBefore := map[string]int{}
	for _, p := range before {
		restartsBefore[p.Owner] += p.Restarts
	}
	var problems []string
	deadline := time.Now().Add(90 * time.Second)
	for {
		pods, err := env.Kube.Pods(ctx, chartUpgradeNamespace)
		if err != nil {
			return []string{"could not read pods: " + err.Error()}
		}
		problems = problems[:0]
		restarts := map[string]int{}
		for _, p := range pods {
			restarts[p.Owner] += p.Restarts
			if !p.Ready {
				problems = append(problems, fmt.Sprintf("pod `%s` (%s) is not ready", p.Name, p.Owner))
			}
		}
		for owner, n := range restarts {
			if n > restartsBefore[owner] && !strings.HasPrefix(owner, "ReplicaSet/") {
				problems = append(problems, fmt.Sprintf("`%s` restarted %d more time(s) after the upgrade", owner, n-restartsBefore[owner]))
			}
		}
		if len(problems) == 0 || time.Now().After(deadline) {
			sort.Strings(problems)
			return problems
		}
		select {
		case <-ctx.Done():
			return problems
		case <-time.After(10 * time.Second):
		}
	}
}

// helmMessage keeps the part of a helm error that explains it, without the command line.
func helmMessage(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, "Error: "); i >= 0 {
		msg = msg[i+len("Error: "):]
	}
	if len(msg) > 600 {
		msg = msg[:600] + "…"
	}
	return msg
}
