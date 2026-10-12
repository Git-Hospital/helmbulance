package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
	"github.com/egezamb/helmbulance/internal/helm"
)

// envWithRelease returns an Env whose helm reports that the release runs version.
func envWithRelease(t *testing.T, fromVersion, version string) *Env {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho '{\"name\":\"hb\",\"version\":\"" + version + "\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "helm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	runner := &execx.Runner{}
	return &Env{Runner: runner, Helm: &helm.Client{Runner: runner, Timeout: time.Minute}, FromVersion: fromVersion}
}

func TestInstalledFrom(t *testing.T) {
	cases := []struct {
		fromVersion, runs string
		ok                bool
	}{
		{"1.0.0", "1.0.0", true},
		{"v1.0.0", "1.0.0", true},
		{"1.2.3", "2.0.0", false},
		{"~1.2", "1.2.5", true},
		{">=1.0.0 <2.0.0", "1.9.0", true},
		{"1.x", "1.4.0", true},
	}
	for _, c := range cases {
		env := envWithRelease(t, c.fromVersion, c.runs)
		v, err := installedFrom(t.Context(), env, "hb", "ns")
		if v != c.runs || (err == nil) != c.ok {
			t.Errorf("--from-version %s, runs %s: installedFrom = %q, %v; want ok=%v", c.fromVersion, c.runs, v, err, c.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "runs 2.0.0, not --from-version 1.2.3") {
			t.Errorf("error %q does not name both versions", err)
		}
	}
}
