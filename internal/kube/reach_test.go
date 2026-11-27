package kube

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/helmbulance/internal/execx"
)

// fakeReach is a kubectl for Reachable. The n-th exec prints $FAKE_DIR/round.<n> and logs its
// script to $FAKE_DIR/script.<n>. A file fail.<word> makes any call containing that word fail.
const fakeReach = `#!/bin/sh
echo "$*" >> "$FAKE_DIR/calls"
for f in "$FAKE_DIR"/fail.*; do
  [ -e "$f" ] || continue
  w=${f##*/fail.}
  case "$*" in *"$w"*) echo "fake kubectl: $w failed" >&2; exit 1 ;; esac
done
case "$*" in
  *" exec client "*)
    n=$(cat "$FAKE_DIR/n" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$FAKE_DIR/n"
    for a in "$@"; do last=$a; done; printf '%s' "$last" > "$FAKE_DIR/script.$n"
    cat "$FAKE_DIR/round.$n" ;;
esac
exit 0
`

func reachClient(t *testing.T, files map[string]string) (*Client, string) {
	t.Helper()
	tries, interval := reachTries, reachInterval
	t.Cleanup(func() { reachTries, reachInterval = tries, interval })
	reachTries, reachInterval = 3, time.Millisecond
	bin, dir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(fakeReach), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DIR", dir)
	return &Client{Runner: &execx.Runner{}}, dir
}

func reachDeletedItsNamespace(t *testing.T, dir string) {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, "calls"))
	var created string
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "create namespace "); i >= 0 {
			created = strings.Fields(line[i+len("create namespace "):])[0]
		}
	}
	if !strings.HasPrefix(created, "hb-reach-") || !strings.Contains(string(b), "delete namespace "+created+" ") {
		t.Errorf("created %q; want an hb-reach- namespace deleted afterwards; calls:\n%s", created, b)
	}
}

var (
	redis = netip.MustParseAddrPort("10.244.0.7:6379")
	web   = netip.MustParseAddrPort("10.244.0.8:8080")
	admin = netip.MustParseAddrPort("10.244.0.8:9090")
)

// A target that opens in a later round is reachable; only the closed ones are probed again, and a
// target closed in every round is unreachable.
func TestReachable(t *testing.T) {
	c, dir := reachClient(t, map[string]string{
		"round.1": "0 open\n1 closed\n2 closed\n",
		"round.2": "0 open\n1 closed\n", // the closed two, in their new order
		"round.3": "0 closed\n",
	})
	got, err := c.Reachable(t.Context(), "busybox", []netip.AddrPort{redis, web, admin})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[netip.AddrPort]bool{redis: true, web: true, admin: false}; !reflect.DeepEqual(got, want) {
		t.Errorf("Reachable = %v, want %v", got, want)
	}
	for n, want := range map[string]string{
		"1": "nc -w 2 10.244.0.7 6379 </dev/null",
		"2": "nc -w 2 10.244.0.8 8080 </dev/null >/dev/null 2>&1; then echo '0 open'",
		"3": "nc -w 2 10.244.0.8 9090 </dev/null >/dev/null 2>&1; then echo '0 open'",
	} {
		script, _ := os.ReadFile(filepath.Join(dir, "script."+n))
		if !strings.Contains(string(script), want) {
			t.Errorf("round %s script %q does not contain %q", n, script, want)
		}
	}
	if s, _ := os.ReadFile(filepath.Join(dir, "script.3")); strings.Contains(string(s), "6379") || strings.Contains(string(s), "8080") {
		t.Errorf("round 3 probed an address that had opened: %q", s)
	}
	reachDeletedItsNamespace(t, dir)
}

// Once everything is open, no further round runs.
func TestReachableStopsWhenAllOpen(t *testing.T) {
	c, dir := reachClient(t, map[string]string{"round.1": "0 open\n1 open\n"})
	if _, err := c.Reachable(t.Context(), "busybox", []netip.AddrPort{redis, web}); err != nil {
		t.Fatal(err)
	}
	if n, _ := os.ReadFile(filepath.Join(dir, "n")); strings.TrimSpace(string(n)) != "1" {
		t.Errorf("%s rounds, want 1", n)
	}
}

func TestReachableNothingToProbe(t *testing.T) {
	c, dir := reachClient(t, nil)
	if got, err := c.Reachable(t.Context(), "busybox", nil); err != nil || len(got) != 0 {
		t.Errorf("Reachable(nil) = %v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "calls")); err == nil {
		t.Error("kubectl was called for no targets")
	}
}

// A kubectl error or an answer that does not fit is an error, never "closed"; the namespace is
// still deleted. Later rounds answer, so the error is the bad round's own.
func TestReachableErrors(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"exec fails":       {"fail.exec": ""},
		"pod never runs":   {"fail.wait": ""},
		"missing target":   {"round.1": "0 open\n", "round.2": "0 closed\n", "round.3": "0 closed\n"},
		"unknown index":    {"round.1": "0 open\n5 open\n"},
		"same index twice": {"round.1": "0 open\n0 closed\n1 open\n", "round.2": "0 closed\n", "round.3": "0 closed\n"},
		"other words":      {"round.1": "0 open\n1 maybe\n", "round.2": "0 closed\n", "round.3": "0 closed\n"},
		"not our output":   {"round.1": "OCI runtime exec failed\n"},
	} {
		t.Run(name, func(t *testing.T) {
			c, dir := reachClient(t, files)
			if got, err := c.Reachable(t.Context(), "busybox", []netip.AddrPort{redis, web}); err == nil {
				t.Errorf("Reachable = %v with no error", got)
			}
			reachDeletedItsNamespace(t, dir)
		})
	}
}

func TestReachableCleansUpAfterCancel(t *testing.T) {
	c, dir := reachClient(t, map[string]string{"round.1": "0 closed\n"})
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		for ctx.Err() == nil {
			if b, _ := os.ReadFile(filepath.Join(dir, "calls")); strings.Contains(string(b), "create namespace") {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	if _, err := c.Reachable(ctx, "busybox", []netip.AddrPort{redis}); err == nil {
		t.Error("a cancelled probe returned no error")
	}
	reachDeletedItsNamespace(t, dir)
}

func TestProbeScriptIPv6(t *testing.T) {
	s := probeScript([]netip.AddrPort{netip.MustParseAddrPort("[fd00:10:244::7]:5432")})
	if !strings.Contains(s, "nc -w 2 fd00:10:244::7 5432 ") {
		t.Errorf("probeScript = %q, want the bare IPv6 address", s)
	}
}
