package scenario

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egezamb/helmbulance/internal/helm"
	"github.com/egezamb/helmbulance/internal/oracle"
)

const gitopsRenderNamespace = "hb-gitops-render"

// GitopsRender installs the chart, then renders it the way a GitOps tool does: `helm template`,
// with no cluster, so `lookup` returns nothing. A Secret value that Helm keeps (the server-side
// render matches the release) but the client-side render changes is overwritten on every sync.
type GitopsRender struct{}

func (GitopsRender) Name() string { return "gitops-render" }

func (GitopsRender) Description() string {
	return "Renders the chart without a cluster, as Argo CD does, and finds Secrets that would change on every sync."
}

func (s GitopsRender) Run(ctx context.Context, env *Env) (res Result) {
	res.Scenario = s.Name()
	const release, ns = "hb-gitops", gitopsRenderNamespace
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

	mark := len(env.Runner.Transcript())
	if err := env.Helm.Install(ctx, release, ns, env.Chart, env.Values); err != nil {
		return inconclusive(res, "the chart did not install", err)
	}
	secrets := newSecretReader(ctx, env, release, ns)
	live, err := secrets.snapshot()
	if err != nil {
		return secretsInconclusive(res, "after install", err)
	}
	if len(live) == 0 {
		res.Verdict = Skipped
		res.Summary = "the release has no Secrets, so there is nothing a client-side render could change"
		return res
	}

	// The same values, rendered with the cluster as Helm upgrades the release, and twice without it
	// as Argo CD does. Argo CD never passes --is-upgrade, so its render is always an install, while
	// Helm keeps what only an install renders: a `.Release.IsInstall` Secret with keep, or one an
	// install-only hook made (#45).
	var hooks map[string]string
	render := func(server bool) (oracle.SecretSnapshot, error) {
		if !server {
			out, err := env.Helm.Template(ctx, release, ns, env.Chart, env.Values, false)
			if err != nil {
				return nil, err
			}
			hooks = helm.InstallOnlyHooks(out, ns)
			return env.Hasher.Snapshot(helm.RenderedSecrets(out, ns)), nil
		}
		out, err := env.Helm.Template(ctx, release, ns, env.Chart, env.Values, true, "--is-upgrade")
		if err != nil {
			return nil, err
		}
		return env.Hasher.Snapshot(helm.UpgradedSecrets(out, ns)), nil
	}
	server, err := render(true)
	if err != nil {
		return inconclusive(res, "the server-side render failed", err)
	}
	client1, err := render(false)
	if err != nil {
		return inconclusive(res, "the client-side render failed", err)
	}
	client2, err := render(false)
	if err != nil {
		return inconclusive(res, "the client-side render failed", err)
	}
	var repro []string
	for _, c := range env.Runner.Transcript()[mark:] {
		if strings.Contains(c.Line, " install ") || (strings.Contains(c.Line, " template ") && !strings.Contains(c.Line, "--dry-run=server")) {
			repro = append(repro, reproLine(c.Line))
		}
	}

	changes, everySync, unchanged, regenerated := gitopsDrift(live, server, client1, client2)
	changes, expected := splitExpected(changes, env.ExpectedRotation)
	if len(changes) == 0 {
		res.Verdict = Pass
		res.Summary = fmt.Sprintf("a render without a cluster gives the release's %d Secret value(s) unchanged", unchanged)
		if regenerated > 0 {
			res.Summary += fmt.Sprintf("; %d value(s) that Helm regenerates on upgrade too not compared (upgrade-without-values)", regenerated)
		}
		if len(expected) > 0 {
			res.Summary += fmt.Sprintf("; %d expected rotation(s) not reported", len(expected))
		}
		return res
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
	evidence := append([]string{"Rendered without a cluster (`helm template`), with the same values, these Secret values differ from the installed release, which Helm keeps (`helm template --dry-run=server --is-upgrade` matches it or leaves the Secret alone):"},
		describe(changes, consumers)...)
	if refs := slices.Sorted(maps.Keys(everySync)); len(refs) > 0 {
		evidence = append(evidence, "A new value on every client-side render, so on every sync: `"+strings.Join(refs, "`, `")+"`.")
	}
	hooked, other := hookSecrets(changes, hooks)
	for _, name := range hooked {
		evidence = append(evidence, fmt.Sprintf("`%s` is a `%s` hook: `helm upgrade` does not run it again, but Argo CD runs it on every sync.", name, hooks[name]))
	}
	// IsLocal, not IsPath: "testdata/charts/x" looks like repo/chart but is on disk.
	if helm.IsLocal(env.Chart) {
		evidence = append(evidence, lookupCalls(env.Chart)...)
	}
	res.Verdict = Fail
	res.Summary = fmt.Sprintf("%d Secret value(s) change when the chart is rendered without a cluster", len(changes))
	res.Findings = append(res.Findings, Finding{
		ID:        "HB-GR-001",
		Title:     "Secret values change when the chart is rendered without a cluster (GitOps)",
		Severity:  worstSeverity(changes, consumers, stateful, statefulKnown),
		Evidence:  evidence,
		Impact:    gitopsImpact(len(hooked) > 0),
		Fix:       gitopsFix(len(hooked) > 0, other),
		Reproduce: repro,
	})
	return res
}

// hookSecrets splits the Secrets of the changed keys into the install-only hooks and the rest.
func hookSecrets(changes []oracle.Change, hooks map[string]string) (hooked []string, other bool) {
	for _, c := range changes {
		name, _, _ := strings.Cut(c.Ref, "/")
		if _, ok := hooks[name]; !ok {
			other = true
		} else if !slices.Contains(hooked, name) {
			hooked = append(hooked, name)
		}
	}
	return hooked, other
}

func gitopsImpact(hooked bool) string {
	cause := "have no cluster to `lookup` in and render every sync as a first install"
	if hooked {
		cause += ", and Argo CD runs install hooks on every sync"
	}
	return "Argo CD and other tools that render with `helm template` " + cause + ", so each sync writes a " +
		"value the running release does not use. Pods restart on it, and a database that kept the first " +
		"password locks its clients out."
}

// gitopsFix leaves ignoreDifferences out for a hook: Argo CD applies hooks on every sync, outside
// the diff that ignoreDifferences filters.
func gitopsFix(hooked, other bool) string {
	if !hooked {
		return "Take the value from values or an existingSecret instead of generating it behind `lookup` or " +
			"`.Release.IsInstall`. If the chart must generate it, document that it needs `helm install` or " +
			"`upgrade`, and for Argo CD an ignoreDifferences entry for these keys."
	}
	fix := "Take the value from values or an existingSecret instead of generating it in an install hook. " +
		"An ignoreDifferences entry does not help there: Argo CD applies hooks on every sync, outside the diff."
	if other {
		fix += " For the keys outside a hook, a chart that must generate them can document that it needs " +
			"`helm install` or `upgrade`, and for Argo CD an ignoreDifferences entry for them."
	}
	return fix
}

// gitopsDrift lists the keys of the release (live) that a client-side render changes while Helm
// keeps them: the server-side render either gives the same value or leaves the key out, as charts
// that render a Secret only `if not (lookup …)` do. A key the server-side render changes too is
// regenerated under Helm as well, which upgrade-without-values reports, so it is left out here.
// everySync marks the keys whose two client-side renders differ from each other, unchanged counts
// the keys compared and found equal, and regenerated the keys left out because Helm changes them too.
// A key the client-side render leaves out is not compared.
func gitopsDrift(live, server, client1, client2 oracle.SecretSnapshot) (changes []oracle.Change, everySync map[string]bool, unchanged, regenerated int) {
	everySync = map[string]bool{}
	for _, ref := range slices.Sorted(maps.Keys(live)) {
		if s, ok := server[ref]; ok && s != live[ref] {
			regenerated++
			continue
		}
		c, ok := client1[ref]
		if !ok {
			continue
		}
		if c == live[ref] {
			unchanged++
			continue
		}
		changes = append(changes, oracle.Change{Ref: ref, Kind: oracle.Changed})
		if c2, ok := client2[ref]; ok && c2 != c {
			everySync[ref] = true
		}
	}
	return changes, everySync, unchanged, regenerated
}

var lookupCall = regexp.MustCompile(`\blookup\b`)

// lookupCalls lists where a local chart's templates (and its subcharts') call `lookup`, as
// "templates/secret.yaml:12" lines for the evidence, at most five.
func lookupCalls(chart string) []string {
	var lines []string
	_ = filepath.WalkDir(chart, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || len(lines) >= 5 {
			return nil
		}
		if ext := filepath.Ext(path); ext != ".yaml" && ext != ".yml" && ext != ".tpl" {
			return nil
		}
		rel, _ := filepath.Rel(chart, path)
		if !strings.Contains(filepath.ToSlash(rel), "templates/") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			if lookupCall.MatchString(sc.Text()) && len(lines) < 5 {
				lines = append(lines, fmt.Sprintf("`lookup` in `%s:%d`", filepath.ToSlash(rel), n))
			}
		}
		return nil
	})
	return lines
}
