package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/egezamb/helmbulance/internal/scenario"
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

// scan takes remote charts, so it refuses only a path that is not on disk (#50).
func TestRefuseMissing(t *testing.T) {
	cases := []struct {
		refs    []string
		refused bool
	}{
		{[]string{"../../testdata/charts/keeps-secret", ""}, false},
		{[]string{"oci://x/y", "bitnami/redis"}, false},
		{[]string{"./nonexistent"}, true},
		{[]string{"~/chart"}, true},
		{[]string{"nonexistent"}, true},
		{[]string{"../../testdata/charts/upgrade-v2-ok", "../../testdata/charts/upgrade-v0"}, true},
	}
	for _, c := range cases {
		var refused bool
		out := stderrOf(t, func() { refused = refuseMissing(c.refs...) })
		if refused != c.refused || refused != strings.Contains(out, "path not found") {
			t.Errorf("refuseMissing(%q) = %v, %q; want %v", c.refs, refused, out, c.refused)
		}
	}
}

func TestScanExit(t *testing.T) {
	for _, c := range []struct {
		strict      bool
		worst       string
		interrupted bool
		want        int
	}{
		{false, scenario.Fail, false, exitClean},
		{false, scenario.Inconclusive, false, exitClean},
		{false, scenario.Pass, true, exitIncomplete},
		{false, scenario.Fail, true, exitIncomplete},
		{true, scenario.Pass, false, exitClean},
		{true, scenario.Fail, false, exitFindings},
		{true, scenario.Inconclusive, false, exitIncomplete},
		{true, scenario.Pass, true, exitIncomplete},
		{true, scenario.Fail, true, exitFindings},
	} {
		if got := scanExit(c.strict, c.worst, c.interrupted); got != c.want {
			t.Errorf("scanExit(strict %v, %s, interrupted %v) = %d, want %d", c.strict, c.worst, c.interrupted, got, c.want)
		}
	}
}

// A -f value is a file, a URL or "-", never a chart repository, so a missing file is refused
// even when it has a slash and no "./", which refuseMissing would take for repo/chart (#68).
func TestRefuseMissingValues(t *testing.T) {
	for _, c := range []struct {
		files   []string
		refused bool
	}{
		{nil, false},
		{[]string{"../../testdata/charts/keeps-secret/values.yaml"}, false},
		{[]string{"-"}, false},
		{[]string{"https://example.invalid/values.yaml"}, false},
		{[]string{"valuse.yaml"}, true},
		{[]string{"../../testdata/charts/keeps-secret/values.yaml", "testdata/charts/x/values.yaml"}, true},
	} {
		var refused bool
		out := stderrOf(t, func() { refused = refuseMissingValues(c.files) })
		if refused != c.refused || refused != strings.Contains(out, "values file not found") {
			t.Errorf("refuseMissingValues(%q) = %v, %q; want %v", c.files, refused, out, c.refused)
		}
	}
}

// version1 stands in for helm show chart: every chart is version 1.0.0.
func version1(string) (string, error) { return "1.0.0", nil }

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
		out := stderrOf(t, func() { refused = refuseFromVersion(c.chart, c.from, c.fromVersion, version1) })
		if refused != c.refused {
			t.Errorf("refuseFromVersion(%q, %q, %q) = %v, want %v", c.chart, c.from, c.fromVersion, refused, c.refused)
		}
		if refused && !strings.Contains(out, "needs --from") {
			t.Errorf("message %q does not say --from is needed", out)
		}
	}
}

func TestRefuseFromVersionThatALocalFromIsNot(t *testing.T) {
	const v1 = "../../testdata/charts/upgrade-v1"
	var refused bool
	out := stderrOf(t, func() { refused = refuseFromVersion("./chart", v1, "1.2.3", version1) })
	if !refused || !strings.Contains(out, "is version 1.0.0, not --from-version 1.2.3") {
		t.Errorf("refused = %v, %q; want a refusal naming both versions", refused, out)
	}
	out = stderrOf(t, func() {
		refused = refuseFromVersion("./chart", v1, "1.0.0", func(string) (string, error) { return "", errors.New("boom") })
	})
	if !refused || !strings.Contains(out, "could not read the version") {
		t.Errorf("refused = %v, %q; want a refusal when the version cannot be read", refused, out)
	}
	if refuseFromVersion("./chart", v1, "v1.0.0", version1) {
		t.Error("--from-version v1.0.0 was refused for a chart at 1.0.0; helm treats them as the same")
	}
	if refuseFromVersion("./chart", "repo/chart", "1.2.3", version1) {
		t.Error("a remote --from was refused; helm honours --version there")
	}
}

