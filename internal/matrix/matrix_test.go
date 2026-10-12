package matrix

import (
	"errors"
	"testing"

	"github.com/egezamb/helmbulance/internal/scenario"
)

func TestInstallFailedIsAFinding(t *testing.T) {
	err := errors.New(`Error: INSTALLATION FAILED: resource mapping not found for name: "web" kind: "PodSecurityPolicy"`)
	res := installFailed(t.Context(), scenario.Result{Scenario: "k8s-1.37"}, "1.37", err, nil, nil)
	if res.Verdict != scenario.Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-K8S-001" {
		t.Errorf("verdict = %s (%s), want FAIL with HB-K8S-001", res.Verdict, res.Summary)
	}
}

func TestInstallTimeoutIsInconclusive(t *testing.T) {
	for _, msg := range []string{
		"Error: INSTALLATION FAILED: resource Deployment/hb-matrix/web not ready. status: InProgress\ncontext deadline exceeded",
		"Failed to pull image \"nginx\": toomanyrequests: You have reached your pull rate limit",
	} {
		res := installFailed(t.Context(), scenario.Result{Scenario: "k8s-1.37"}, "1.37", errors.New(msg), nil, nil)
		if res.Verdict != scenario.Inconclusive || len(res.Findings) > 0 {
			t.Errorf("%q: verdict = %s with %d finding(s), want INCONCLUSIVE and none", msg, res.Verdict, len(res.Findings))
		}
	}
}
