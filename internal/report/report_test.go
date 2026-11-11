package report

import (
	"encoding/json"
	"os"
	"path/filepath"
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
