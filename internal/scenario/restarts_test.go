package scenario

import (
	"errors"
	"strings"
	"testing"

	"github.com/egezamb/helmbulance/internal/execx"
)

var (
	installCmd = []execx.Command{{Line: "helm install hb ./chart"}}
	reuseCmd   = []execx.Command{{Line: "helm upgrade hb ./chart"}}
)

// The control already showed the restart; a treatment that could not run must not lose it.
func TestRestartsSurviveAFailedTreatment(t *testing.T) {
	res := inconclusive(Result{}, "the upgrade with the values files failed", errors.New("timed out"))
	res = withRestarts(res, []string{"Deployment/web"}, installCmd, reuseCmd)
	if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-UV-003" {
		t.Fatalf("verdict = %s with %d finding(s), want FAIL with HB-UV-003", res.Verdict, len(res.Findings))
	}
	if !strings.Contains(res.Summary, "the upgrade with the values files failed") {
		t.Errorf("summary %q lost why the treatment did not run", res.Summary)
	}
}
