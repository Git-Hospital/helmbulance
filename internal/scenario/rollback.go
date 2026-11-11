package scenario

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Git-Hospital/helmbulance/internal/execx"
	"github.com/Git-Hospital/helmbulance/internal/kube"
	"github.com/Git-Hospital/helmbulance/internal/oracle"
)

// Rollback installs an earlier version of the chart, upgrades it to the chart under test and rolls
// it back, the way users undo a bad upgrade (#130).
//
// Control: the earlier version upgraded to itself and rolled back. Secrets and pods that change
// there change on every rollback, whatever the version, and are not blamed on the chart under
// test; a control rollback that fails proves nothing about the way back (D4). Treatment: the
// upgrade to the chart under test, then a rollback to the control's revision. It reports a rollback
// that fails (HB-RB-001) and one that succeeds but leaves the earlier version unhealthy or with
// different Secrets (HB-RB-002).
type Rollback struct{}

const rollbackNamespace = "hb-rollback"

func (Rollback) Name() string { return "rollback" }

func (Rollback) Description() string {
	return "Installs an earlier chart version (--from-version), upgrades to the chart under test, rolls back, and checks the earlier version comes back healthy with the same Secrets."
}

func (s Rollback) Run(ctx context.Context, env *Env) (res Result) {
	res.Scenario = s.Name()
	if env.FromVersion == "" {
		res.Verdict = Skipped
		res.Summary = "no earlier version: give --from-version to say which version users roll back to"
		return res
	}
	from := env.From
	if from == "" {
		from = env.Chart
	}
	const release, ns = "hb-rollback", rollbackNamespace
	defer func() {
		ctx, cancel := cleanupContext(ctx)
		defer cancel()
		if err := env.Helm.Uninstall(ctx, release, ns); err != nil {
			fmt.Fprintf(env.Log, "cleanup: %v\n", err)
		}
		if err := env.Kube.DeleteNamespace(ctx, ns); err != nil {
			fmt.Fprintf(env.Log, "cleanup: %v\n", err)
		}
	}()

	if err := startClean(ctx, env, release, ns); err != nil {
		return inconclusive(res, "could not clear what an earlier run left", err)
	}
	mark := len(env.Runner.Transcript())
	if err := env.Helm.Install(ctx, release, ns, from, env.Values, "--version", env.FromVersion); err != nil {
		return inconclusive(res, "the earlier version did not install", err)
	}
	repro := []string{execx.ReproLine(env.Runner.Transcript()[mark].Line)}
	fromV, err := installedFrom(ctx, env, release, ns)
	if err != nil {
		return inconclusive(res, "could not confirm the earlier version", err)
	}
	secrets := newSecretReader(ctx, env, release, ns)
	installed, err := secrets.snapshot()
	if err != nil {
		return secretsInconclusive(res, "after install", err)
	}

	// Control: the earlier version upgraded to itself, then rolled back to the install.
	mark = len(env.Runner.Transcript())
	if err := env.Helm.Upgrade(ctx, release, ns, from, env.Values, "--version", env.FromVersion); err != nil {
		return inconclusive(res, "upgrading the earlier version to itself failed", err)
	}
	repro = append(repro, execx.ReproLine(env.Runner.Transcript()[mark].Line))
	mark = len(env.Runner.Transcript())
	if err := env.Helm.Rollback(ctx, release, ns, 1); err != nil {
		return inconclusive(res, "rolling the earlier version back to itself failed, so a rollback proves nothing here", err)
	}
	repro = append(repro, execx.ReproLine(env.Runner.Transcript()[mark].Line))
	controlRev, err := latestRevision(ctx, env, release, ns)
	if err != nil {
		return inconclusive(res, "could not read the release history", err)
	}
	control, err := secrets.snapshot()
	if err != nil {
		return secretsInconclusive(res, "after the control rollback", err)
	}
	podsBefore, err := env.Kube.Pods(ctx, ns)
	if err != nil {
		return inconclusive(res, "could not read pods", err)
	}
	jobsBefore, err := env.Kube.FailedJobs(ctx, ns)
	if err != nil {
		return inconclusive(res, "could not read Jobs", err)
	}
	containersBefore, err := env.Kube.Containers(ctx, ns)
	containersKnown := err == nil
	if err != nil {
		fmt.Fprintf(env.Log, "could not read containers before the upgrade: %v\n", err)
	}

	// Treatment: the version under test, then back.
	mark = len(env.Runner.Transcript())
	if err := env.Helm.Upgrade(ctx, release, ns, env.Chart, env.Values); err != nil {
		// chart-upgrade reports a failed upgrade (HB-CU-001). What Helm's automatic rollback
		// does then is HB-RB-003, not built yet.
		return inconclusive(res, "the upgrade to the chart under test failed, so there was nothing to roll back (chart-upgrade reports it)", err)
	}
	repro = append(repro, execx.ReproLine(env.Runner.Transcript()[mark].Line))
	back := "the rollback to " + fromV
	if toV, err := env.Helm.ReleaseVersion(ctx, release, ns); err == nil {
		back = "the rollback from " + toV + " to " + fromV
	}
	mark = len(env.Runner.Transcript())
	rollbackErr := env.Helm.Rollback(ctx, release, ns, controlRev)
	repro = append(repro, execx.ReproLine(env.Runner.Transcript()[mark].Line))

	if rollbackErr != nil {
		// A rollback whose --wait ran out while the earlier version's containers crash went
		// through, and came back broken: HB-RB-002 with the containers as evidence (#28).
		if timedOutWaiting(ctx, rollbackErr) {
			hookJobs, hookErr := env.Helm.HookJobs(ctx, release, ns)
			if hookErr != nil {
				fmt.Fprintf(env.Log, "could not identify rollback hook Jobs: %v\n", hookErr)
			}
			var crashes, hookCrashes []string
			if hookErr == nil {
				crashes = ReadCrashes(ctx, env.Kube, ns, env.Log, func(containers []kube.Container) []string {
					hooks := slices.DeleteFunc(slices.Clone(containers), func(c kube.Container) bool { return !hookJobs[c.Owner] })
					hookCrashes = crashedSince(hooks, containersBefore, podsBefore, containersKnown)
					if len(hookCrashes) > 0 {
						return hookCrashes
					}
					containers = slices.DeleteFunc(containers, func(c kube.Container) bool { return hookJobs[c.Owner] })
					return crashedSince(containers, containersBefore, podsBefore, containersKnown)
				})
			}
			if len(hookCrashes) > 0 {
				return rollbackFails(res, back, repro, append([]string{
					"`helm rollback` did not finish while a hook Job crashed: " + helmMessage(rollbackErr),
				}, hookCrashes...))
			}
			if len(crashes) > 0 {
				return rolledBackBroken(res, back, repro, append([]string{
					"`helm rollback --wait` timed out while containers of " + fromV + " crashed or could not start: " + helmMessage(rollbackErr),
				}, crashes...), nil, nil)
			}
		}
		if IsEnvironmentError(ctx, rollbackErr) {
			return inconclusive(res, back+" did not finish", rollbackErr)
		}
		return rollbackFails(res, back, repro, []string{"`helm rollback` failed: " + helmMessage(rollbackErr)})
	}

	// The rollback said it worked: the release must be deployed again, at the earlier version.
	revs, err := env.Helm.History(ctx, release, ns)
	if err != nil {
		return inconclusive(res, "could not read the release history after "+back, err)
	}
	last := revs[len(revs)-1]
	// helm history prints the chart as name-version.
	atFrom := strings.HasSuffix(last.Chart, "-"+fromV) || strings.HasSuffix(last.Chart, "-"+strings.TrimPrefix(fromV, "v"))
	if last.Status != "deployed" || !atFrom {
		return rollbackFails(res, back, repro, []string{fmt.Sprintf(
			"`helm rollback` returned success, but revision %d is %s with chart %s, not deployed at %s", last.Revision, last.Status, last.Chart, fromV)})
	}

	podsBack, err := env.Kube.Pods(ctx, ns)
	if err != nil {
		fmt.Fprintf(env.Log, "could not read pods right after the rollback: %v\n", err)
	}
	after, err := secrets.snapshot()
	if err != nil {
		return secretsInconclusive(res, "after "+back, err)
	}
	consumers, err := env.Kube.SecretConsumers(ctx, ns)
	if err != nil {
		fmt.Fprintf(env.Log, "could not map Secret consumers: %v\n", err)
	}
	stateful, err := env.Kube.StatefulWorkloads(ctx, ns)
	if err != nil {
		fmt.Fprintf(env.Log, "could not find stateful workloads: %v\n", err)
	}
	statefulKnown := err == nil

	// Keys that already changed in the control change on every rollback, whatever the version.
	regenerated := oracle.Diff(installed, control)
	changes, added := splitAdded(withoutRefs(oracle.Diff(control, after), regenerated))
	changes, rotated := splitExpected(changes, env.ExpectedRotation)
	unhealthy, healthErr := unhealthyAfter(ctx, env, ns, podsBefore, podsBack, jobsBefore)

	if len(unhealthy) > 0 || len(changes) > 0 {
		res = rolledBackBroken(res, back, repro, unhealthy, changes, func() (string, []string) {
			return worstSeverity(changes, consumers, stateful, statefulKnown), describe(changes, consumers)
		})
	} else {
		res.Verdict = Pass
		res.Summary = back + " left the release deployed, healthy and with the Secrets it had"
		if noKeys(installed, control, after) {
			res.Summary = back + " left the release deployed and healthy; " + noSecretsTested
		}
	}
	res.Summary = withNotes(res.Summary,
		note("keys the rollback added, not a finding", describe(added, consumers)),
		note("not compared, since they change on every rollback of "+fromV+" too", describe(regenerated, consumers)),
		note("rotated as expected (--expected-rotation)", describe(rotated, consumers)),
		unknownStatefulNote(statefulKnown, changes),
		secrets.notComparedNote())
	if healthErr != nil {
		return healthUnread(res, healthErr)
	}
	return res
}

