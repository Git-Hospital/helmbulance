package kube

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// Variables, not constants, so tests can shorten them.
var (
	// reachTries bounds how often Reachable probes an address that stays closed: a listener can be
	// busy for a moment, and an answer of "closed" is the one a finding rests on.
	reachTries    = 3
	reachInterval = 2 * time.Second
)

// Reachable reports which of targets a pod in a foreign namespace can open a TCP connection to:
// the access matrix of network-isolation (architecture: output), for one source namespace. The
// client pod runs in a namespace of its own, hardened like the traffic pod, with no labels a
// policy could allow by name. Every target is probed once per round with a 2 s connect timeout;
// a target that opens in any round is reachable, and only those still closed are probed again,
// up to reachTries rounds. The namespace is deleted afterwards, also when ctx is cancelled.
//
// Whether a policy should have blocked a connection is for the caller to judge, against a control
// with the policies removed and the canary (EnforcesNetworkPolicy) saying the CNI enforces them.
func (c *Client) Reachable(ctx context.Context, image string, targets []netip.AddrPort) (reach map[netip.AddrPort]bool, err error) {
	reach = map[netip.AddrPort]bool{}
	if len(targets) == 0 {
		return reach, nil
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return nil, err
	}
	ns := "hb-reach-" + hex.EncodeToString(suffix)
	// Deferred before the create, as in the canary: kubectl can be stopped after the namespace exists.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), canaryCleanup)
		defer cancel()
		if derr := c.DeleteNamespace(cleanup, ns); derr != nil && err == nil {
			err = fmt.Errorf("delete the probe namespace %s: %w", ns, derr)
		}
	}()
	if _, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "create", "namespace", ns); err != nil {
		return nil, err
	}
	if err := c.waitForServiceAccount(ctx, ns); err != nil {
		return nil, err
	}
	if err := c.StartPod(ctx, ns, "client", image, "sleep 3600", nil); err != nil {
		return nil, err
	}
	if err := c.WaitPodRunning(ctx, ns, "client", "2m"); err != nil {
		return nil, err
	}

	pending := slices.Clone(targets)
	for round := 0; round < reachTries && len(pending) > 0; round++ {
		if round > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(reachInterval):
			}
		}
		// The shell prints one line per target, so kubectl exits 0 either way and an error is kubectl's own.
		out, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "--namespace", ns, "exec", "client", "--",
			"sh", "-c", probeScript(pending))
		if err != nil {
			return nil, err
		}
		results, err := parseProbe(string(out), pending)
		if err != nil {
			return nil, err
		}
		var closed []netip.AddrPort
		for _, t := range pending {
			reach[t] = results[t]
			if !results[t] {
				closed = append(closed, t)
			}
		}
		pending = closed
	}
	return reach, nil
}

// probeScript tries each target with busybox nc and prints "<n> open" or "<n> closed", n being
// the target's index. Addresses come from netip, so they hold no shell syntax.
func probeScript(targets []netip.AddrPort) string {
	var b strings.Builder
	for i, t := range targets {
		fmt.Fprintf(&b, "if nc -w 2 %s %d </dev/null >/dev/null 2>&1; then echo '%d open'; else echo '%d closed'; fi; ",
			t.Addr().String(), t.Port(), i, i)
	}
	return b.String()
}

// parseProbe reads probeScript's output: exactly one line for each target.
func parseProbe(out string, targets []netip.AddrPort) (map[netip.AddrPort]bool, error) {
	got := map[netip.AddrPort]bool{}
	seen := make([]bool, len(targets))
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var i int
		var state string
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d %s", &i, &state); err != nil || i < 0 || i >= len(targets) || seen[i] ||
			(state != "open" && state != "closed") {
			return nil, fmt.Errorf("the reachability probe printed %q", strings.TrimSpace(out))
		}
		seen[i] = true
		got[targets[i]] = state == "open"
	}
	if slices.Contains(seen, false) {
		return nil, fmt.Errorf("the reachability probe answered %d of %d targets", len(got), len(targets))
	}
	return got, nil
}
