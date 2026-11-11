// Package scan is a static pre-screen: it renders a chart and reads its templates, without a
// cluster, and lists candidates for the problems the cluster scenarios confirm. A candidate is not
// a finding: each one names the `helmbulance test` run that confirms or rejects it.
package scan

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Git-Hospital/helmbulance/internal/execx"
	"github.com/Git-Hospital/helmbulance/internal/helm"
	"github.com/Git-Hospital/helmbulance/internal/scenario"
)

// Options is what a scan needs.
type Options struct {
	Runner      *execx.Runner
	Helm        string // helm binary; empty for "helm"
	Chart       string // local chart directory
	Values      helm.Values
	From        string // chart to compare against for immutable fields
	FromVersion string
}

// Run executes every check and returns one Result per check.
func Run(ctx context.Context, o Options) []scenario.Result {
	return []scenario.Result{
		immutableFields(ctx, o),
		firstUpgradeRestart(o),
		regeneratedSecrets(ctx, o),
		unquotedValues(ctx, o),
		duplicateEnv(ctx, o),
	}
}

// object is one rendered manifest and the template it came from.
type object struct {
	Kind, Name, Source string
	Hook               bool
	HookEvents         string // the helm.sh/hook annotation, for a hook
	Doc                map[string]any
}

var sourceLine = regexp.MustCompile(`(?m)^# Source: (\S+)`)

func render(ctx context.Context, o Options, chart string, extra ...string) ([]object, error) {
	bin := o.Helm
	if bin == "" {
		bin = "helm"
	}
	args := append([]string{"template", "hb", chart, "--namespace", "hb"}, extra...)
	for _, f := range o.Values.Files {
		args = append(args, "-f", f)
	}
	for _, s := range o.Values.Sets {
		args = append(args, "--set", s)
	}
	out, err := o.Runner.Run(ctx, bin, args...)
	if err != nil {
		return nil, err
	}
	var objs []object
	for _, raw := range regexp.MustCompile(`(?m)^---\s*$`).Split(string(out), -1) {
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(raw), &doc); err != nil || doc == nil {
			continue
		}
		meta, _ := doc["metadata"].(map[string]any)
		ann, _ := meta["annotations"].(map[string]any)
		events, hook := ann["helm.sh/hook"]
		obj := object{Kind: str(doc["kind"]), Name: str(meta["name"]), Hook: hook, HookEvents: str(events), Doc: doc}
		if m := sourceLine.FindStringSubmatch(raw); m != nil {
			obj.Source = m[1]
		}
		objs = append(objs, obj)
	}
	return objs, nil
}

// immutableFields compares the earlier version, rendered as an install, with the chart under test,
// rendered as an upgrade, and lists changes to fields Kubernetes does not allow to change.
func immutableFields(ctx context.Context, o Options) (res scenario.Result) {
	res.Scenario = "scan:immutable-fields"
	if o.FromVersion == "" {
		res.Verdict = scenario.Skipped
		res.Summary = "give --from-version to compare with the version users upgrade from"
		return res
	}
	from := o.From
	if from == "" {
		from = o.Chart
	}
	old, err := render(ctx, o, from, "--version", o.FromVersion)
	if err != nil {
		res.Verdict = scenario.Inconclusive
		res.Summary = "could not render " + o.FromVersion + ": " + err.Error()
		return res
	}
	cur, err := render(ctx, o, o.Chart, "--is-upgrade")
	if err != nil {
		res.Verdict = scenario.Inconclusive
		res.Summary = "could not render the chart: " + err.Error()
		return res
	}
	evidence := immutableChanges(old, cur, func(source string) bool {
		return sourceContains(o.Chart, source, "Release.Revision")
	})
	if len(evidence) == 0 {
		res.Verdict = scenario.Pass
		res.Summary = "no immutable field changes from " + o.FromVersion
		return res
	}
	res.Verdict = scenario.Fail
	res.Summary = fmt.Sprintf("%d immutable field change(s) from %s (candidates)", len(evidence), o.FromVersion)
	res.Findings = []scenario.Finding{{
		ID:       "HB-SCAN-001",
		Title:    "Candidate: the upgrade from " + o.FromVersion + " changes fields Kubernetes does not allow to change",
		Severity: "candidate",
		Evidence: evidence,
		Impact:   "If confirmed, every `helm upgrade` from " + o.FromVersion + " fails with \"field is immutable\".",
		Fix:      "Keep the field as it was, or document the migration (for StatefulSets, delete with --cascade=orphan before upgrading).",
		Reproduce: []string{fmt.Sprintf("helmbulance test %s --scenario chart-upgrade --from-version %s%s",
			o.Chart, o.FromVersion, fromFlag(o.From))},
	}}
	return res
}

