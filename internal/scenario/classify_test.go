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
		// etcd under load or electing a leader, as the API server passes it on.
		{"Error: UPGRADE FAILED: etcdserver: request timed out", true},
		{"Error: INSTALLATION FAILED: etcdserver: leader changed", true},
		{"Error: UPGRADE FAILED: etcdserver: request timed out, possibly due to previous leader failure", true},
		// An object over etcd's request limit fails the same way on every cluster: the chart's fault.
		{"Error: UPGRADE FAILED: failed to create resource: etcdserver: request is too large", false},
		// helm 4.3 downloading the chart again: an unknown host, a host or repository answering 503.
		{`Error: Get "https://charts.example.invalid/x-1.0.0.tgz": dial tcp: lookup charts.example.invalid: no such host`, true},
		{"Error: failed to fetch https://charts.example.com/x-1.0.0.tgz : 503 Service Unavailable", true},
		{`Error: looks like "https://charts.example.com" is not a valid chart repository or cannot be reached: failed to fetch https://charts.example.com/index.yaml : 503 Service Unavailable`, true},
		{"Error: UPGRADE FAILED: cannot patch \"db\" with kind StatefulSet: StatefulSet.apps \"db\" is invalid: spec: Forbidden: updates to statefulset spec for fields other than ... are forbidden", false},
		{"Error: UPGRADE FAILED: field is immutable", false},
		{"Error: execution error at (demo/templates/secret.yaml:3:4): password is required", false},
		{"Error: values don't meet the specifications of the schema(s)", false},
		// The chart's own admission webhook, called before its pod is ready: the chart's fault.
		{`Error: UPGRADE FAILED: Internal error occurred: failed calling webhook "validate.nginx.ingress.kubernetes.io": failed to call webhook: Post "https://ingress-nginx-controller-admission.hb.svc:443/networking/v1/ingresses?timeout=10s": dial tcp 10.96.153.31:443: connect: connection refused`, false},
		{`Error: INSTALLATION FAILED: Internal error occurred: failed calling webhook "mutate.example.io": failed to call webhook: Post "https://example-webhook.hb.svc:443/mutate?timeout=10s": context deadline exceeded`, false},
		{`Error: INSTALLATION FAILED: Internal error occurred: failed calling webhook "validate.example.io": failed to call webhook: Post "https://example-webhook.hb.svc:443/validate?timeout=10s": dial tcp: lookup example-webhook.hb.svc: no such host`, false},
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

func TestRefusedAtRender(t *testing.T) {
	cases := []struct {
		err  string
		want bool
	}{
		// helm 4.3 on a chart with `required`, and on one with `fail`.
		{"Error: UPGRADE FAILED: execution error at (refuses-dropped-key/templates/secret.yaml:6:21): encryptionKey is required: it encrypts data already written", true},
		{"Error: INSTALLATION FAILED: execution error at (demo/templates/secret.yaml:3:4): encryptionKey differs from the key already in use", true},
		{"Error: UPGRADE FAILED: field is immutable", false},
		{"Error: UPGRADE FAILED: context deadline exceeded", false},
	}
	for _, c := range cases {
		if got := RefusedAtRender(context.Background(), errors.New(c.err)); got != c.want {
			t.Errorf("RefusedAtRender(%q) = %v, want %v", c.err, got, c.want)
		}
	}
	if RefusedAtRender(context.Background(), nil) {
		t.Error("a nil error is a refusal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if RefusedAtRender(ctx, errors.New(cases[0].err)) {
		t.Error("a cancelled run is a refusal")
	}
}
