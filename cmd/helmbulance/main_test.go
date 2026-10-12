package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// stderrOf runs f and returns what it wrote to os.Stderr.
func stderrOf(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = old
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestRefuseRemote(t *testing.T) {
	cases := []struct {
		allow   bool
		refs    []string
		refused bool
		says    string
	}{
		{false, []string{"../../testdata/charts/keeps-secret", ""}, false, ""},
		{false, []string{"oci://x/y"}, true, "pass --allow-remote"},
		{true, []string{"oci://x/y"}, false, "warning: running oci://x/y"},
		{false, []string{"./nonexistent"}, true, "path not found"},
		{true, []string{"~/chart"}, true, "path not found"},
		{false, []string{"../../testdata/charts/keeps-secret", "bitnami/redis"}, true, "bitnami/redis is not a chart on disk"},
	}
	for _, c := range cases {
		var refused bool
		out := stderrOf(t, func() { refused = refuseRemote(c.allow, c.refs...) })
		if refused != c.refused || !strings.Contains(out, c.says) {
			t.Errorf("refuseRemote(%v, %q) = %v, %q; want %v and a message with %q", c.allow, c.refs, refused, out, c.refused, c.says)
		}
		if c.says == "" && out != "" {
			t.Errorf("refuseRemote(%v, %q) printed %q, want nothing", c.allow, c.refs, out)
		}
	}
}

func TestRefuseFromVersionWithoutFrom(t *testing.T) {
	const local = "../../testdata/charts/upgrade-v2-ok"
	cases := []struct {
		chart, from, fromVersion string
		refused                  bool
	}{
		{local, "", "", false},
		{local, "", "1.0.0", true},
		{local, "../../testdata/charts/upgrade-v1", "1.0.0", false},
		{"repo/chart", "", "1.0.0", false},
	}
	for _, c := range cases {
		var refused bool
		out := stderrOf(t, func() { refused = refuseFromVersion(c.chart, c.from, c.fromVersion) })
		if refused != c.refused {
			t.Errorf("refuseFromVersion(%q, %q, %q) = %v, want %v", c.chart, c.from, c.fromVersion, refused, c.refused)
		}
		if refused && !strings.Contains(out, "needs --from") {
			t.Errorf("message %q does not say --from is needed", out)
		}
	}
}