// withHelpers is text followed by the bodies of the helpers it includes, and of the helpers those
// include, each once.
func withHelpers(text string, helpers map[string]string) string {
	seen := map[string]bool{}
	out := text
	for todo := text; todo != ""; {
		var next string
		for _, inc := range includeOf.FindAllStringSubmatch(todo, -1) {
			if body, ok := helpers[inc[1]]; ok && !seen[inc[1]] {
				seen[inc[1]] = true
				out += "\n" + body
				next += "\n" + body
			}
		}
		todo = next
	}
	return out
}

func fromFlag(from string) string {
	if from == "" {
		return ""
	}
	return " --from " + from
}

// immutableChanges lists the immutable fields that differ between an object of the earlier
// version (old) and the same object in the chart under test (cur). byRevision reports whether a
// template names its object after .Release.Revision.
func immutableChanges(old, cur []object, byRevision func(source string) bool) []string {
	byKey := map[string]object{}
	for _, c := range cur {
		byKey[c.Kind+"/"+c.Name] = c
	}
	var evidence []string
	for _, a := range old {
		b, ok := byKey[a.Kind+"/"+a.Name]
		if !ok || a.Hook || b.Hook {
			continue
		}
		// A Job named after .Release.Revision gets a new name on every real upgrade; both renders
		// here use revision 1, so comparing them would be a false alarm.
		if a.Kind == "Job" && byRevision(b.Source) {
			continue
		}
		for _, field := range immutablePaths(a.Kind) {
			if !reflect.DeepEqual(withDefault(a.Kind, field, dig(a.Doc, field)), withDefault(a.Kind, field, dig(b.Doc, field))) {
				evidence = append(evidence, fmt.Sprintf("`%s/%s` %s changes (%s)", a.Kind, a.Name, strings.Join(field, "."), b.Source))
			}
		}
	}
	return evidence
}

func immutablePaths(kind string) [][]string {
	switch kind {
	case "Deployment", "DaemonSet", "ReplicaSet":
		return [][]string{{"spec", "selector"}}
	case "StatefulSet":
		return [][]string{{"spec", "selector"}, {"spec", "serviceName"}, {"spec", "volumeClaimTemplates"}, {"spec", "podManagementPolicy"}}
	case "Job":
		return [][]string{{"spec", "template"}}
	case "PersistentVolumeClaim":
		return [][]string{{"spec", "accessModes"}, {"spec", "storageClassName"}}
	case "RoleBinding", "ClusterRoleBinding":
		return [][]string{{"roleRef"}}
	case "Service":
		return [][]string{{"spec", "clusterIP"}}
	}
	return nil
}

// apiDefaults are the values the API server fills in for an immutable field left out, so that
// writing the default out in one version and leaving it out in the other is not a change (#56).
var apiDefaults = map[string]any{
	"StatefulSet spec.podManagementPolicy": "OrderedReady",
	"Service spec.clusterIP":               "", // assigned by the API server
}

func withDefault(kind string, field []string, v any) any {
	if d, ok := apiDefaults[kind+" "+strings.Join(field, ".")]; ok && v == nil {
		return d
	}
	return v
}

