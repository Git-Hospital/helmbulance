package execx

import (
	"slices"
	"testing"
)

func TestSandboxEnv(t *testing.T) {
	got := SandboxEnv([]string{
		"PATH=/usr/bin",
		"HOME=/home/u",
		"LC_ALL=C",
		"DOCKER_HOST=unix:///run/podman.sock",
		"HELM_KUBEAPISERVER=https://127.0.0.1:1",
		"HELM_KUBETOKEN=token",
		"HELM_KUBECONTEXT=prod",
		"HELM_DRIVER=sql",
		"HELM_DRIVER_SQL_CONNECTION_STRING=postgres://prod",
		"KUBECONFIG=/home/u/.kube/prod",
		"AWS_SECRET_ACCESS_KEY=secret",
	})
	want := []string{
		"PATH=/usr/bin",
		"HOME=/home/u",
		"LC_ALL=C",
		"DOCKER_HOST=unix:///run/podman.sock",
		"HELM_DRIVER=secret",
	}
	if !slices.Equal(got, want) {
		t.Errorf("SandboxEnv = %q, want %q", got, want)
	}
}
