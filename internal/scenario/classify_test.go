package scenario

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestIsEnvironmentError(t *testing.T) {
	cases := []struct {
		err  string
		want bool
	}{
		// Helm 4.3 on kind, a Deployment that never became ready under --wait --timeout 15s.
		{"Error: INSTALLATION FAILED: resource Deployment/nr/nr not ready. status: InProgress, message: Available: 0/1\ncontext deadline exceeded", true},
		{"Error: UPGRADE FAILED: timed out waiting for the condition", true},
		{"signal: killed", true},
		{"Back-off pulling image: ErrImagePull", true},
		{"toomanyrequests: You have reached your pull rate limit", true},
		{"dial tcp 127.0.0.1:6443: connect: connection refused", true},
		{"Error: UPGRADE FAILED: cannot patch \"db\" with kind StatefulSet: StatefulSet.apps \"db\" is invalid: spec: Forbidden: updates to statefulset spec for fields other than ... are forbidden", false},
		{"Error: UPGRADE FAILED: field is immutable", false},
		{"Error: execution error at (demo/templates/secret.yaml:3:4): password is required", false},
		{"Error: values don't meet the specifications of the schema(s)", false},
		// The chart's own admission webhook, called before its pod is ready: the chart's fault.
		{`Error: UPGRADE FAILED: Internal error occurred: failed calling webhook "validate.nginx.ingress.kubernetes.io": failed to call webhook: Post "https://ingress-nginx-controller-admission.hb.svc:443/networking/v1/ingresses?timeout=10s": dial tcp 10.96.153.31:443: connect: connection refused`, false},
		{`Error: INSTALLATION FAILED: Internal error occurred: failed calling webhook "mutate.example.io": failed to call webhook: Post "https://example-webhook.hb.svc:443/mutate?timeout=10s": context deadline exceeded`, false},
	}
	for _, c := range cases {
		if got := IsEnvironmentError(context.Background(), errors.New(c.err)); got != c.want {
			t.Errorf("IsEnvironmentError(%q) = %v, want %v", c.err, got, c.want)
		}
	}
	if IsEnvironmentError(context.Background(), nil) {
		t.Error("a nil error is an environment error")
	}
}

func TestIsEnvironmentErrorFromTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !IsEnvironmentError(ctx, errors.New("exit status 1")) {
		t.Error("an error after the run was cancelled is not an environment error")
	}
	if !IsEnvironmentError(context.Background(), fmt.Errorf("helm: %w", context.DeadlineExceeded)) {
		t.Error("a wrapped deadline is not an environment error")
	}
}
