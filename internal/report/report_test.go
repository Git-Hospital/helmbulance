package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Git-Hospital/helmbulance/internal/scenario"
)

func TestWorst(t *testing.T) {
	cases := []struct {
		verdicts []string
		want     string
	}{
		{[]string{scenario.Pass, scenario.Skipped}, scenario.Pass},
		{[]string{scenario.Pass, scenario.Inconclusive}, scenario.Inconclusive},
		{[]string{scenario.Inconclusive, scenario.Fail}, scenario.Fail},
		// Nothing was tested: that is not a clean run.
		{[]string{scenario.Skipped, scenario.Skipped}, scenario.Inconclusive},
		{nil, scenario.Inconclusive},
	}
	for _, c := range cases {
		r := &Report{}
		for _, v := range c.verdicts {
			r.Results = append(r.Results, scenario.Result{Verdict: v})
		}
		if got := r.Worst(); got != c.want {
			t.Errorf("Worst(%v) = %s, want %s", c.verdicts, got, c.want)
		}
	}
}

// The Pod Security level is shown as information, with the lines that keep the pods from the next
// level, masked like every other field; it changes no verdict (#115).
func TestReportShowsPodSecurityAsInformation(t *testing.T) {
	dir := t.TempDir()
	r := &Report{
		Hide: func(s string) string { return strings.ReplaceAll(s, "secret-pod", "***") },
		Results: []scenario.Result{
			{Scenario: "reinstall", Verdict: scenario.Pass, PodSecurity: &scenario.PodSecurity{Level: "baseline", Violations: []string{"secret-pod: seccompProfile"}}},
			{Scenario: "gitops-render", Verdict: scenario.Pass, PodSecurity: &scenario.PodSecurity{Level: "restricted"}},
		},
	}
	if err := r.Write(dir); err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "report.md"))
	for _, want := range []string{"## Pod Security (information, not a finding)", "- `reinstall`: **baseline**. Not restricted: ***: seccompProfile", "- `gitops-render`: **restricted**"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("report.md lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(string(md), "secret-pod") {
		t.Error("a violation line was not masked")
	}
	if r.Worst() != scenario.Pass {
		t.Errorf("verdict %s, want PASS: the level is not a finding", r.Worst())
	}
}

// report.json carries the overall verdict, the one the exit code follows, and writes an empty
// list as [], so a reader does not have to work either out (#58).
func TestReportJSONHasVerdictAndNoNullLists(t *testing.T) {
	for _, c := range []struct {
		results []scenario.Result
		want    string
	}{
		{[]scenario.Result{{Verdict: scenario.Pass}, {Verdict: scenario.Fail}}, scenario.Fail},
		{[]scenario.Result{{Verdict: scenario.Pass}, {Verdict: scenario.Inconclusive}}, scenario.Inconclusive},
		{[]scenario.Result{{Verdict: scenario.Pass}, {Verdict: scenario.Skipped}}, scenario.Pass},
		{nil, scenario.Inconclusive},
	} {
		dir := t.TempDir()
		r := &Report{Results: c.results}
		if err := r.Write(dir); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "report.json"))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if got["verdict"] != c.want {
			t.Errorf("verdict = %v for %+v, want %s", got["verdict"], c.results, c.want)
		}
		for _, key := range []string{"valuesFiles", "setKeys", "results", "commands"} {
			if _, ok := got[key].([]any); !ok {
				t.Errorf("%s = %v, want a list", key, got[key])
			}
		}
	}
}

// #164: a report written while the run goes on says so, in report.json and report.md, and
// writing it leaves no temporary file behind.
func TestPartialReportSaysItIsNotComplete(t *testing.T) {
	dir := t.TempDir()
	r := &Report{SchemaVersion: SchemaVersion, Results: []scenario.Result{{Scenario: "a", Verdict: scenario.Pass}}}
	if err := r.Write(dir); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	data, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["complete"] != false {
		t.Errorf("complete = %v, want false", got["complete"])
	}
	md, _ := os.ReadFile(filepath.Join(dir, "report.md"))
	if !strings.Contains(string(md), "written before the run finished") {
		t.Errorf("report.md does not say it is partial:\n%s", md)
	}

	r.Complete = true
	if err := r.Write(dir); err != nil {
		t.Fatal(err)
	}
	md, _ = os.ReadFile(filepath.Join(dir, "report.md"))
	if strings.Contains(string(md), "written before the run finished") {
		t.Errorf("a complete report.md still says it is partial:\n%s", md)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != "report.json report.md report.sarif" {
		t.Errorf("files in the report dir: %v, want only the three reports", names)
	}
}