var (
	randomFn = regexp.MustCompile(`\b(randAlphaNum|randAlpha|randAscii|randNumeric|randBytes|uuidv4|genCA|genPrivateKey|genSelfSignedCert|genSignedCert)\b`)
	// checksumOf matches the two ways a template names another for its checksum:
	// include (print $.Template.BasePath "/secret.yaml") and
	// include (printf "%s/secret.yaml" $.Template.BasePath), each piped to sha256sum (#56).
	checksumOf = regexp.MustCompile(`include\s*\(\s*(?:print\s+\$?\.Template\.BasePath\s+"([^"]+)"|printf\s+"%s([^"]+)"\s+\$?\.Template\.BasePath)\s*\)[^}]*?sha256sum`)
	includeOf  = regexp.MustCompile(`include\s+"([^"]+)"`)
	defineOf   = regexp.MustCompile(`(?s)\{\{-?\s*define\s+"([^"]+)"\s*-?\}\}(.*?)\{\{-?\s*end\s*-?\}\}`)
)

// firstUpgradeRestart reads the templates for pod-template checksums of a template that keeps
// generated values with lookup. On install the hash is taken over a second random draw, so the
// first upgrade changes it and restarts the pods although nothing changed.
func firstUpgradeRestart(o Options) (res scenario.Result) {
	res.Scenario = "scan:first-upgrade-restart"
	tdir := filepath.Join(o.Chart, "templates")
	files, helpers := map[string]string{}, map[string]string{}
	_ = filepath.WalkDir(tdir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(tdir, path)
		files[rel] = string(b)
		for _, m := range defineOf.FindAllStringSubmatch(string(b), -1) {
			helpers[m[1]] = m[2]
		}
		return nil
	})
	if len(files) == 0 {
		res.Verdict = scenario.Skipped
		res.Summary = "needs a chart directory to read its templates"
		return res
	}
	var evidence []string
	for name, text := range files {
		// Files whose name starts with _ render nothing; their helpers count where they are included.
		if strings.HasPrefix(filepath.Base(name), "_") {
			continue
		}
		// A checksum can sit in a helper the template includes, such as
		// checksum/config: {{ include "app.configChecksum" . }}.
		for _, m := range checksumOf.FindAllStringSubmatch(withHelpers(text, helpers), -1) {
			target := strings.TrimPrefix(m[1]+m[2], "/")
			body, ok := files[target]
			if !ok {
				continue
			}
			for _, inc := range includeOf.FindAllStringSubmatch(body, -1) {
				body += helpers[inc[1]]
			}
			if strings.Contains(body, "lookup") && randomFn.MatchString(body) {
				evidence = append(evidence, fmt.Sprintf("`templates/%s` hashes `templates/%s`, which keeps generated values with `lookup`", name, target))
			}
		}
	}
	sort.Strings(evidence)
	if len(evidence) == 0 {
		res.Verdict = scenario.Pass
		res.Summary = "no checksum of a template that combines lookup with a random value"
		return res
	}
	res.Verdict = scenario.Fail
	res.Summary = fmt.Sprintf("%d checksum(s) that likely change on the first upgrade (candidates)", len(evidence))
	res.Findings = []scenario.Finding{{
		ID:        "HB-SCAN-002",
		Title:     "Candidate: the first upgrade after install restarts pods although nothing changed",
		Severity:  "candidate",
		Evidence:  evidence,
		Impact:    "If confirmed, the first `helm upgrade` replaces these pods once for nothing; with one replica or Recreate that is an outage.",
		Fix:       "Hash the values that feed the Secret instead of the rendered template; generated values never change after install.",
		Reproduce: []string{fmt.Sprintf("helmbulance test %s --scenario upgrade-without-values   # HB-UV-003", o.Chart)},
	}}
	return res
}

