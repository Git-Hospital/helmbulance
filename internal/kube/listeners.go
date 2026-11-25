package kube

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Listener is a TCP socket in the LISTEN state in a pod's network namespace.
type Listener struct {
	Addr netip.Addr // the bound address: 0.0.0.0 or :: for every interface
	Port uint16
}

// Loopback reports whether only the pod itself can connect: 127.0.0.0/8 or ::1.
func (l Listener) Loopback() bool { return l.Addr.IsLoopback() }

func (l Listener) String() string { return netip.AddrPortFrom(l.Addr, l.Port).String() }

// listenerPoll is how often Listeners checks whether its container has finished. A variable, so
// tests can shorten it.
var listenerPoll = time.Second

// listenersScript prints both socket tables, each after a header line naming it. tcp6 is missing on
// a node without IPv6, which is not an error.
const listenersScript = `for t in tcp tcp6; do echo "== $t"; cat /proc/net/$t 2>/dev/null; done`

// Listeners returns the TCP ports the pod listens on, read from /proc/net/tcp and /proc/net/tcp6
// by an ephemeral container (architecture: network-isolation, port discovery). The containers of a
// pod share one network namespace, so those files list every container's sockets, including ports
// no containerPort declares, and the ephemeral container needs no access to the app's processes.
//
// It runs as an unprivileged user with the restricted profile, so a namespace that enforces the
// restricted Pod Security level admits it; the socket tables are world-readable. An ephemeral
// container cannot be removed and stays in the pod's spec until the pod is replaced; it has
// finished by the time Listeners returns. image needs sh and cat.
func (c *Client) Listeners(ctx context.Context, ns, pod, image string, timeout time.Duration) ([]Listener, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return nil, err
	}
	name := "hb-ports-" + hex.EncodeToString(suffix)
	// kubectl debug's restricted profile drops every capability and sets runAsNonRoot, but not a
	// user, and busybox images run as root: without a user the kubelet refuses to start it.
	custom, err := json.Marshal(map[string]any{"securityContext": map[string]any{"runAsUser": 65534, "runAsGroup": 65534}})
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "hb-ports-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	customFile := filepath.Join(dir, "custom.json")
	if err := os.WriteFile(customFile, custom, 0o600); err != nil {
		return nil, err
	}
	base := []string{"--kubeconfig", c.Kubeconfig, "--namespace", ns}
	if _, err := c.Runner.Run(ctx, "kubectl", append(base, "debug", pod, "--image", image, "--container", name,
		"--profile", "restricted", "--custom", customFile, "--attach=false", "--quiet", "--", "sh", "-c", listenersScript)...); err != nil {
		return nil, err
	}

	state := `jsonpath={.status.ephemeralContainerStatuses[?(@.name=="` + name + `")].state}`
	deadline := time.Now().Add(timeout)
	for {
		out, err := c.Runner.Run(ctx, "kubectl", append(base, "--request-timeout", readTimeout, "get", "pod", pod, "-o", state)...)
		if err != nil {
			return nil, err
		}
		done, exit, err := ephemeralDone(out)
		if err != nil {
			return nil, err
		}
		if done {
			if exit != 0 {
				return nil, fmt.Errorf("reading the socket tables of pod %s exited %d", pod, exit)
			}
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the container reading the socket tables of pod %s did not finish within %s (state %s)", pod, timeout, strings.TrimSpace(string(out)))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(listenerPoll):
		}
	}
	out, err := c.Runner.Run(ctx, "kubectl", append(base, "--request-timeout", readTimeout, "logs", pod, "--container", name)...)
	if err != nil {
		return nil, err
	}
	return parseListeners(string(out))
}

// ephemeralDone reads a container state ({"terminated": {"exitCode": 0}}, or waiting or running,
// or nothing before the kubelet reports it).
func ephemeralDone(out []byte) (done bool, exit int, err error) {
	if len(strings.TrimSpace(string(out))) == 0 {
		return false, 0, nil
	}
	var s struct {
		Terminated *struct {
			ExitCode int `json:"exitCode"`
		} `json:"terminated"`
	}
	if err := json.Unmarshal(out, &s); err != nil {
		return false, 0, fmt.Errorf("parse the container state: %w", err)
	}
	if s.Terminated == nil {
		return false, 0, nil
	}
	return true, s.Terminated.ExitCode, nil
}

// tcpListen is the LISTEN state in /proc/net/tcp (TCP_LISTEN in the kernel's tcp_states.h).
const tcpListen = "0A"

// parseListeners reads what listenersScript printed: the listening sockets of both tables, sorted
// by port and address, each once. An IPv4 address mapped into IPv6 is given as IPv4.
func parseListeners(out string) ([]Listener, error) {
	var listeners []Listener
	table := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if t, ok := strings.CutPrefix(line, "== "); ok {
			table = t
			continue
		}
		fields := strings.Fields(line)
		// The header line starts with "sl"; a socket line with "N:".
		if len(fields) < 4 || !strings.HasSuffix(fields[0], ":") || fields[3] != tcpListen {
			continue
		}
		if table != "tcp" && table != "tcp6" {
			return nil, fmt.Errorf("socket line outside a known table: %q", line)
		}
		l, err := parseLocal(fields[1], table == "tcp6")
		if err != nil {
			return nil, fmt.Errorf("parse %q in /proc/net/%s: %w", fields[1], table, err)
		}
		if !slices.Contains(listeners, l) {
			listeners = append(listeners, l)
		}
	}
	if table == "" {
		return nil, errors.New("no socket table in the output")
	}
	slices.SortFunc(listeners, func(a, b Listener) int {
		if a.Port != b.Port {
			return int(a.Port) - int(b.Port)
		}
		return a.Addr.Compare(b.Addr)
	})
	return listeners, nil
}

// parseLocal reads a local_address column, such as 0100007F:1F90 (127.0.0.1:8080). The kernel
// prints the address as 32-bit words in host byte order, which is little-endian on the amd64 and
// arm64 nodes kind runs; the port is plain hex.
func parseLocal(s string, v6 bool) (Listener, error) {
	addrHex, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return Listener{}, errors.New("no port")
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return Listener{}, err
	}
	raw, err := hex.DecodeString(addrHex)
	if err != nil {
		return Listener{}, err
	}
	if (v6 && len(raw) != 16) || (!v6 && len(raw) != 4) {
		return Listener{}, fmt.Errorf("%d address bytes", len(raw))
	}
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	addr, _ := netip.AddrFromSlice(raw)
	return Listener{Addr: addr.Unmap(), Port: uint16(port)}, nil
}
