package kube

import (
	"encoding/json"
	"strings"
	"testing"
)

// The traffic pod runs in the chart's namespace. It must pass Pod Security "restricted" and
// carry no ServiceAccount token, so the chart's RBAC never reaches it.
func TestStartPodIsHardened(t *testing.T) {
	c := fakeKubectl(t, "")
	if err := c.StartPod(t.Context(), "ns", "hb-traffic", "img", "true", nil); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(c.Runner.Transcript()[0].Line)
	var overrides, overrideType string
	for i := 0; i+1 < len(fields); i++ {
		switch fields[i] {
		case "--overrides":
			overrides = fields[i+1]
		case "--override-type":
			overrideType = fields[i+1]
		}
	}
	if overrideType != "strategic" {
		t.Errorf("--override-type = %q, want strategic, so the container is merged by name", overrideType)
	}
	var pod struct {
		Spec struct {
			AutomountServiceAccountToken *bool `json:"automountServiceAccountToken"`
			SecurityContext              struct {
				RunAsNonRoot   *bool `json:"runAsNonRoot"`
				RunAsUser      int64 `json:"runAsUser"`
				SeccompProfile struct {
					Type string `json:"type"`
				} `json:"seccompProfile"`
			} `json:"securityContext"`
			Containers []struct {
				Name            string `json:"name"`
				SecurityContext struct {
					AllowPrivilegeEscalation *bool `json:"allowPrivilegeEscalation"`
					ReadOnlyRootFilesystem   *bool `json:"readOnlyRootFilesystem"`
					Capabilities             struct {
						Drop []string `json:"drop"`
					} `json:"capabilities"`
				} `json:"securityContext"`
			} `json:"containers"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(overrides), &pod); err != nil {
		t.Fatalf("--overrides %q: %v", overrides, err)
	}
	s := pod.Spec
	if s.AutomountServiceAccountToken == nil || *s.AutomountServiceAccountToken {
		t.Error("the ServiceAccount token is mounted")
	}
	if s.SecurityContext.RunAsNonRoot == nil || !*s.SecurityContext.RunAsNonRoot || s.SecurityContext.RunAsUser == 0 {
		t.Error("the pod may run as root")
	}
	if s.SecurityContext.SeccompProfile.Type != "RuntimeDefault" {
		t.Error("no RuntimeDefault seccomp profile")
	}
	if len(s.Containers) != 1 || s.Containers[0].Name != "hb-traffic" {
		t.Fatalf("containers = %+v, want one named after the pod", s.Containers)
	}
	sc := s.Containers[0].SecurityContext
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("privilege escalation allowed")
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Error("root filesystem writable")
	}
	if strings.Join(sc.Capabilities.Drop, ",") != "ALL" {
		t.Errorf("capabilities dropped = %v, want ALL", sc.Capabilities.Drop)
	}
}