// regeneratedSecrets renders the chart twice as an install and once as an upgrade, and lists the
// Secret keys that differ between the two installs, in Secrets whose template does not use lookup.
// Which scenario confirms one depends on when Helm renders it: a Secret Helm renders on upgrade
// gets a new value on every upgrade (HB-SCAN-003, upgrade-without-values), while one Helm renders
// only at install, under .Release.IsInstall or in an install-only hook, keeps its first value under
// Helm and gets a new one on every Argo CD sync (HB-SCAN-006, gitops-render; #86).
//
// Some charts refuse to render as an upgrade on purpose (Bitnami's "PASSWORDS ERROR" without the
// current passwords). Then hooks are still sorted by their annotation, and every other Secret goes
// to HB-SCAN-003 as it did before #107, with a note: one rendered only at install cannot be told
// apart without the upgrade render.
func regeneratedSecrets(ctx context.Context, o Options) (res scenario.Result) {
	res.Scenario = "scan:regenerated-secrets"
	var installs [2][]object
	for i := range installs {
		objs, err := render(ctx, o, o.Chart)
		if err != nil {
			res.Verdict = scenario.Inconclusive
			res.Summary = "could not render the chart: " + err.Error()
			return res
		}
		installs[i] = objs
	}
	var upgraded map[string]bool // nil: the chart did not render as an upgrade
	note := ""
	if objs, err := render(ctx, o, o.Chart, "--is-upgrade"); err != nil {
		note = "; the chart does not render as an upgrade (" + helmError(err.Error()) + "), so a Secret rendered only at install is not told apart from one rendered on upgrade"
	} else {
		upgraded = map[string]bool{}
		for _, x := range objs {
			upgraded[x.Kind+"/"+x.Name] = true
		}
	}
	usesLookup, readable := lookupChecker(o.Chart)
	onUpgrade, onSync, guarded := regenerated(installs[0], installs[1], upgraded, usesLookup)
	// A Secret that calls lookup is left out as a whole: whether lookup keeps every key that differs
	// only shows on a cluster, where upgrade-without-values compares them one by one (#51).
	if len(guarded) > 0 {
		note += fmt.Sprintf("; %d Secret(s) that use `lookup` differ between two renders and are left out (%s): only `helmbulance test` checks which keys lookup keeps",
			len(guarded), strings.Join(guarded, ", "))
	}
	if !readable {
		note += "; the chart's templates could not be read, so no Secret that uses `lookup` was left out"
	}
	if len(onUpgrade)+len(onSync) == 0 {
		res.Verdict = scenario.Pass
		res.Summary = "no Secret value is generated anew on every render" + note
		if len(guarded) > 0 {
			res.Summary = "no Secret without `lookup` has values generated anew on every render" + note
		}
		return res
	}
	res.Verdict = scenario.Fail
	res.Summary = fmt.Sprintf("%d Secret(s) with values generated on every render (candidates)", len(onUpgrade)+len(onSync)) + note
	if len(onUpgrade) > 0 {
		res.Findings = append(res.Findings, scenario.Finding{
			ID:        "HB-SCAN-003",
			Title:     "Candidate: Secret values are generated anew on every upgrade",
			Severity:  "candidate",
			Evidence:  onUpgrade,
			Impact:    "If confirmed, every `helm upgrade` replaces these values: credentials stop matching what the data was created with, and pods that hash the Secret restart.",
			Fix:       "Keep generated values with `lookup`, and support an existingSecret for tools that render without cluster access. A pre-install hook keeps the value under Helm but not under Argo CD, which runs the hook on every sync (HB-GR-001).",
			Reproduce: []string{fmt.Sprintf("helmbulance test %s --scenario upgrade-without-values   # HB-UV-001", o.Chart)},
		})
	}
	if len(onSync) > 0 {
		res.Findings = append(res.Findings, scenario.Finding{
			ID:        "HB-SCAN-006",
			Title:     "Candidate: Secret values are generated anew on every GitOps sync",
			Severity:  "candidate",
			Evidence:  onSync,
			Impact:    "If confirmed, Helm keeps the first value, but Argo CD and other tools that render with `helm template` render every sync as an install and run install hooks each time, so each sync writes a value the running release does not use.",
			Fix:       "Take the value from values or an existingSecret. For a hook, an ignoreDifferences entry does not help: Argo CD applies hooks on every sync, outside the diff.",
			Reproduce: []string{fmt.Sprintf("helmbulance test %s --scenario gitops-render   # HB-GR-001", o.Chart)},
		})
	}
	return res
}

