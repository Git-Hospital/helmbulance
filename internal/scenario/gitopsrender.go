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

	// The same values, rendered with the cluster (as Helm does) and twice without it (as Argo CD does).
	render := func(server bool) (oracle.SecretSnapshot, error) {
		out, err := env.Helm.Template(ctx, release, ns, env.Chart, env.Values, server)
		if err != nil {
			return nil, err
		}
		return env.Hasher.Snapshot(helm.RenderedSecrets(out, ns)), nil
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

	changes, everySync := gitopsDrift(live, server, client1, client2)
	changes, expected := splitExpected(changes, env.ExpectedRotation)
	if len(changes) == 0 {
		res.Verdict = Pass
		res.Summary = fmt.Sprintf("a render without a cluster gives the release's %d Secret value(s) unchanged", len(live))
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
	evidence := append([]string{"Rendered without a cluster (`helm template`), with the same values, these Secret values differ from the installed release, which Helm keeps (`helm template --dry-run=server` matches it or leaves the Secret alone):"},
		describe(changes, consumers)...)
	if refs := slices.Sorted(maps.Keys(everySync)); len(refs) > 0 {
		evidence = append(evidence, "A new value on every client-side render, so on every sync: `"+strings.Join(refs, "`, `")+"`.")
	}
	// IsLocal, not IsPath: "testdata/charts/x" looks like repo/chart but is on disk.
	if helm.IsLocal(env.Chart) {
		evidence = append(evidence, lookupCalls(env.Chart)...)
	}
	res.Verdict = Fail
	res.Summary = fmt.Sprintf("%d Secret value(s) change when the chart is rendered without a cluster", len(changes))
	res.Findings = append(res.Findings, Finding{
		ID:       "HB-GR-001",
		Title:    "Secret values change when the chart is rendered without a cluster (GitOps)",
		Severity: worstSeverity(changes, consumers, stateful, statefulKnown),
		Evidence: evidence,
		Impact: "Argo CD and other tools that render with `helm template` have no cluster to `lookup` in, " +
			"so each sync writes a value the running release does not use. Pods restart on it, and a " +
			"database that kept the first password locks its clients out.",
		Fix: "Take the value from values or an existingSecret instead of generating it behind `lookup`. " +
			"If the chart must generate it, document that it needs `helm install` or `upgrade`, and for " +
			"Argo CD an ignoreDifferences entry for these keys.",
		Reproduce: repro,
	})
	return res
}

// gitopsDrift lists the keys of the release (live) that a client-side render changes while Helm
// keeps them: the server-side render either gives the same value or leaves the key out, as charts
// that render a Secret only `if not (lookup …)` do. A key the server-side render changes too is
// regenerated under Helm as well, which upgrade-without-values reports, so it is left out here.
// everySync marks the keys whose two client-side renders differ from each other.
func gitopsDrift(live, server, client1, client2 oracle.SecretSnapshot) ([]oracle.Change, map[string]bool) {
	var changes []oracle.Change
	everySync := map[string]bool{}
	for _, ref := range slices.Sorted(maps.Keys(live)) {
		if s, ok := server[ref]; ok && s != live[ref] {
			continue
		}
		c, ok := client1[ref]
		if !ok || c == live[ref] {
			continue
		}
		changes = append(changes, oracle.Change{Ref: ref, Kind: oracle.Changed})
		if c2, ok := client2[ref]; ok && c2 != c {
			everySync[ref] = true
		}
	}
	return changes, everySync
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