// A traffic flag that cannot work is refused before a cluster exists, not after the control
// traffic fails (#95).
func TestRefuseTraffic(t *testing.T) {
	for _, c := range []struct {
		target, path string
		refused      bool
	}{
		{"", "/", false},
		{"", "/healthz", false},
		{"web:80", "/", false},
		{"web:65535", "/healthz", false},
		{"", "healthz", true},
		{"", "/health z", true},
		{"", "/a\tb", true},
		{"http://web:80", "/", true},
		{"web:", "/", true},
		{"web:0", "/", true},
		{"web:65536", "/", true},
		{"web:http", "/", true},
		{"web:80:90", "/", true},
	} {
		var refused bool
		out := stderrOf(t, func() { refused = refuseTraffic(c.target, c.path) })
		if refused != c.refused {
			t.Errorf("refuseTraffic(%q, %q) = %v, want %v", c.target, c.path, refused, c.refused)
		}
		if refused && out == "" {
			t.Errorf("refuseTraffic(%q, %q) printed no message", c.target, c.path)
		}
	}
}

// A repeated --scenario runs once, in the order first given (#95).
func TestPickOnce(t *testing.T) {
	got, err := pick([]string{"reinstall", "reinstall", "upgrade-without-values"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name() != "reinstall" || got[1].Name() != "upgrade-without-values" {
		t.Errorf("pick = %v, want reinstall then upgrade-without-values", got)
	}
	if _, err := pick([]string{"nope"}); err == nil {
		t.Error("pick accepted an unknown scenario")
	}
}

func TestCheckRotations(t *testing.T) {
	for _, ok := range []string{"*-webhook-tls/*", "hb-upgrade-ca/tls.crt", "*/ca.crt"} {
		if err := checkRotations([]string{ok}); err != nil {
			t.Errorf("checkRotations(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"webhook-tls", "/tls.crt", "ca/", "ns/secret/key", "[ca/tls.crt"} {
		if err := checkRotations([]string{bad}); err == nil {
			t.Errorf("checkRotations(%q) = nil, want an error", bad)
		}
	}
}

// #49 (1): a run cut short by Ctrl-C or SIGTERM never exits 0, whatever the scenarios that finished
// before it said. A finding they proved still exits 1.
func TestExitCode(t *testing.T) {
	for _, c := range []struct {
		worst       string
		interrupted bool
		want        int
	}{
		{scenario.Pass, false, exitClean},
		{scenario.Fail, false, exitFindings},
		{scenario.Inconclusive, false, exitIncomplete},
		{scenario.Pass, true, exitIncomplete},
		{scenario.Fail, true, exitFindings},
		{scenario.Inconclusive, true, exitIncomplete},
	} {
		if got := exitCode(c.worst, c.interrupted); got != c.want {
			t.Errorf("exitCode(%s, interrupted %v) = %d, want %d", c.worst, c.interrupted, got, c.want)
		}
	}
}

// Flags count on both sides of the chart, and nothing else is left over silently (#54).
func TestParseArgs(t *testing.T) {
	for _, c := range []struct {
		args   []string
		chart  string
		ok     bool
		strict bool
		files  string
		out    string
	}{
		{[]string{"./c", "--strict"}, "./c", true, true, "", ""},
		{[]string{"--strict", "./c"}, "./c", true, true, "", ""},
		{[]string{"--report-dir", "out", "./c", "--strict"}, "./c", true, true, "", "out"},
		{[]string{"-f", "a.yaml", "./c", "-f", "b.yaml"}, "./c", true, false, "a.yaml,b.yaml", ""},
		{[]string{"--strict"}, "", true, true, "", ""},
		// A shell glob after one -f: b.yaml would be dropped, and the flags after it.
		{[]string{"./c", "-f", "a.yaml", "b.yaml", "--strict"}, "", false, false, "", ""},
		{[]string{"--strict", "./c", "./d"}, "", false, false, "", ""},
		{[]string{"./c", "--", "--strict"}, "", false, false, "", ""},
		{[]string{"./c", "--no-such-flag"}, "", false, false, "", ""},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		var files multiFlag
		fs.Var(&files, "f", "")
		strict := fs.Bool("strict", false, "")
		out := fs.String("report-dir", "", "")
		var chart string
		var ok bool
		msg := stderrOf(t, func() { chart, ok = parseArgs(fs, c.args) })
		if ok != c.ok || chart != c.chart {
			t.Errorf("parseArgs(%q) = %q, %v; want %q, %v (stderr %q)", c.args, chart, ok, c.chart, c.ok, msg)
			continue
		}
		if ok && (*strict != c.strict || files.String() != c.files || *out != c.out) {
			t.Errorf("parseArgs(%q): strict %v, -f %q, --report-dir %q; want %v, %q, %q", c.args, *strict, files.String(), *out, c.strict, c.files, c.out)
		}
	}
}
