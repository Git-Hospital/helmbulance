package kube

import (
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/helmbulance/internal/execx"
)

// Lines as a 6.x kernel prints them. In tcp: 0.0.0.0:80 and 127.0.0.1:9000 listening, and an
// established connection and a TIME_WAIT one on 80. In tcp6: :::5432, ::1:6379, the IPv4-mapped
// form of 127.0.0.1:9000 again, and an established socket.
const socketTables = `== tcp
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 31000 1 0000000000000000 100 0 0 10 0
   1: 0100007F:2328 00000000:0000 0A 00000000:00000000 00:00000000 00000000   101        0 31001 1 0000000000000000 100 0 0 10 0
   2: 0A0000F4:0050 0A0000F5:C350 01 00000000:00000000 02:0000A4B2 00000000     0        0 31002 2 0000000000000000 20 4 30 10 -1
   3: 0A0000F4:0050 0A0000F5:C351 06 00000000:00000000 03:00001773 00000000     0        0 0 3 0000000000000000
== tcp6
  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:1538 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000   999        0 32000 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000001000000:18EB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000   999        0 32001 1 0000000000000000 100 0 0 10 0
   2: 0000000000000000FFFF00000100007F:2328 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000   101        0 32002 1 0000000000000000 100 0 0 10 0
   3: 0000000000000000FFFF0000F400000A:1538 0000000000000000FFFF0000F500000A:D431 01 00000000:00000000 00:00000000 00000000   999        0 32003 1 0000000000000000 20 4 30 10 -1
`

