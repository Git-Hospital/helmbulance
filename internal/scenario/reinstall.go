package scenario

import (
	"context"
	"fmt"
	"strings"

	"github.com/egezamb/helmbulance/internal/oracle"
)

// Reinstall checks what comes back after `helm uninstall` and `helm install` with the same values,
// the way operators recover a broken release. It writes a sentinel into every volume, reinstalls,
// and compares the volumes and the release's Secrets. The dangerous case is data that survives
// while the credentials it was written with do not.
type Reinstall struct{}

const reinstallNamespace = "hb-reinstall"

func (Reinstall) Name() string { return "reinstall" }

func (Reinstall) Description() string {
	return "Uninstalls and installs again with the same values, and checks volumes and Secrets."
}

// volume is one PersistentVolumeClaim and what happened to its sentinel.
type volume struct {
	claim, uid, value string
	// state is "kept", "missing" or "replaced" after the reinstall; "deleted" when the claim went
	// away with the release, and "recreated" when the reinstall made a new claim with the same
	// name; "unwritable" when no sentinel could be written.
	state string
}

func (s Reinstall) Run(ctx context.Context, env *Env) (res Result) {
	res.Scenario = s.Name()
	const release, ns = "hb-reinstall", reinstallNamespace
	defer func() {
		if err := env.Helm.Uninstall(ctx, release, ns); err != nil {
			fmt.Fprintf(env.Log, "cleanup: %v\n", err)
		}
		if err := env.Kube.DeleteNamespace(ctx, ns); err != nil {
			fmt.Fprintf(env.Log, "cleanup: %v\n", err)
		}
	}()

	mark := len(env.Runner.Transcript())
	if err := env.Helm.Install(ctx, release, ns, env.Chart, env.Values); err != nil {
		return inconclusive(res, "the chart did not install", err)
	}
	var repro []string
	for _, c := range env.Runner.Transcript()[mark:] {
		repro = append(repro, reproLine(c.Line))
	}

	secrets := newSecretReader(ctx, env, release, ns)
	before, err := secrets.snapshot()
	if err != nil {
		return secretsInconclusive(res, "after install", err)
	}
	volumes, err := s.writeSentinels(ctx, env, ns)
	if err != nil {
		return inconclusive(res, "could not write the sentinels", err)
	}

	mark = len(env.Runner.Transcript())
	if err := env.Helm.Uninstall(ctx, release, ns); err != nil {
		return inconclusive(res, "the uninstall failed", err)
	}
	if err := env.Helm.Install(ctx, release, ns, env.Chart, env.Values); err != nil {
		return s.installFailed(ctx, res, err, concat(repro, transcriptRepro(env, mark)...))
	}
	repro = concat(repro, transcriptRepro(env, mark)...)

	after, err := secrets.snapshot()
	if err != nil {
		return secretsInconclusive(res, "after the reinstall", err)
	}
	if err := s.readSentinels(ctx, env, ns, volumes); err != nil {
		return inconclusive(res, "could not read the sentinels", err)
	}

	consumers, err := env.Kube.SecretConsumers(ctx, ns)
	if err != nil {
		fmt.Fprintf(env.Log, "could not map Secret consumers: %v\n", err)
	}
	stateful, err := env.Kube.StatefulWorkloads(ctx, ns)
	if err != nil {
		fmt.Fprintf(env.Log, "could not find stateful workloads: %v\n", err)
	}
	changes, added := splitAdded(oracle.Diff(before, after))
	changes, rotated := splitExpected(changes, env.ExpectedRotation)
	res.Findings = reinstallFindings(volumes, changes, consumers, stateful, repro)

	if len(res.Findings) > 0 {
		res.Verdict = Fail
		res.Summary = fmt.Sprintf("%d problem(s) after uninstall and install", len(res.Findings))
	} else {
		res.Verdict = Pass
		res.Summary = reinstallPassSummary(volumes, changes)
	}
	res.Summary = withNotes(res.Summary,
		note("keys added by the reinstall, not a finding", describe(added, consumers)),
		note("rotated as expected (--expected-rotation)", describe(rotated, consumers)),
		note("no sentinel could be written to", claimsIn(volumes, "unwritable")),
		secrets.notComparedNote())
	return res
}

// writeSentinels writes a fresh sentinel into every PersistentVolumeClaim in ns.
func (Reinstall) writeSentinels(ctx context.Context, env *Env, ns string) ([]volume, error) {
	claims, err := env.Kube.PVCs(ctx, ns)
	if err != nil {
		return nil, err
	}
	var volumes []volume
	for i, claim := range claims {
		value, err := newSentinel()
		if err != nil {
			return nil, err
		}
		out, err := env.Kube.RunWithPVC(ctx, ns, fmt.Sprintf("hb-sentinel-w%d", i), sentinelImage, claim.Name,
			writeSentinelScript(value), env.Helm.Timeout.String())
		if err != nil {
			return nil, err
		}
		v := volume{claim: claim.Name, uid: claim.UID, value: value}
		if strings.TrimSpace(out) != "written" {
			v.state = "unwritable"
		}
		volumes = append(volumes, v)
	}
	return volumes, nil
}

