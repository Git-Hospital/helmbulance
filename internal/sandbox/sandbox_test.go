package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egezamb/helmbulance/internal/execx"
)

const (
	name      = "helmbulance-1a2b3c4d"
	sandbox   = "kind-" + name
	sandboxCA = "U0FOREJPWC1DQQ=="
	otherCA   = "T1RIRVItQ0E="
)

// pair is a named kubeconfig entry and the one value of it that Open reads: a cluster's CA, or
// the cluster a context points at.
type pair struct{ name, value string }

// config writes a kubeconfig in the layout kind uses, with one user for every context.
func config(current string, clusters, contexts []pair) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\nclusters:\n")
	for i, c := range clusters {
		fmt.Fprintf(&b, "- cluster:\n    certificate-authority-data: %s\n    server: https://127.0.0.1:%d\n  name: %s\n", c.value, 41111+i, c.name)
	}
	b.WriteString("contexts:\n")
	for _, c := range contexts {
		fmt.Fprintf(&b, "- context:\n    cluster: %s\n    user: %s\n  name: %s\n", c.value, sandbox, c.name)
	}
	fmt.Fprintf(&b, "current-context: %s\nkind: Config\npreferences: {}\nusers:\n- name: %s\n  user:\n    client-certificate-data: Q0VSVA==\n    client-key-data: S0VZ\n", current, sandbox)
	return b.String()
}

// kindConfig is the file kind writes: one cluster, one context, one user.
var kindConfig = config(sandbox, []pair{{sandbox, sandboxCA}}, []pair{{sandbox, sandbox}})

// merged has another cluster and context added after the sandbox's, as kubectl config
// set-cluster or aws eks update-kubeconfig would add them, with current-context set to current.
func merged(other, current string) string {
	return config(current,
		[]pair{{sandbox, sandboxCA}, {other, otherCA}},
		[]pair{{sandbox, sandbox}, {other, other}})
}