func TestParseListeners(t *testing.T) {
	got, err := parseListeners(socketTables)
	if err != nil {
		t.Fatal(err)
	}
	want := []Listener{
		{netip.MustParseAddr("0.0.0.0"), 80},
		{netip.MustParseAddr("::"), 5432},
		{netip.MustParseAddr("::1"), 6379},
		{netip.MustParseAddr("127.0.0.1"), 9000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseListeners = %v, want %v", got, want)
	}
	var loopback []string
	for _, l := range got {
		if l.Loopback() {
			loopback = append(loopback, l.String())
		}
	}
	if want := []string{"[::1]:6379", "127.0.0.1:9000"}; !reflect.DeepEqual(loopback, want) {
		t.Errorf("loopback listeners = %v, want %v", loopback, want)
	}
}

func TestParseListenersEdges(t *testing.T) {
	// No tcp6 table (a node without IPv6) and nothing listening: no listeners, no error.
	if got, err := parseListeners("== tcp\n  sl  local_address rem_address   st\n== tcp6\n"); err != nil || len(got) != 0 {
		t.Errorf("empty tables = %v, %v; want none and no error", got, err)
	}
	for _, bad := range []string{
		"",                                     // the container printed nothing
		"error: something else\n",              // not the script's output
		"   0: 00000000:0050 00000000:0000 0A", // a socket line before any table header
		"== tcp\n   0: 000000:0050 00000000:0000 0A",                 // a short address
		"== tcp\n   0: 00000000:ZZ50 00000000:0000 0A",               // a port that is not hex
		"== tcp6\n   0: 00000000:0050 00000000:0000 0A",              // an IPv4 address in tcp6
		"== tcp\n   0: 00000000000000000000000000000000:0050 x:0 0A", // an IPv6 address in tcp
	} {
		if got, err := parseListeners(bad); err == nil {
			t.Errorf("parseListeners(%q) = %v, want an error", bad, got)
		}
	}
}

// Listeners adds a restricted ephemeral container running as nobody, waits for it to finish and
// reads its log.
// The script exits 0 when only tcp6 is missing, as on a node booted with ipv6.disable=1, and 1
// when tcp is (review of #181). It runs here with /proc/net swapped for a directory of fakes.
func TestListenersScriptOnMissingTables(t *testing.T) {
	for name, c := range map[string]struct {
		files  []string
		exit   int
		header string
	}{
		"both":    {[]string{"tcp", "tcp6"}, 0, "== tcp6"},
		"no tcp6": {[]string{"tcp"}, 0, "== tcp6"},
		"no tcp":  {[]string{"tcp6"}, 1, "== tcp"},
		"neither": {nil, 1, "== tcp"},
	} {
		dir := t.TempDir()
		for _, f := range c.files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("  sl  local_address\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		script := strings.ReplaceAll(listenersScript, "/proc/net/", dir+"/")
		out, err := exec.Command("sh", "-c", script).CombinedOutput()
		exit := 0
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if exit != c.exit || !strings.Contains(string(out), c.header) {
			t.Errorf("%s: exit %d, output %q; want exit %d and %q", name, exit, out, c.exit, c.header)
		}
	}
}

func TestListeners(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	polls := filepath.Join(bin, "polls")
	// The first state read is empty (the kubelet has not reported it), the second running, then
	// terminated. The debug call copies the --custom file so the test can read it.
	script := `#!/bin/sh
echo "$*" >> ` + calls + `
case "$*" in
  *" debug "*)
    while [ $# -gt 0 ]; do [ "$1" = --custom ] && cp "$2" ` + bin + `/custom.json; shift; done ;;
  *ephemeralContainerStatuses*)
    n=$(cat ` + polls + ` 2>/dev/null || echo 0); n=$((n+1)); echo $n > ` + polls + `
    case $n in 1) ;; 2) printf '{"running":{"startedAt":"x"}}' ;; *) printf '{"terminated":{"exitCode":0,"reason":"Completed"}}' ;; esac ;;
  *" logs "*) cat <<'EOF'
` + socketTables + `EOF
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	defer func(d time.Duration) { listenerPoll = d }(listenerPoll)
	listenerPoll = time.Millisecond

	c := &Client{Runner: &execx.Runner{}, Kubeconfig: "kc"}
	got, err := c.Listeners(t.Context(), "app", "web-0", "busybox", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].Port != 80 {
		t.Errorf("Listeners = %v, want the four from the tables", got)
	}
	log, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	debug := lines[0]
	for _, want := range []string{"--namespace app debug web-0 --image busybox --container hb-ports-", "--profile restricted", "--attach=false", "-- sh -c " + listenersScript} {
		if !strings.Contains(debug, want) {
			t.Errorf("debug call %q does not contain %q", debug, want)
		}
	}
	name := strings.Fields(debug[strings.Index(debug, "--container ")+len("--container "):])[0]
	if want := "logs web-0 --container " + name; !strings.Contains(lines[len(lines)-1], want) {
		t.Errorf("last call %q, want %q", lines[len(lines)-1], want)
	}
	if n, _ := os.ReadFile(polls); strings.TrimSpace(string(n)) != "3" {
		t.Errorf("%s state reads, want 3: empty, running, terminated", n)
	}
	custom, _ := os.ReadFile(filepath.Join(bin, "custom.json"))
	if string(custom) != `{"securityContext":{"runAsGroup":65534,"runAsUser":65534}}` {
		t.Errorf("--custom = %s, want an unprivileged user", custom)
	}
}

func TestListenersFailures(t *testing.T) {
	for _, c := range []struct{ name, state, want string }{
		{"non-zero exit", `{"terminated":{"exitCode":1}}`, "exited 1"},
		{"never finishes", `{"waiting":{"reason":"ImagePullBackOff"}}`, "did not finish within"},
		{"bad state", `{`, "parse the container state"},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin := t.TempDir()
			script := "#!/bin/sh\ncase \"$*\" in *ephemeralContainerStatuses*) printf '%s' '" + c.state + "' ;; esac\n"
			if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			defer func(d time.Duration) { listenerPoll = d }(listenerPoll)
			listenerPoll = time.Millisecond
			k := &Client{Runner: &execx.Runner{}}
			if _, err := k.Listeners(t.Context(), "app", "web-0", "busybox", 20*time.Millisecond); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Listeners = %v, want an error with %q", err, c.want)
			}
		})
	}
}
