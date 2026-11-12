package kube

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Variables, not constants, so tests can shorten them.
var (
	// canaryTries and canaryInterval bound the probes on each side of the policy: the server may
	// take a moment to listen, and a CNI applies a NetworkPolicy asynchronously.
	canaryTries    = 10
	canaryInterval = time.Second
	// canaryCleanup bounds deleting the canary's namespace, which runs even after a cancel.
	canaryCleanup = 2 * time.Minute
)

// EnforcesNetworkPolicy reports whether the cluster's network plugin enforces NetworkPolicy, which a
// network scenario needs before a policy can prove anything (architecture §3.5, the CNI canary;
// #101). In a namespace of its own it starts a server pod and a client pod, both hardened like the
// traffic pod, checks that the client reaches the server, applies a deny-all ingress policy and
// probes again, with bounded retries since policies apply asynchronously. It is true when the
// client is blocked. The namespace is deleted afterwards, also when ctx is cancelled.
//
// An error means the canary could not tell, never that policies are not enforced: a kubectl
// error, a pod that did not start, or a client that could not reach the server without any policy.
func (c *Client) EnforcesNetworkPolicy(ctx context.Context, image string) (enforced bool, err error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return false, err
	}
	ns := "hb-canary-" + hex.EncodeToString(suffix)
	// Deferred before the create: a cancel can stop kubectl after the API server made the
	// namespace, and deleting one that does not exist is harmless (--ignore-not-found).
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), canaryCleanup)
		defer cancel()
		if derr := c.DeleteNamespace(cleanup, ns); derr != nil && err == nil {
			err = fmt.Errorf("delete the canary namespace %s: %w", ns, derr)
		}
	}()
	if _, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "create", "namespace", ns); err != nil {
		return false, err
	}

	// A new namespace gets its default ServiceAccount a moment later, and a pod created before
	// that is refused.
	if err := c.waitForServiceAccount(ctx, ns); err != nil {
		return false, err
	}
	const port = "8080"
	if err := c.StartPod(ctx, ns, "server", image, "while true; do echo ok | nc -l -p "+port+"; done",
		map[string]string{"app.kubernetes.io/name": "hb-canary-server"}); err != nil {
		return false, err
	}
	if err := c.StartPod(ctx, ns, "client", image, "sleep 3600", nil); err != nil {
		return false, err
	}
	for _, pod := range []string{"server", "client"} {
		if err := c.WaitPodRunning(ctx, ns, pod, "2m"); err != nil {
			return false, err
		}
	}
	out, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "--namespace", ns,
		"get", "pod", "server", "-o", "jsonpath={.status.podIP}")
	if err != nil {
		return false, err
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return false, fmt.Errorf("the canary server has no pod IP")
	}

	// Control: without a policy the client must reach the server, or a block later proves nothing.
	if open, err := c.probeUntil(ctx, ns, ip, port, true); err != nil {
		return false, err
	} else if !open {
		return false, fmt.Errorf("the canary client could not reach its server with no policy in place")
	}
	if err := c.applyDenyAll(ctx, ns); err != nil {
		return false, err
	}
	open, err := c.probeUntil(ctx, ns, ip, port, false)
	if err != nil {
		return false, err
	}
	return !open, nil
}

// waitForServiceAccount waits, with the canary's bounded retries, until ns has its default
// ServiceAccount.
func (c *Client) waitForServiceAccount(ctx context.Context, ns string) error {
	var err error
	for i := 0; i < 3*canaryTries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(canaryInterval):
			}
		}
		if _, err = c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "--namespace", ns,
			"get", "serviceaccount", "default", "-o", "name"); err == nil {
			return nil
		}
	}
	return fmt.Errorf("namespace %s got no default ServiceAccount: %w", ns, err)
}

// probeUntil probes from the client up to canaryTries times and stops as soon as the server is
// open (want true) or closed (want false). It returns the last state seen.
func (c *Client) probeUntil(ctx context.Context, ns, ip, port string, want bool) (bool, error) {
	open := !want
	for i := 0; i < canaryTries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return open, ctx.Err()
			case <-time.After(canaryInterval):
			}
		}
		// The shell prints the result, so kubectl exits 0 either way and an error is kubectl's own.
		out, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "--namespace", ns, "exec", "client", "--",
			"sh", "-c", "if nc -w 2 "+ip+" "+port+" </dev/null >/dev/null 2>&1; then echo open; else echo closed; fi")
		if err != nil {
			return open, err
		}
		switch strings.TrimSpace(string(out)) {
		case "open":
			open = true
		case "closed":
			open = false
		default:
			return open, fmt.Errorf("the canary probe printed %q", strings.TrimSpace(string(out)))
		}
		if open == want {
			return open, nil
		}
	}
	return open, nil
}

// applyDenyAll applies a NetworkPolicy that selects every pod in ns and allows no ingress.
func (c *Client) applyDenyAll(ctx context.Context, ns string) error {
	policy, err := json.Marshal(map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   map[string]any{"name": "hb-canary-deny-all", "namespace": ns},
		"spec":       map[string]any{"podSelector": map[string]any{}, "policyTypes": []string{"Ingress"}},
	})
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "hb-canary-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "deny-all.json")
	if err := os.WriteFile(file, policy, 0o600); err != nil {
		return err
	}
	_, err = c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "apply", "-f", file)
	return err
}
