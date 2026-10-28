package scan

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/egezamb/helmbulance/internal/scenario"
)

// containerLists are the pod spec fields that hold containers.
var containerLists = []string{"initContainers", "containers", "ephemeralContainers"}

// duplicateEnv lists the containers whose env names one variable twice, rendered with the values
// given to the scan. A chart that sets a variable itself and also lets values set it renders both:
// Helm 4's server-side apply rejects the install ("duplicate entries for key"), and Helm 3 deploys
// with a warning that one definition may be dropped on apply.
func duplicateEnv(ctx context.Context, o Options) (res scenario.Result) {
	res.Scenario = "scan:duplicate-env"
	objs, err := render(ctx, o, o.Chart)
	if err != nil {
		res.Verdict = scenario.Inconclusive
		res.Summary = "could not render the chart: " + err.Error()
		return res
	}
	var evidence []string
	for _, obj := range objs {
		for _, c := range containersIn(obj.Doc) {
			if dups := duplicateNames(c["env"]); len(dups) > 0 {
				evidence = append(evidence, fmt.Sprintf("`%s/%s` container `%s`: %s", obj.Kind, obj.Name, str(c["name"]), strings.Join(dups, ", ")))
			}
		}
	}
	sort.Strings(evidence)
	if len(evidence) == 0 {
		res.Verdict = scenario.Pass
		res.Summary = "no container names an environment variable twice"
		return res
	}
	res.Verdict = scenario.Fail
	res.Summary = fmt.Sprintf("%d container(s) with an environment variable named twice (candidates)", len(evidence))
	res.Findings = []scenario.Finding{{
		ID:       "HB-SCAN-005",
		Title:    "Candidate: a container names the same environment variable twice",
		Severity: "candidate",
		Evidence: evidence,
		Impact: "Helm 4 applies server-side and rejects the install or upgrade with `duplicate entries " +
			"for key`. Helm 3 deploys with a warning that one definition may be dropped on apply, so which " +
			"value the container gets depends on how the release is applied.",
		Fix: "Render each variable once: when values set a variable the chart also sets, leave the chart's " +
			"entry out, or merge both sources into one map before rendering.",
		Reproduce: []string{fmt.Sprintf("helm install hb %s%s   # with Helm 4", o.Chart, valuesFlags(o))},
	}}
	return res
}

// containersIn finds the containers of every pod spec in a rendered object, at any depth, so a
// Deployment, a CronJob and a bare Pod are read alike.
func containersIn(doc any) []map[string]any {
	var out []map[string]any
	switch x := doc.(type) {
	case map[string]any:
		for _, field := range containerLists {
			if list, ok := x[field].([]any); ok {
				for _, item := range list {
					if c, ok := item.(map[string]any); ok {
						out = append(out, c)
					}
				}
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "initContainers" || k == "containers" || k == "ephemeralContainers" {
				continue
			}
			out = append(out, containersIn(x[k])...)
		}
	case []any:
		for _, item := range x {
			out = append(out, containersIn(item)...)
		}
	}
	return out
}

// duplicateNames lists, in order, the env names that appear more than once, with their count.
func duplicateNames(env any) []string {
	list, _ := env.([]any)
	count := map[string]int{}
	var order []string
	for _, item := range list {
		e, _ := item.(map[string]any)
		name := str(e["name"])
		if name == "" {
			continue
		}
		if count[name] == 0 {
			order = append(order, name)
		}
		count[name]++
	}
	var dups []string
	for _, name := range order {
		if count[name] > 1 {
			dups = append(dups, fmt.Sprintf("`%s` %d times", name, count[name]))
		}
	}
	return dups
}

// valuesFlags repeats the values the scan rendered with, for a Reproduce line.
func valuesFlags(o Options) string {
	var b strings.Builder
	for _, f := range o.Values.Files {
		b.WriteString(" -f " + f)
	}
	for _, s := range o.Values.Sets {
		b.WriteString(" --set " + s)
	}
	return b.String()
}