// regenerated compares two install renders (a, b) of the Secrets and sorts the keys that differ by
// when Helm renders them: onUpgrade for a Secret Helm renders on upgrade, onSync for one only an
// install renders. upgraded holds the objects of the upgrade render ("Secret/name"); nil means the
// chart did not render as an upgrade, and then only hooks can be told apart, by their annotation.
// A Secret whose template uses lookup is left out, and so is a hook that runs neither at install
// nor at upgrade (test, delete, rollback).
func regenerated(a, b []object, upgraded map[string]bool, usesLookup func(source string) bool) (onUpgrade, onSync, guarded []string) {
	second := map[string]object{}
	for _, x := range b {
		second[x.Kind+"/"+x.Name] = x
	}
	for _, x := range a {
		if x.Kind != "Secret" {
			continue
		}
		y, ok := second["Secret/"+x.Name]
		if !ok {
			continue
		}
		var changed []string
		for _, field := range []string{"data", "stringData"} {
			d1, _ := x.Doc[field].(map[string]any)
			d2, _ := y.Doc[field].(map[string]any)
			for k, v := range d1 {
				if hash(v) != hash(d2[k]) {
					changed = append(changed, k)
				}
			}
		}
		if len(changed) == 0 {
			continue
		}
		if usesLookup(x.Source) {
			guarded = append(guarded, "`Secret/"+x.Name+"`")
			continue
		}
		sort.Strings(changed)
		differ := fmt.Sprintf("`Secret/%s` keys %s differ between two renders (%s)", x.Name, strings.Join(changed, ", "), x.Source)
		switch {
		case x.Hook && helm.UpgradeHook(x.HookEvents):
			onUpgrade = append(onUpgrade, differ+"; a `"+x.HookEvents+"` hook, run again on every upgrade")
		case x.Hook && helm.InstallOnlyHook(x.HookEvents):
			onSync = append(onSync, differ+"; a `"+x.HookEvents+"` hook: Helm runs it once, Argo CD on every sync")
		case x.Hook:
		case upgraded != nil && !upgraded["Secret/"+x.Name]:
			onSync = append(onSync, differ+"; rendered only at install (`.Release.IsInstall`), so Helm keeps the first value")
		default:
			onUpgrade = append(onUpgrade, differ)
		}
	}
	sort.Strings(onUpgrade)
	sort.Strings(onSync)
	sort.Strings(guarded)
	return onUpgrade, onSync, guarded
}

// maxUnquoted caps how many default values unquotedValues tries, one render each.
const maxUnquoted = 60

// quotedScalar is a default that is a string a user would write without quotes: a whole number
// other than 0, or "true". "0" and "false" are left out, since templates test them for truth and
// the unquoted value is falsy where the string is not; that difference is the user's intent.
var quotedScalar = regexp.MustCompile(`^(true|-?[1-9][0-9]{0,8})$`)

