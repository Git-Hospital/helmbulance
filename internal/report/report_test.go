package report

import (
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
