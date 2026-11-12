package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Git-Hospital/helmbulance/internal/execx"
	"github.com/Git-Hospital/helmbulance/internal/scenario"
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
		// The ref is printed with its credentials hidden, refused or not.
		{false, []string{"https://bob:hunter2@charts.example/x.tgz"}, true, "https://***@charts.example/x.tgz is not a chart on disk"},
		{true, []string{"https://bob:hunter2@charts.example/x.tgz"}, false, "warning: running https://***@charts.example/x.tgz"},
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

// Only the tools found on PATH pass; the message names the missing ones, before any cluster (#58).
func TestRefuseMissingTools(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"kind", "helm"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	for _, c := range []struct {
		tools   []string
		refused bool
		missing string
	}{
		{[]string{"helm"}, false, ""},
		{[]string{"kind", "helm"}, false, ""},
		{[]string{"kind", "helm", "kubectl"}, true, "not found in PATH: kubectl."},
		{[]string{"kubectl", "oc"}, true, "not found in PATH: kubectl, oc."},
	} {
		var refused bool
		out := stderrOf(t, func() { refused = refuseMissingTools(c.tools...) })
		if refused != c.refused || !strings.Contains(out, c.missing) || (!refused && out != "") {
			t.Errorf("refuseMissingTools(%q) = %v, %q; want %v with %q", c.tools, refused, out, c.refused, c.missing)
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

// chart-upgrade named on the command line (or in the Action's scenarios input) with no
// --from-version would test nothing: it is SKIPPED, which counts for nothing, so a run whose other
// scenarios pass would exit 0 and look as if the upgrade was tested. It is refused before any
// cluster starts. Without --scenario the default set leaves chart-upgrade out quietly, as before.
func TestRefuseChartUpgradeWithoutFromVersion(t *testing.T) {
	for _, c := range []struct {
		only        []string
		fromVersion string
		refused     bool
	}{
		{nil, "", false},
		{[]string{"upgrade-without-values"}, "", false},
		{[]string{"upgrade-without-values", "chart-upgrade"}, "", true},
		{[]string{"chart-upgrade"}, "", true},
		{[]string{"chart-upgrade"}, "1.0.0", false},
	} {
		var refused bool
		out := stderrOf(t, func() { refused = refuseChartUpgradeWithoutFromVersion(c.only, c.fromVersion) })
		if refused != c.refused || refused != strings.Contains(out, "--from-version") {
			t.Errorf("refuseChartUpgradeWithoutFromVersion(%q, %q) = %v, %q; want %v", c.only, c.fromVersion, refused, out, c.refused)
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
		{"", "", false},
		{"web:80", "", false},
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

// A refusal names the value it rejected, but never the token a query can carry.
func TestRefuseTrafficRedactsTokens(t *testing.T) {
	for _, c := range []struct{ target, path string }{
		{"", "health?token=hunter2"},
		{"web:80?token=hunter2", "/"},
	} {
		var refused bool
		out := stderrOf(t, func() { refused = refuseTraffic(c.target, c.path) })
		if !refused {
			t.Fatalf("refuseTraffic(%q, %q) = false, want a refusal", c.target, c.path)
		}
		if !strings.Contains(out, "token=***") || strings.Contains(out, "hunter2") {
			t.Errorf("refuseTraffic(%q, %q) = %q, want the token hidden", c.target, c.path, out)
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

// A values file passed with -f can hold the same secrets as --set, and helm quotes them back in
// schema errors, a chart's fail message or a Kubernetes "Invalid value" (#58 reviews, N3). Values
// under a key that names a credential or TLS material are redacted like --set values; the rest
// of the file is not, so image names and hosts stay readable in error text.
func TestFileSecrets(t *testing.T) {
	dir := t.TempDir()
	values := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(values, []byte(`
image: nginx-1.27
image: nginx-1.28
1: one
db:
  host: db.example.internal
  password: FileSecret-7f3a91
access:
  users:
    - name: alice
      token: tok-0a1b2c3d
auth:
  username: admin-user
  existingSecret: my-release-pg
  secretKeys:
    userPasswordKey: user-password
    password: pg-password-entry
  tlsSecretName: my-tls-cert
  passwordFile: /run/secrets/db-pass
  rootPasswordKey: root-password-key
apiToken: 12345678
apiKey: 0x1F2E3D4C
secretKey: aws-secret-0a1b
tls:
  key: |
    -----BEGIN PRIVATE KEY-----
    MIIEvQ
    -----END PRIVATE KEY-----
passwordUpdateJob:
  image:
    tag: latest-1001
extraEnv:
  - name: DB_PASSWORD
    value: EnvSecret-55aa
  - name: LOG_LEVEL
    value: debug-level
featureFlags:
  enabled: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.yaml")
	if err := os.WriteFile(other, []byte("password: OtherFile-9e8d\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := fileSecrets([]string{values, "-", other + ",https://charts.example/values.yaml", filepath.Join(dir, "missing.yaml")})
	for _, want := range []string{
		"FileSecret-7f3a91", "tok-0a1b2c3d", "EnvSecret-55aa", "aws-secret-0a1b", "OtherFile-9e8d",
		"-----BEGIN PRIVATE KEY-----\nMIIEvQ\n-----END PRIVATE KEY-----\n",
		// As written, and as helm prints a number it read as float64.
		"12345678", "1.2345678e+07", "0x1F2E3D4C", "5.23124044e+08",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("fileSecrets is missing %q: %q", want, got)
		}
	}
	for _, not := range []string{
		"nginx-1.28", "db.example.internal", "alice", "admin-user", "my-release-pg", "user-password",
		"root-password-key", "pg-password-entry", "my-tls-cert", "/run/secrets/db-pass", "latest-1001", "debug-level", "true", "one",
	} {
		if slices.Contains(got, not) {
			t.Errorf("fileSecrets hides %q, which is not a secret: %q", not, got)
		}
	}
	if text := execx.Redact("password FileSecret-7f3a91 rejected", execx.SecretValues(got)); strings.Contains(text, "FileSecret") {
		t.Errorf("Redact left the value: %q", text)
	}
	// helm and Kubernetes quote a value in their errors: the PEM key with escaped line breaks.
	quotedPEM := strconv.Quote("-----BEGIN PRIVATE KEY-----\nMIIEvQ\n-----END PRIVATE KEY-----\n")
	if text := execx.Redact("invalid value "+quotedPEM, execx.SecretValues(got)); strings.Contains(text, "MIIEvQ") {
		t.Errorf("Redact left the quoted key: %q", text)
	}
}

// A summary can carry a chart's fail message. Printed raw, its newline starts a line that the
// Action's log reads as a workflow command, and its escape sequences reach the terminal (#58).
func TestOneLine(t *testing.T) {
	cases := map[string]string{
		"plain summary":                              "plain summary",
		"upgrade failed\n::warning::from chart":      "upgrade failed ::warning::from chart",
		"a\r\nb\tc":                                  "a  b c",
		"title\x1b]0;x\x07 and \x1b[2Jclear":         "title]0;x and [2Jclear",
		"c1 \u009b31m control":                       "c1 31m control",
		"del\x7f and nul\x00":                        "del and nul",
		"bidi \u202egnp.exe\u202c and \u2066x\u2069": "bidi gnp.exe and x",
		"line\u2028sep":                              "line sep",
		// The runner's older command form is read anywhere in a line, not only at its start.
		"x ##[error]from chart": "x ## [error]from chart",
		// Other line breaks become spaces too, so words do not run together.
		"a\u0085b\vc\fd": "a b c d",
		// A byte that is not UTF-8, as a summary cut at 600 bytes can end with, is replaced.
		"cut \xe2\x9c":          "cut \ufffd\ufffd",
		"unicode stays: çğış ✓": "unicode stays: çğış ✓",
	}
	for in, want := range cases {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// Review of #122: a list under a credential key is a list of credentials (bitnami/mongodb's
// auth.passwords), and values keys such as a toleration's key, topologyKey, externalUrl and
// authType are not credentials, so a Kubernetes "Invalid value" about them keeps the value.
func TestFileSecretsListsAndCommonValuesKeys(t *testing.T) {
	values := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(values, []byte(`
auth:
  passwords: [MongoPw-in-a-list]
  authType: basic-auth
apiKeys:
  - ApiKey-in-a-list
privateKey: PrivKey-77aa
tolerations:
  - key: node-role.kubernetes.io/control-plane
topologySpreadConstraints:
  - topologyKey: kubernetes.io/hostname
externalUrl: https://grafana.example.com
connectionTimeout: 30s-timeout
ca:
  key: CaKey-0c0c
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := fileSecrets([]string{values})
	for _, want := range []string{"MongoPw-in-a-list", "ApiKey-in-a-list", "PrivKey-77aa", "CaKey-0c0c"} {
		if !slices.Contains(got, want) {
			t.Errorf("fileSecrets is missing %q: %q", want, got)
		}
	}
	for _, not := range []string{"node-role.kubernetes.io/control-plane", "kubernetes.io/hostname", "https://grafana.example.com", "basic-auth", "30s-timeout"} {
		if slices.Contains(got, not) {
			t.Errorf("fileSecrets hides %q, which is not a secret: %q", not, got)
		}
	}
}

// Second review of #122, checked with Helm v4.3.0: helm 4 merges every document of a values file,
// resolves aliases and merge keys, and reads 012345670 as octal, printing 2.739128e+06. Key names
// that hold a key themselves leak unless they are named, and ssl.key is TLS like tls.key.
func TestFileSecretsDocumentsAliasesKeysAndOctal(t *testing.T) {
	values := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(values, []byte(`
shared: &pw AliasSecret-3c3c
base: &base
  password: MergedSecret-4d4d
db:
  password: *pw
  <<: *base
airflow:
  fernetKey: Fernet-5e5e
  jwtKey: Jwt-6f6f
storage:
  accountKey: Account-7a7a
  connectionString: Conn-8b8b
ssl:
  key: SslKey-9c9c
env:
  - name: STRIPE_KEY
    value: Stripe-0d0d
  - name: SORT_ORDER
    value: ascending-order
pin:
  apiToken: 012345670
auth:
  userPasswordKey: user-password-entry
  adminTokenKey: admin-token-entry
tolerations:
  - key: dedicated-node
---
db:
  password: SecondDoc-1e1e
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := fileSecrets([]string{values})
	for _, want := range []string{
		"AliasSecret-3c3c", "MergedSecret-4d4d", "Fernet-5e5e", "Jwt-6f6f", "Account-7a7a", "Conn-8b8b",
		"SslKey-9c9c", "Stripe-0d0d", "SecondDoc-1e1e", "012345670", "2.739128e+06", "2739128",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("fileSecrets is missing %q: %q", want, got)
		}
	}
	for _, not := range []string{"12345670", "1.234567e+07", "ascending-order", "user-password-entry", "admin-token-entry", "dedicated-node"} {
		if slices.Contains(got, not) {
			t.Errorf("fileSecrets hides %q, which is not a secret or not how helm prints it: %q", not, got)
		}
	}
}

// After an interrupt no further scenario starts: each one left is INCONCLUSIVE "not run", so the
// sandbox cluster is deleted at once instead of after every remaining scenario's cleanup (#58).
func TestRunEachStopsStartingAfterAnInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var ran []string
	names := []string{"a", "b", "c"}
	got := runEach(ctx, names, func(i int) scenario.Result {
		ran = append(ran, names[i])
		if names[i] == "a" {
			cancel() // the interrupt arrives while a runs
		}
		return scenario.Result{Scenario: names[i], Verdict: scenario.Pass}
	})
	if !reflect.DeepEqual(ran, []string{"a"}) {
		t.Errorf("ran %v, want only a", ran)
	}
	if len(got) != 3 || got[0].Verdict != scenario.Pass {
		t.Fatalf("results %+v, want a's own result first and one per name", got)
	}
	for _, r := range got[1:] {
		if r.Verdict != scenario.Inconclusive || !strings.Contains(r.Summary, "not run") {
			t.Errorf("%s: %+v, want INCONCLUSIVE not run", r.Scenario, r)
		}
	}
}

// An unsupported or empty --k8s is a usage error before any cluster starts (#56).
func TestCheckVersions(t *testing.T) {
	for _, good := range []string{"1.34, 1.37", "1.36,"} {
		if err := checkVersions(good); err != nil {
			t.Errorf("checkVersions(%q): %v", good, err)
		}
	}
	for _, bad := range []string{"1.34,1.30", "", " , "} {
		if err := checkVersions(bad); err == nil {
			t.Errorf("checkVersions(%q) = nil, want an error", bad)
		}
	}
}

func TestMatrixRefusesAnUnsupportedVersionBeforeAnyCluster(t *testing.T) {
	if code := matrixCmd([]string{"--k8s", "1.29", t.TempDir()}); code != exitError {
		t.Errorf("exit %d, want %d", code, exitError)
	}
}
