package kube

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Git-Hospital/helmbulance/internal/execx"
)

// fakePodSecurity is a kubectl that lists $FAKE_DIR/pods and answers a dry-run enforce label with
// the warnings in $FAKE_DIR/<level> on stderr, the way the API server does (quoted from a kind
// v0.33.0 cluster). A file fail.<level> makes that call fail. Every call is logged.
const fakePodSecurity = `#!/bin/sh
echo "$*" >> "$FAKE_DIR/calls"
case "$*" in
  *" get pods -o name") cat "$FAKE_DIR/pods" 2>/dev/null; true ;;
  *"pod-security.kubernetes.io/enforce="*)
    all="$*"; level=${all##*enforce=}
    if [ -f "$FAKE_DIR/fail.$level" ]; then echo "connection refused" >&2; exit 1; fi
    if [ -f "$FAKE_DIR/$level" ]; then cat "$FAKE_DIR/$level" >&2; fi
    echo "namespace/ns labeled (server dry run)" ;;
esac
`

func podSecurityClient(t *testing.T, files map[string]string) (*Client, string) {
	t.Helper()
	bin, dir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(fakePodSecurity), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DIR", dir)
	return &Client{Runner: &execx.Runner{}}, dir
}

const (
	restrictedHeader = "Warning: existing pods in namespace \"ns\" violate the new PodSecurity enforce level \"restricted:latest\"\n"
	baselineHeader   = "Warning: existing pods in namespace \"ns\" violate the new PodSecurity enforce level \"baseline:latest\"\n"
)

func TestPodSecurityLevel(t *testing.T) {
	for name, c := range map[string]struct {
		files      map[string]string
		level      string
		violations []string
	}{
		"restricted": {map[string]string{"pods": "pod/app\n"}, "restricted", nil},
		"baseline only": {map[string]string{"pods": "pod/app\n",
			"restricted": restrictedHeader + "Warning: app: seccompProfile\n"}, "baseline", []string{"app: seccompProfile"}},
		"privileged": {map[string]string{"pods": "pod/app\n",
			"restricted": restrictedHeader + "Warning: app: privileged, allowPrivilegeEscalation != false, unrestricted capabilities, runAsNonRoot != true, seccompProfile\n",
			"baseline":   baselineHeader + "Warning: app: privileged\n"}, "privileged", []string{"app: privileged"}},
		"no pods": {map[string]string{}, "", nil},
	} {
		t.Run(name, func(t *testing.T) {
			c2, dir := podSecurityClient(t, c.files)
			level, violations, err := c2.PodSecurityLevel(t.Context(), "ns")
			if err != nil || level != c.level || !reflect.DeepEqual(violations, c.violations) {
				t.Errorf("PodSecurityLevel = %q, %q, %v; want %q, %q", level, violations, err, c.level, c.violations)
			}
			b, _ := os.ReadFile(filepath.Join(dir, "calls"))
			for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				if strings.Contains(line, " label ") && !strings.Contains(line, "--dry-run=server") {
					t.Errorf("a label call without --dry-run=server: %s", line)
				}
			}
		})
	}
}

// A kubectl that fails is an error, never a level.
func TestPodSecurityLevelError(t *testing.T) {
	for _, failing := range []string{"restricted", "baseline"} {
		c, _ := podSecurityClient(t, map[string]string{"pods": "pod/app\n", "fail." + failing: "",
			"restricted": restrictedHeader + "Warning: app: seccompProfile\n"})
		if level, _, err := c.PodSecurityLevel(t.Context(), "ns"); err == nil || level != "" {
			t.Errorf("%s fails: level %q, err %v; want an error and no level", failing, level, err)
		}
	}
}
