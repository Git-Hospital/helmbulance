package execx

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// TestSandboxRunnerEnv runs a child through NewSandboxRunner with a hostile environment, so it
// checks what the child actually sees, not only the filter.
func TestSandboxRunnerEnv(t *testing.T) {
	t.Setenv("HELM_KUBEAPISERVER", "https://127.0.0.1:1")
	t.Setenv("HELM_DRIVER", "sql")
	t.Setenv("KUBECONFIG", "/home/u/.kube/prod")
	out, err := NewSandboxRunner().Run(t.Context(), "/usr/bin/env")
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, kv := range env {
		if strings.HasPrefix(kv, "HELM_KUBE") || strings.HasPrefix(kv, "KUBECONFIG=") || kv == "HELM_DRIVER=sql" {
			t.Errorf("child saw %q", kv)
		}
	}
	if !slices.Contains(env, "HELM_DRIVER=secret") {
		t.Errorf("child env %q has no HELM_DRIVER=secret", env)
	}
}

// TestCommandsUseSandboxRunner fails when a command builds its own Runner, which could pass the
// caller's environment to helm.
func TestCommandsUseSandboxRunner(t *testing.T) {
	files, _ := filepath.Glob("../../cmd/*/*.go")
	if len(files) == 0 {
		t.Fatal("no command sources found")
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "execx.Runner{") {
			t.Errorf("%s builds an execx.Runner directly; use execx.NewSandboxRunner", f)
		}
	}
}
