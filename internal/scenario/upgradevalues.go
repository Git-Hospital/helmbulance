package scenario

import (
	"context"
	"fmt"
	"strings"

	"github.com/egezamb/helmbulance/internal/execx"
	"github.com/egezamb/helmbulance/internal/helm"
	"github.com/egezamb/helmbulance/internal/oracle"
)

// UpgradeValues checks what happens to Secrets when the release is upgraded the way operators
// really upgrade it.
//
// Helm reuses the previous release's values when an upgrade gets no values flags at all, so that
// upgrade is the control: nothing should change. Then it upgrades with only the values files,
// leaving out every --set given at install, which is what happens when someone upgrades months
// later from the values file in git. A Secret key that changes or disappears there means the chart
// does not keep a value once it has been set.
type UpgradeValues struct{}

const upgradeNamespace = "hb-upgrade-values"

func (UpgradeValues) Name() string { return "upgrade-without-values" }

func (UpgradeValues) Description() string {
	return "Upgrades with reused values (control), then with the values files only, and compares every Secret."
}

func (s UpgradeValues) Run(ctx context.Context, env *Env) (res Result) {
	res.Scenario = s.Name()
	const release, ns = "hb-upgrade", upgradeNamespace
	defer func() {
		if err := env.Helm.Uninstall(ctx, release, ns); err != nil {
			fmt.Fprintf(env.Log, "cleanup: %v\n", err)
		}
		if err := env.Kube.DeleteNamespace(ctx, ns); err != nil {
			fmt.Fprintf(env.Log, "cleanup: %v\n", err)
		}
	}()

	start := len(env.Runner.Transcript())
	if err := env.Helm.Install(ctx, release, ns, env.Chart, env.Values); err != nil {
		return inconclusive(res, "the chart did not install", err)
	}
	installCmd := env.Runner.Transcript()[start:]

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

	// Control: Helm reuses the install values.
	mark := len(env.Runner.Transcript())
	if err := env.Helm.Upgrade(ctx, release, ns, env.Chart, helm.Values{}); err != nil {
		return inconclusive(res, "the upgrade with reused values failed", err)
	}
	reuseCmd := env.Runner.Transcript()[mark:]
	reused, err := snapshot()
	if err != nil {
		return inconclusive(res, "could not read Secrets after the control upgrade", err)
	}

	// Treatment: values files only. Without any --set or values file it would repeat the control.
	if len(env.Values.Sets) == 0 && len(env.Values.Files) == 0 {
		return s.verdict(ctx, res, installed, reused, nil, installCmd, reuseCmd, nil, env)
	}
	mark = len(env.Runner.Transcript())
	// --reset-values: without any values flag Helm would reuse the install values, --set included.
	if err := env.Helm.Upgrade(ctx, release, ns, env.Chart, helm.Values{Files: env.Values.Files}, "--reset-values"); err != nil {
		return inconclusive(res, "the upgrade with the values files failed", err)
	}
	filesCmd := env.Runner.Transcript()[mark:]
	filesOnly, err := snapshot()
	if err != nil {
		return inconclusive(res, "could not read Secrets after the upgrade", err)
	}

	return s.verdict(ctx, res, installed, reused, filesOnly, installCmd, reuseCmd, filesCmd, env)
}

func (s UpgradeValues) verdict(ctx context.Context, res Result, installed, reused, filesOnly oracle.SecretSnapshot,
	installCmd, reuseCmd, filesCmd []execx.Command, env *Env) Result {
	consumers, err := env.Kube.SecretConsumers(ctx, upgradeNamespace)
	if err != nil {
		fmt.Fprintf(env.Log, "could not map Secret consumers: %v\n", err)
	}

	var repro []string
	for _, c := range installCmd {
		repro = append(repro, reproLine(c.Line))
	}

	control := oracle.Diff(installed, reused)
	if len(control) > 0 {
		steps := concat(repro, reproLine(reuseCmd[0].Line))
		res.Findings = append(res.Findings, Finding{
			ID:       "HB-UV-001",
			Title:    "Secrets change on every upgrade, even with the same values",
			Severity: "high",
			Evidence: describe(control, consumers),
			Impact: "Every `helm upgrade` replaces these values. Passwords that a database stored at " +
				"its first start stop matching, and anything encrypted or signed with the old value " +
				"can no longer be read.",
			Fix: "Generate the value only when it does not exist yet: read the current Secret with " +
				"`lookup` and reuse its value, and add `helm.sh/resource-policy: keep`. `lookup` " +
				"returns nothing under `helm template` (Argo CD), so also support an existingSecret.",
			Reproduce: steps,
		})
	}

	// Keys that already changed in the control are reported there, not again here.
	var changes []oracle.Change
	if filesOnly != nil {
		changes = withoutRefs(oracle.Diff(reused, filesOnly), control)
	}
	if len(changes) > 0 {
		steps := concat(repro, reproLine(filesCmd[0].Line))
		title := "Secret values are lost when an upgrade leaves out an install-time --set"
		if len(env.Values.Sets) == 0 {
			title = "Secret values change when the release is upgraded from its values files"
		}
		res.Findings = append(res.Findings, Finding{
			ID:       "HB-UV-002",
			Title:    title,
			Severity: "high",
			Evidence: describe(changes, consumers),
			Impact: "Upgrading from the values file in git, without repeating every --set used at " +
				"install, silently replaces or removes these values. For an encryption key this " +
				"means new data is written with a different key, or none, and old data cannot be read.",
			Fix: "Once a value is in use, keep it: fall back to the value stored in the Secret (via " +
				"`lookup`) when values no longer provide it, and `fail` the render when a new value " +
				"would make existing data unreadable.",
			Reproduce: steps,
		})
	}

	if len(res.Findings) > 0 {
		res.Verdict = Fail
		res.Summary = fmt.Sprintf("%d problem(s) with Secrets across upgrades", len(res.Findings))
	} else {
		res.Verdict = Pass
		res.Summary = "Secrets stayed the same through both upgrades"
	}
	return res
}

func describe(changes []oracle.Change, consumers map[string][]string) []string {
	var lines []string
	for _, c := range changes {
		line := fmt.Sprintf("`%s` %s", c.Ref, c.Kind)
		secret, _, _ := strings.Cut(c.Ref, "/")
		users := append(append([]string{}, consumers[c.Ref]...), consumers[secret+"/*"]...)
		if len(users) > 0 {
			line += " (read by " + strings.Join(users, ", ") + ")"
		}
		lines = append(lines, line)
	}
	return lines
}

func inconclusive(res Result, why string, err error) Result {
	res.Verdict = Inconclusive
	res.Summary = why + ": " + err.Error()
	return res
}

// concat returns a new slice, so findings never share a backing array.
func concat(base []string, more ...string) []string {
	return append(append([]string{}, base...), more...)
}

// withoutRefs drops changes whose key is already in seen.
func withoutRefs(changes, seen []oracle.Change) []oracle.Change {
	skip := map[string]bool{}
	for _, c := range seen {
		skip[c.Ref] = true
	}
	var out []oracle.Change
	for _, c := range changes {
		if !skip[c.Ref] {
			out = append(out, c)
		}
	}
	return out
}

// reproLine replaces the sandbox kubeconfig path, so the command reads as one to run against any
// test cluster.
func reproLine(line string) string {
	fields := strings.Fields(line)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "--kubeconfig" {
			fields[i+1] = "$KUBECONFIG"
		}
	}
	return strings.Join(fields, " ")
}
