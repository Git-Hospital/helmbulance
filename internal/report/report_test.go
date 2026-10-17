package report

import (
	"testing"

	"github.com/egezamb/helmbulance/internal/scenario"
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