// unquotedValues sets each default that is a quoted number or "true" to the number or boolean a
// user writes without quotes, renders the chart again, and compares the fields each object has,
// not their values. A field under another name, such as an env `valueFrom: 3306` where the string
// rendered `value: "3306"`, is one the API server rejects or a setting that silently stops
// applying.
func unquotedValues(ctx context.Context, o Options) (res scenario.Result) {
	res.Scenario = "scan:unquoted-values"
	bin := o.Helm
	if bin == "" {
		bin = "helm"
	}
	out, err := o.Runner.Run(ctx, bin, "show", "values", o.Chart)
	if err != nil {
		res.Verdict = scenario.Inconclusive
		res.Summary = "could not read the chart's values: " + err.Error()
		return res
	}
	var defaults map[string]any
	if err := yaml.Unmarshal(out, &defaults); err != nil {
		res.Verdict = scenario.Inconclusive
		res.Summary = "could not parse the chart's values: " + err.Error()
		return res
	}
	candidates := quotedScalars(defaults, nil)
	if len(candidates) == 0 {
		res.Verdict = scenario.Pass
		res.Summary = "no default is a quoted number or \"true\""
		return res
	}
	base, err := render(ctx, o, o.Chart)
	if err != nil {
		res.Verdict = scenario.Inconclusive
		res.Summary = "could not render the chart: " + err.Error()
		return res
	}
	before := shapes(base)
	tried := candidates
	if len(tried) > maxUnquoted {
		tried = tried[:maxUnquoted]
	}
	var evidence, repro []string
	for _, c := range tried {
		objs, err := render(ctx, o, o.Chart, "--set-json", c.key+"="+c.value)
		if err != nil {
			continue // a refusal, such as a values.schema.json type, is the right answer
		}
		if gone, added := shapeDiff(before, shapes(objs)); len(gone)+len(added) > 0 {
			evidence = append(evidence, fmt.Sprintf("`%s`: quoted, %s; unquoted (%s), %s", c.key,
				fieldList(gone), c.value, fieldList(added)))
			repro = append(repro, fmt.Sprintf("helm install hb %s --set %s=%s   # quoted in values.yaml, unquoted here", o.Chart, c.key, c.value))
		}
	}
	summaryNote := ""
	if len(candidates) > len(tried) {
		summaryNote = fmt.Sprintf(" (the first %d of %d quoted defaults)", len(tried), len(candidates))
	}
	if len(evidence) == 0 {
		res.Verdict = scenario.Pass
		res.Summary = fmt.Sprintf("%d quoted default(s) render the same unquoted%s", len(tried), summaryNote)
		return res
	}
	res.Verdict = scenario.Fail
	res.Summary = fmt.Sprintf("%d value(s) that render differently unquoted (candidates)%s", len(evidence), summaryNote)
	if len(repro) > 3 {
		repro = repro[:3]
	}
	res.Findings = []scenario.Finding{{
		ID:       "HB-SCAN-004",
		Title:    "Candidate: an unquoted number or boolean changes what the chart renders",
		Severity: "candidate",
		Evidence: evidence,
		Impact: "Users write `port: 3306` or `enabled: true` without quotes, in a values file or with " +
			"`--set`. Here that renders a different field than the quoted default: one the API server " +
			"rejects, so the install fails, or a setting that silently stops applying.",
		Fix: "Render a scalar the same whatever its YAML type (`toString` or `toJson` it) and branch on " +
			"kind only for maps, or declare the type in values.schema.json so Helm rejects the wrong one " +
			"with a clear message.",
		Reproduce: repro,
	}}
	return res
}

// scalar is one default value: its --set-json key and the unquoted JSON to try.
type scalar struct{ key, value string }

// quotedScalars lists the string defaults that look like a number or true, in key order. Keys that
// --set-json cannot address plainly (a dot, a bracket, a comma, an equals sign) are skipped.
func quotedScalars(values map[string]any, path []string) []scalar {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []scalar
	for _, k := range keys {
		if strings.ContainsAny(k, ".[],=\\") {
			continue
		}
		p := append(append([]string{}, path...), k)
		switch v := values[k].(type) {
		case map[string]any:
			out = append(out, quotedScalars(v, p)...)
		case string:
			if quotedScalar.MatchString(v) {
				out = append(out, scalar{key: strings.Join(p, "."), value: v})
			}
		}
	}
	return out
}

// shapes is the set of field paths each rendered object has, without the values. A list of maps
// with a name is indexed by name, so an entry that moves keeps its path.
func shapes(objs []object) map[string]bool {
	set := map[string]bool{}
	for _, o := range objs {
		walkShape(o.Kind+"/"+o.Name+" ", o.Doc, set)
	}
	return set
}

func walkShape(prefix string, v any, set map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			p := prefix + "." + k
			if strings.HasSuffix(prefix, " ") {
				p = prefix + k
			}
			set[p] = true
			walkShape(p, child, set)
		}
	case []any:
		for i, child := range x {
			idx := fmt.Sprint(i)
			if m, ok := child.(map[string]any); ok {
				if n, ok := m["name"].(string); ok {
					idx = n
				}
			}
			p := prefix + "[" + idx + "]"
			set[p] = true
			walkShape(p, child, set)
		}
	}
}

// shapeDiff lists the field paths only before has (gone) and only after has (added), sorted.
func shapeDiff(before, after map[string]bool) (gone, added []string) {
	for p := range before {
		if !after[p] {
			gone = append(gone, p)
		}
	}
	for p := range after {
		if !before[p] {
			added = append(added, p)
		}
	}
	sort.Strings(gone)
	sort.Strings(added)
	return gone, added
}

