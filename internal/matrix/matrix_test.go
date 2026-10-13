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

func result(version, verdict, finding string) scenario.Result {
	r := scenario.Result{Scenario: "k8s-" + version, Verdict: verdict}
	if finding != "" {
		r.Findings = []scenario.Finding{{ID: finding}}
	}
	return r
}

func TestBlameOnlyWhenAnotherVersionInstalled(t *testing.T) {
	// 1.37 fails to install while 1.34 installs: the version is to blame.
	got := Attribute([]scenario.Result{
		result("1.34", scenario.Pass, ""),
		result("1.37", scenario.Fail, "HB-K8S-001"),
	})
	if got[1].Verdict != scenario.Fail {
		t.Errorf("1.37 = %s, want FAIL when 1.34 installed", got[1].Verdict)
	}

	// Nothing installed anywhere: no version is to blame.
	got = Attribute([]scenario.Result{
		result("1.34", scenario.Fail, "HB-K8S-001"),
		result("1.37", scenario.Fail, "HB-K8S-001"),
	})
	for _, r := range got {
		if r.Verdict != scenario.Inconclusive || len(r.Findings) > 0 {
			t.Errorf("%s = %s with %d finding(s), want INCONCLUSIVE and none", r.Scenario, r.Verdict, len(r.Findings))
		}
	}

	// A version that installed but was unhealthy still counts as installed.
	got = Attribute([]scenario.Result{
		result("1.34", scenario.Fail, "HB-K8S-002"),
		result("1.37", scenario.Fail, "HB-K8S-001"),
	})
	if got[1].Verdict != scenario.Fail {
		t.Errorf("1.37 = %s, want FAIL when 1.34 installed but was unhealthy", got[1].Verdict)
	}
}

func TestBlameARemovedAPIEvenWhenNothingInstalled(t *testing.T) {
	// A chart that uses policy/v1beta1 PodSecurityPolicy fails everywhere: the API was removed
	// before the oldest version tested, so it is still a Kubernetes version problem.
	err := errors.New(`Error: INSTALLATION FAILED: unable to build kubernetes objects from release manifest: resource mapping not found for name: "web" namespace: "" from "": no matches for kind "PodSecurityPolicy" in version "policy/v1beta1"`)
	var results []scenario.Result
	for _, v := range []string{"1.34", "1.35", "1.37"} {
		results = append(results, installFailed(t.Context(), scenario.Result{Scenario: "k8s-" + v}, v, err, nil, nil))
	}
	for _, r := range Attribute(results) {
		if r.Verdict != scenario.Fail || len(r.Findings) != 1 || r.Findings[0].ID != "HB-K8S-001" {
			t.Errorf("%s = %s (%s), want FAIL with HB-K8S-001", r.Scenario, r.Verdict, r.Summary)
		}
	}
	// One version tested is the same case.
	if r := Attribute(results[:1])[0]; r.Verdict != scenario.Fail {
		t.Errorf("a single --k8s version: %s = %s, want FAIL", r.Scenario, r.Verdict)
	}
}