// latestRevision is the number of the release's newest revision.
func latestRevision(ctx context.Context, env *Env, release, ns string) (int, error) {
	revs, err := env.Helm.History(ctx, release, ns)
	if err != nil {
		return 0, err
	}
	return revs[len(revs)-1].Revision, nil
}

// rollbackFails is the HB-RB-001 result: the rollback to the earlier version did not succeed.
func rollbackFails(res Result, back string, repro, evidence []string) Result {
	res.Verdict = Fail
	res.Summary = back + " fails"
	res.Findings = append(res.Findings, Finding{
		ID:       "HB-RB-001",
		Title:    strings.ToUpper(back[:1]) + back[1:] + " fails",
		Severity: "high",
		Evidence: evidence,
		Impact: "A user whose upgrade went wrong cannot go back with `helm rollback`. The release is " +
			"left failed, at the version that did not work.",
		Fix: "Make the rollback hooks work after the new version ran: a pre-rollback hook that " +
			"refuses data the new version wrote should migrate it back, or say how to. If a field " +
			"conflict is named, drop or relax the field the new version took over.",
		Reproduce: repro,
	})
	return res
}

// rolledBackBroken is the HB-RB-002 result: the rollback ran, but the earlier version came back
// unhealthy, or with Secret values that differ from before the upgrade. secretRating gives the
// severity and evidence of changed keys; nil when only health is reported.
func rolledBackBroken(res Result, back string, repro, unhealthy []string, changes []oracle.Change, secretRating func() (string, []string)) Result {
	severity := "high"
	evidence := append([]string{}, unhealthy...)
	if len(changes) > 0 && secretRating != nil {
		s, lines := secretRating()
		if len(unhealthy) == 0 {
			severity = s
		}
		evidence = append(evidence, lines...)
	}
	res.Verdict = Fail
	res.Summary = back + " comes back broken"
	res.Findings = append(res.Findings, Finding{
		ID:       "HB-RB-002",
		Title:    "The earlier version does not come back as it was after a rollback",
		Severity: severity,
		Evidence: evidence,
		Impact: "After `helm rollback`, the earlier version does not run as it did " +
			"before the upgrade: it cannot read what the new version left behind, or its Secrets " +
			"are not the ones its data was set up with.",
		Fix: "Keep data that the new version writes readable by the version before it, or gate the " +
			"migration until the upgrade is confirmed. Render generated Secrets with `lookup`, so a " +
			"rollback does not bring back different values.",
		Reproduce: repro,
	})
	return res
}
