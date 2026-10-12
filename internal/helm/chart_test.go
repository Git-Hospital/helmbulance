package helm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
)

// fakeHelm puts a helm that prints out first on PATH.
func fakeHelm(t *testing.T, out string) *Client {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "out"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat " + filepath.Join(bin, "out") + "\n"
	if err := os.WriteFile(filepath.Join(bin, "helm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &Client{Runner: &execx.Runner{}, Timeout: time.Minute}
}

func TestChartVersion(t *testing.T) {
	c := fakeHelm(t, "apiVersion: v2\nname: demo\nversion: 1.2.3-rc.1\nappVersion: \"9.9\"\n")
	if v, err := c.ChartVersion(t.Context(), "./demo"); err != nil || v != "1.2.3-rc.1" {
		t.Errorf("ChartVersion = %q, %v; want 1.2.3-rc.1", v, err)
	}
}

func TestChartVersionMissing(t *testing.T) {
	c := fakeHelm(t, "apiVersion: v2\nname: demo\n")
	if v, err := c.ChartVersion(t.Context(), "./demo"); err == nil {
		t.Errorf("ChartVersion = %q, want an error", v)
	}
}