// readSentinels sets each written volume's state after the reinstall.
func (Reinstall) readSentinels(ctx context.Context, env *Env, ns string, volumes []volume) error {
	claims, err := env.Kube.PVCs(ctx, ns)
	if err != nil {
		return err
	}
	uids := map[string]string{}
	for _, c := range claims {
		uids[c.Name] = c.UID
	}
	for i := range volumes {
		v := &volumes[i]
		uid, present := uids[v.claim]
		switch {
		case v.state == "unwritable":
			continue
		case !present:
			v.state = "deleted"
			continue
		case uid != v.uid:
			// helm uninstall deleted the claim and the reinstall made a new, empty one.
			v.state = "recreated"
			continue
		}
		out, err := env.Kube.RunWithPVC(ctx, ns, fmt.Sprintf("hb-sentinel-r%d", i), sentinelImage, v.claim,
			readSentinelScript, env.Helm.Timeout.String())
		if err != nil {
			return err
		}
		v.state = sentinelState(out, v.value)
	}
	return nil
}

// installFailed is the result when the second install fails. A timeout says more about the run
// than the chart; anything else is a release that cannot be recreated.
func (Reinstall) installFailed(ctx context.Context, res Result, err error, repro []string) Result {
	msg := strings.ToLower(err.Error())
	if ctx.Err() != nil || strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "timed out waiting") {
		return inconclusive(res, "the reinstall did not finish", err)
	}
	res.Verdict = Fail
	res.Summary = "the release does not install again after an uninstall"
	res.Findings = []Finding{{
		ID:       "HB-RI-003",
		Title:    "The release does not install again after `helm uninstall`",
		Severity: "high",
		Evidence: []string{"The second `helm install` failed: " + helmMessage(err)},
		Impact: "Reinstalling is the usual way out of a broken release. Here it fails, often because " +
			"something kept from the first install (a Secret with `helm.sh/resource-policy: keep`, a " +
			"cluster-scoped object) conflicts with the new one.",
		Fix: "Make kept objects adoptable by the same release (keep Helm's ownership labels and " +
			"annotations), or document the manual cleanup before a reinstall.",
		Reproduce: repro,
	}}
	return res
}

// reinstallFindings turns the volumes and the Secret changes into findings.
func reinstallFindings(volumes []volume, changes []oracle.Change, consumers map[string][]string,
	stateful map[string]bool, repro []string) []Finding {
	var findings []Finding
	kept := claimsIn(volumes, "kept")
	if len(kept) > 0 && len(changes) > 0 {
		evidence := []string{"Volumes that kept their data: " + strings.Join(kept, ", ") + "."}
		findings = append(findings, Finding{
			ID:       "HB-RI-001",
			Title:    "Data survives a reinstall, but the credentials it was written with do not",
			Severity: worstSeverity(changes, consumers, stateful),
			Evidence: append(evidence, describe(changes, consumers)...),
			Impact: "After `helm uninstall` and `helm install` the volumes still hold the old data, " +
				"but the chart generated new credentials. A database initialised with the old " +
				"password refuses the new one, and data encrypted with an old key cannot be read.",
			Fix: "Keep generated credentials across a reinstall: add `helm.sh/resource-policy: keep` " +
				"to the Secret and reuse its value with `lookup`, or require an existingSecret for " +
				"anything a volume depends on.",
			Reproduce: repro,
		})
	}
	if lost := append(claimsIn(volumes, "missing"), claimsIn(volumes, "replaced")...); len(lost) > 0 {
		findings = append(findings, Finding{
			ID:       "HB-RI-002",
			Title:    "A volume that survives the reinstall comes back without its data",
			Severity: "medium",
			Evidence: []string{"The sentinel written before the uninstall is gone from: " + strings.Join(lost, ", ") + "."},
			Impact: "The claim was kept, so users expect their data back, but something emptied or " +
				"replaced it during the reinstall, such as an init container or a job that formats it.",
			Fix:       "Only initialise a volume that is empty, and never on an install that finds existing data.",
			Reproduce: repro,
		})
	}
	return findings
}

// reinstallPassSummary says what was checked, including volumes deleted with the release, which
// depend on the StorageClass and the chart and are not a finding on their own.
func reinstallPassSummary(volumes []volume, changes []oracle.Change) string {
	summary := "Secrets came back unchanged after uninstall and install"
	switch {
	case len(volumes) == 0 && len(changes) > 0:
		return "the release has no volumes, so new Secrets after a reinstall lose nothing"
	case len(changes) > 0:
		summary = "no volume kept its data, so new Secrets are expected"
	}
	if kept := claimsIn(volumes, "kept"); len(kept) > 0 {
		summary += fmt.Sprintf("; %d volume(s) kept their data", len(kept))
	}
	if deleted := append(claimsIn(volumes, "deleted"), claimsIn(volumes, "recreated")...); len(deleted) > 0 {
		summary += "; deleted with the release (depends on the StorageClass reclaim policy): " + strings.Join(deleted, ", ")
	}
	if len(volumes) == 0 {
		summary += "; the release has no volumes"
	}
	return summary
}

func claimsIn(volumes []volume, state string) []string {
	var claims []string
	for _, v := range volumes {
		if v.state == state {
			claims = append(claims, "`"+v.claim+"`")
		}
	}
	return claims
}

// transcriptRepro returns the reproduction lines of the commands run since mark.
func transcriptRepro(env *Env, mark int) []string {
	var lines []string
	for _, c := range env.Runner.Transcript()[mark:] {
		lines = append(lines, reproLine(c.Line))
	}
	return lines
}
