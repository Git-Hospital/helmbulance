package scenario

import (
	"io"
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
		// helm reads a partial version as a range: 1.2 installs 1.2.7.
		{"1.2", "1.2.7", true},
		{"1", "1.3.0", true},
		// A pre-release is one version, so a different one is caught.
		{"2.0.0-next.1", "2.0.0-next.1", true},
		{"1.0.0-experimental", "1.0.0", false},
	}
	for _, c := range cases {
		env := envWithRelease(t, c.fromVersion, c.runs)
		v, err := installedFrom(t.Context(), env, "hb", "ns")
		if v != c.runs || (err == nil) != c.ok {
			t.Errorf("--from-version %s, runs %s: installedFrom = %q, %v; want ok=%v", c.fromVersion, c.runs, v, err, c.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "runs "+c.runs+", not --from-version "+c.fromVersion) {
			t.Errorf("error %q does not name both versions", err)
		}
	}
}

func TestIsExactVersion(t *testing.T) {
	for v, want := range map[string]bool{
		"1.2.7": true, "v1.2.7": true, "2.0.0-next.1": true, "1.0.0+build.5": true,
		"1.2": false, "1": false, "~1.2": false, "1.x": false, ">=1.0.0": false, "": false,
	} {
		if got := isExactVersion(v); got != want {
			t.Errorf("isExactVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestToVersion(t *testing.T) {
	env := envWithRelease(t, "1.0.0", "2.0.0")
	if got := toVersion(t.Context(), env, "hb", "ns"); got != " to 2.0.0" {
		t.Errorf("toVersion = %q, want \" to 2.0.0\"", got)
	}
	env = envWithRelease(t, "1.0.0", "")
	env.Log = io.Discard
	if got := toVersion(t.Context(), env, "hb", "ns"); got != "" {
		t.Errorf("toVersion without a version = %q, want \"\"", got)
	}
}