// writeCluster lays out the state directory of a cluster created from kindConfig, then
// replaces its kubeconfig with kc.
func writeCluster(t *testing.T, kc string) {
	t.Helper()
	t.Setenv("HELMBULANCE_STATE_DIR", t.TempDir())
	c, err := clusterFiles(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Kubeconfig, []byte(kindConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.record(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Kubeconfig, []byte(kc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// refused fails the test unless Open refuses kc with an error that says why.
func refused(t *testing.T, kc, why string) {
	t.Helper()
	writeCluster(t, kc)
	if _, err := Open(name); err == nil || !strings.Contains(err.Error(), why) {
		t.Errorf("Open = %v, want an error with %q, for:\n%s", err, why, kc)
	}
}

func TestOpenAcceptsTheFileKindWrote(t *testing.T) {
	writeCluster(t, kindConfig)
	c, err := Open(name)
	if err != nil {
		t.Fatalf("Open(%q) = %v, want the sandbox", name, err)
	}
	if c.Name != name {
		t.Errorf("Open(%q).Name = %q", name, c.Name)
	}
}

// The current context decides where helm and kubectl go, so it is what must be the sandbox (#47).
func TestOpenRefusesACurrentContextOnAnotherCluster(t *testing.T) {
	for _, other := range []string{
		"kind-prod",      // sorts after the sandbox: the first CA line is still the sandbox's
		"docker-desktop", // sorts first
		"arn:aws:eks:eu-central-1:123456789012:cluster/prod",
	} {
		refused(t, merged(other, other), "current-context")
	}
}

// Another cluster in the file is harmless while the current context is still the sandbox's.
func TestOpenAcceptsAMergedFileStillOnTheSandbox(t *testing.T) {
	writeCluster(t, merged("kind-prod", sandbox))
	if _, err := Open(name); err != nil {
		t.Errorf("Open = %v, want the sandbox", err)
	}
}

// The context has the sandbox's name but points at the other cluster entry, whose CA is checked.
func TestOpenRefusesTheSandboxContextOnAnotherCluster(t *testing.T) {
	kc := config(sandbox,
		[]pair{{sandbox, sandboxCA}, {"kind-prod", otherCA}},
		[]pair{{sandbox, "kind-prod"}})
	refused(t, kc, "does not match")
}

func TestOpenRefusesAChangedCA(t *testing.T) {
	refused(t, config(sandbox, []pair{{sandbox, otherCA}}, []pair{{sandbox, sandbox}}), "does not match")
}

// A name that is missing or given twice leaves it unclear what helm and kubectl would use.
func TestOpenRefusesAmbiguousEntries(t *testing.T) {
	for _, kc := range []string{
		config(sandbox, []pair{{sandbox, sandboxCA}}, []pair{{sandbox, sandbox}, {sandbox, sandbox}}),
		config(sandbox, []pair{{sandbox, sandboxCA}, {sandbox, sandboxCA}}, []pair{{sandbox, sandbox}}),
		config(sandbox, []pair{{sandbox, sandboxCA}}, nil),
		config(sandbox, []pair{{"kind-prod", sandboxCA}}, []pair{{sandbox, sandbox}}),
	} {
		refused(t, kc, "want once")
	}
}

func TestOpenRefusesAClusterWithoutCAData(t *testing.T) {
	refused(t, config(sandbox, []pair{{sandbox, `""`}}, []pair{{sandbox, sandbox}}), "no certificate-authority-data")
}

// With TLS verification off, the CA no longer ties the file to the sandbox's server.
func TestOpenRefusesInsecureSkipTLSVerify(t *testing.T) {
	kc := strings.Replace(kindConfig, "    server: https://", "    insecure-skip-tls-verify: true\n    server: https://", 1)
	if kc == kindConfig {
		t.Fatal("the fixture did not change")
	}
	refused(t, kc, "insecure-skip-tls-verify")
}

func TestOpenRefusesAFileThatIsNotYAML(t *testing.T) {
	writeCluster(t, "clusters: [\n")
	if _, err := Open(name); err == nil {
		t.Error("Open accepted a kubeconfig that does not parse")
	}
}

func TestOpenRefusesAClusterWithNoRecord(t *testing.T) {
	writeCluster(t, kindConfig)
	c, _ := clusterFiles(name)
	if err := os.Remove(filepath.Join(c.dir, "ca.sha256")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(name); err == nil || !strings.Contains(err.Error(), "no record") {
		t.Errorf("Open without ca.sha256 = %v, want a 'no record' error", err)
	}
}

// A name is only ever one that NewCluster makes, so it cannot leave the clusters directory.
func TestOpenRefusesNamesNewClusterNeverMakes(t *testing.T) {
	t.Setenv("HELMBULANCE_STATE_DIR", t.TempDir())
	for _, n := range []string{
		"kind-prod",
		"helmbulance-",
		"helmbulance-1a2b3c4d/../../elsewhere",
		`helmbulance-1a2b3c4d\..\elsewhere`,
		"helmbulance-1A2B3C4D",
		"helmbulance-1a2b3c4d5",
	} {
		if _, err := Open(n); err == nil || !strings.Contains(err.Error(), "was not created by helmbulance") {
			t.Errorf("Open(%q) = %v, want a 'was not created by helmbulance' error", n, err)
		}
	}
}

func TestNewClusterNamesPassOpensCheck(t *testing.T) {
	t.Setenv("HELMBULANCE_STATE_DIR", t.TempDir())
	c, err := NewCluster()
	if err != nil {
		t.Fatal(err)
	}
	if !nameRE.MatchString(c.Name) {
		t.Errorf("NewCluster made %q, which Open would refuse", c.Name)
	}
}

// fakeKind puts a kind first on PATH that logs its arguments to the returned file. On create it
// writes kubeconfig where --kubeconfig says and exits with createExit.
func fakeKind(t *testing.T, kubeconfig string, createExit int) string {
	t.Helper()
	bin, calls := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	kc := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kc, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> "$FAKE_CALLS"
if [ "$1" = create ]; then
  while [ $# -gt 0 ]; do [ "$1" = --kubeconfig ] && out=$2; shift; done
  cp "$FAKE_KUBECONFIG" "$out"
  exit %d
fi
`, createExit)
	if err := os.WriteFile(filepath.Join(bin, "kind"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_CALLS", calls)
	t.Setenv("FAKE_KUBECONFIG", kc)
	t.Setenv("HELMBULANCE_STATE_DIR", t.TempDir())
	return calls
}

// createFails runs Create against the fake kind and checks that it failed with an error that says
// why, deleted the cluster and removed its state directory: the caller never gets a cluster to
// delete, so anything left would keep running.
func createFails(t *testing.T, calls, why string) {
	t.Helper()
	c, err := NewCluster()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Create(t.Context(), &execx.Runner{}, ""); err == nil || !strings.Contains(err.Error(), why) {
		t.Fatalf("Create = %v, want an error with %q", err, why)
	}
	b, _ := os.ReadFile(calls)
	if !strings.Contains(string(b), "delete cluster --name "+c.Name) {
		t.Errorf("kind calls:\n%s\nwant a delete of %s", b, c.Name)
	}
	if _, err := os.Stat(c.dir); !os.IsNotExist(err) {
		t.Errorf("state directory %s is still there (%v)", c.dir, err)
	}
}

// kind made the cluster, but its kubeconfig cannot be recorded.
func TestCreateDeletesAClusterItCannotRecord(t *testing.T) {
	createFails(t, fakeKind(t, merged("kind-prod", "kind-prod"), 0), "current-context")
}

// kind itself failed, for example killed by the SIGTERM a Ctrl-C sends to its process group,
// after its node container had started (#54).
func TestCreateDeletesTheClusterWhenKindFails(t *testing.T) {
	createFails(t, fakeKind(t, kindConfig, 1), "exit status 1")
}
