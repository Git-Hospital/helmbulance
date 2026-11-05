package kube

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/helmbulance/internal/execx"
)

// fakeCanary is a kubectl for EnforcesNetworkPolicy. A probe prints "open" until the deny-all
// policy is applied; after it, "closed" when $FAKE_DIR/enforce exists, else "open" still. With
// $FAKE_DIR/neveropen it is always "closed". A file
// fail.<word> makes any call containing that word fail. Every call is logged to $FAKE_DIR/calls.
const fakeCanary = `#!/bin/sh
echo "$*" >> "$FAKE_DIR/calls"
for f in "$FAKE_DIR"/fail.*; do
  [ -e "$f" ] || continue
  w=${f##*/fail.}
  case "$*" in *"$w"*) echo "fake kubectl: $w failed" >&2; exit 1 ;; esac
done
case "$*" in
  *" apply -f "*) touch "$FAKE_DIR/applied" ;;
  *"jsonpath={.status.podIP}"*) printf 10.244.0.7 ;;
  *" exec client "*)
    if [ -f "$FAKE_DIR/neveropen" ]; then echo closed
    elif [ -f "$FAKE_DIR/applied" ] && [ -f "$FAKE_DIR/enforce" ]; then echo closed; else echo open; fi ;;
esac
exit 0
`

func canaryClient(t *testing.T, files ...string) (*Client, string) {
	t.Helper()
	tries, interval := canaryTries, canaryInterval
	t.Cleanup(func() { canaryTries, canaryInterval = tries, interval })
	canaryTries, canaryInterval = 3, time.Millisecond
	bin, dir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(fakeCanary), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DIR", dir)
	return &Client{Runner: &execx.Runner{}}, filepath.Join(dir, "calls")
}

// deletedItsNamespace checks that the namespace the canary created is the one it deleted.
func deletedItsNamespace(t *testing.T, calls string) {
	t.Helper()
	b, _ := os.ReadFile(calls)
	var created string
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "create namespace "); i >= 0 {
			created = strings.Fields(line[i+len("create namespace "):])[0]
		}
	}
	if !strings.HasPrefix(created, "hb-canary-") || !strings.Contains(string(b), "delete namespace "+created+" ") {
		t.Errorf("created %q; want an hb-canary- namespace deleted afterwards; calls:\n%s", created, b)
	}
}

func TestEnforcesNetworkPolicy(t *testing.T) {
	for name, c := range map[string]struct {
		files []string
		want  bool
	}{
		"enforced":     {[]string{"enforce"}, true},
		"not enforced": {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			client, calls := canaryClient(t, c.files...)
			got, err := client.EnforcesNetworkPolicy(t.Context(), "busybox")
			if err != nil || got != c.want {
				t.Errorf("EnforcesNetworkPolicy = %v, %v; want %v", got, err, c.want)
			}
			deletedItsNamespace(t, calls)
		})
	}
}

// A kubectl error is an error, never "not enforced"; and the namespace is still deleted.
func TestEnforcesNetworkPolicyError(t *testing.T) {
	for _, failing := range []string{"exec", "apply", "wait", "serviceaccount"} {
		client, calls := canaryClient(t, "fail."+failing)
		if got, err := client.EnforcesNetworkPolicy(t.Context(), "busybox"); err == nil {
			t.Errorf("%s fails: %v with no error; want an error", failing, got)
		}
		deletedItsNamespace(t, calls)
	}
}

// After a cancel the canary still deletes what it created.
func TestEnforcesNetworkPolicyCleansUpAfterCancel(t *testing.T) {
	client, calls := canaryClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	// Cancel once the namespace exists: the fake logs the create before the next call starts.
	go func() {
		for ctx.Err() == nil {
			if b, _ := os.ReadFile(calls); strings.Contains(string(b), "create namespace") {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	if _, err := client.EnforcesNetworkPolicy(ctx, "busybox"); err == nil {
		t.Error("a cancelled canary returned no error")
	}
	deletedItsNamespace(t, calls)
}

// A client that cannot reach the server with no policy in place proves nothing about policies: an
// error, not "enforced".
func TestEnforcesNetworkPolicyNeedsAWorkingControl(t *testing.T) {
	client, calls := canaryClient(t, "neveropen", "enforce")
	if got, err := client.EnforcesNetworkPolicy(t.Context(), "busybox"); err == nil {
		t.Errorf("EnforcesNetworkPolicy = %v with no error; want an error", got)
	}
	if b, _ := os.ReadFile(calls); strings.Contains(string(b), " apply -f ") {
		t.Error("the policy was applied after a failed control")
	}
	deletedItsNamespace(t, calls)
}