// fieldList names up to two field paths, in backticks, or "nothing" for none.
func fieldList(paths []string) string {
	if len(paths) == 0 {
		return "nothing"
	}
	var shown []string
	for i, p := range paths {
		if i == 2 {
			shown = append(shown, fmt.Sprintf("and %d more", len(paths)-2))
			break
		}
		shown = append(shown, "`"+p+"`")
	}
	return strings.Join(shown, ", ")
}

// sourceContains reports whether the template a manifest came from contains text.
func sourceContains(chart, source, text string) bool {
	parts := strings.SplitN(source, "/", 2)
	if len(parts) != 2 {
		return false
	}
	b, err := os.ReadFile(filepath.Join(chart, parts[1]))
	return err == nil && strings.Contains(string(b), text)
}

// lookupChecker reports whether the template a manifest came from, or a helper it includes at
// any depth, calls lookup. It reads the chart's files once, subcharts and their archives included,
// so a lookup in a library or subchart helper counts, and a helper's body runs to its own end
// (#51). "# Source:" paths start with the chart name. readable is false when the chart's files
// could not be read, a repository or OCI reference: then no Secret is left out.
func lookupChecker(chart string) (uses func(source string) bool, readable bool) {
	files := chartFiles(chart)
	looks := lookupHelpers(files)
	return func(source string) bool {
		_, rel, ok := strings.Cut(source, "/")
		if !ok {
			return false
		}
		text, ok := files[rel]
		if !ok {
			return false
		}
		text = withoutComments(text)
		if lookupCall.MatchString(text) {
			return true
		}
		for _, inc := range includeOf.FindAllStringSubmatch(text, -1) {
			if looks[inc[1]] {
				return true
			}
		}
		return false
	}, files != nil
}

// lookupHelpers returns the helpers that call lookup, directly or through those they include.
//
// A name defined in several files counts only when every definition does. Which one helm renders
// depends on more than the files: .helmignore and a subchart's condition can leave out the one that
// would otherwise win, and neither is read here. So a disagreement makes a candidate that should not
// be one rather than leave a regenerated Secret out. An empty define never replaces another in
// text/template, so it counts only when the name has nothing else.
//
// One pass over the includes, without recursion: a body is done once it calls lookup or includes a
// helper that is, and a helper once all its bodies are.
func lookupHelpers(files map[string]string) map[string]bool {
	type body struct {
		name string
		done bool
	}
	bodies := map[string][]string{}
	for _, text := range files {
		for n, b := range defines(text) {
			bodies[n] = append(bodies[n], b)
		}
	}
	left := map[string]int{}
	users := map[string][]*body{}
	var todo []*body
	for n, all := range bodies {
		var kept []string
		for _, b := range all {
			if strings.TrimSpace(b) != "" {
				kept = append(kept, b)
			}
		}
		if len(kept) == 0 {
			kept = all[:1]
		}
		left[n] = len(kept)
		for _, b := range kept {
			x := &body{name: n}
			if lookupCall.MatchString(b) {
				x.done = true
				todo = append(todo, x)
				continue
			}
			for _, inc := range includeOf.FindAllStringSubmatch(b, -1) {
				users[inc[1]] = append(users[inc[1]], x)
			}
		}
	}
	looks := map[string]bool{}
	for len(todo) > 0 {
		x := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if left[x.name]--; left[x.name] > 0 || looks[x.name] {
			continue
		}
		looks[x.name] = true
		for _, u := range users[x.name] {
			if !u.done {
				u.done = true
				todo = append(todo, u)
			}
		}
	}
	return looks
}

func dig(doc map[string]any, path []string) any {
	var cur any = doc
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func hash(v any) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprint(v)))
}

// helmError shortens a failed helm command to its error: the text after its last "Error: ", up to
// two lines (a chart's `fail` message often starts on the second, as Bitnami's do), without helm's
// "Use --debug" hint.
func helmError(s string) string {
	if i := strings.LastIndex(s, "Error: "); i >= 0 {
		s = s[i+len("Error: "):]
	}
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "Use --debug") {
			continue
		}
		if lines = append(lines, l); len(lines) == 2 {
			break
		}
	}
	return strings.Join(lines, " ")
}
