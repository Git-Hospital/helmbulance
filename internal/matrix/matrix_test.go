package matrix

import (
	"errors"
	"testing"

	"github.com/egezamb/helmbulance/internal/scenario"
)

func TestInstallFailedIsAFinding(t *testing.T) {
	err := errors.New(`Error: INSTALLATION FAILED: resource mapping not found for name: "web" kind: "PodSecurityPolicy"`)
	res := installFailed(scenario.Result{Scenario: "k8s-1.37"}, "1.37", err, nil, nil)
	if res.Verdict != scenario.Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-K8S-001" {
		t.Errorf("verdict = %s (%s), want FAIL with HB-K8S-001", res.Verdict, res.Summary)
	}
}
