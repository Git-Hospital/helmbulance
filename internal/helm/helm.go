// Package helm drives the helm CLI. Helmbulance shells out instead of embedding the Helm SDK, so it
// tests the Helm version the user actually runs, and the reproduction commands in a report are the
// commands that were really executed.
package helm

import (
	"context"
	"strings"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
)

// Client runs helm against one sandbox cluster.
type Client struct {
	Runner     *execx.Runner
	Kubeconfig string
	Binary     string
	Timeout    time.Duration
}

// Values are the values flags of an install or upgrade.
type Values struct {
	Files []string // -f
	Sets  []string // --set, each "key=value"
}

func (v Values) args() []string {
	var a []string
	for _, f := range v.Files {
		a = append(a, "-f", f)
	}
	for _, s := range v.Sets {
		a = append(a, "--set", s)
	}
	return a
}

func (c *Client) bin() string {
	if c.Binary != "" {
		return c.Binary
	}
	return "helm"
}

func (c *Client) base(ns string) []string {
	return []string{"--kubeconfig", c.Kubeconfig, "--namespace", ns}
}

func (c *Client) waitArgs() []string {
	return []string{"--wait", "--timeout", c.Timeout.String()}
}

// Install installs chart as release into ns, creating the namespace. extra carries flags such as
// --version or --repo.
func (c *Client) Install(ctx context.Context, release, ns, chart string, v Values, extra ...string) error {
	args := append([]string{"install", release, chart, "--create-namespace"}, c.base(ns)...)
	args = append(args, c.waitArgs()...)
	args = append(args, extra...)
	args = append(args, v.args()...)
	_, err := c.Runner.Run(ctx, c.bin(), args...)
	return err
}

// Upgrade upgrades release with the given values and extra flags (e.g. --reset-values).
func (c *Client) Upgrade(ctx context.Context, release, ns, chart string, v Values, extra ...string) error {
	args := append([]string{"upgrade", release, chart}, c.base(ns)...)
	args = append(args, c.waitArgs()...)
	args = append(args, extra...)
	args = append(args, v.args()...)
	_, err := c.Runner.Run(ctx, c.bin(), args...)
	return err
}

// Uninstall removes release and waits for its resources to go.
func (c *Client) Uninstall(ctx context.Context, release, ns string) error {
	args := append([]string{"uninstall", release, "--wait", "--timeout", c.Timeout.String()}, c.base(ns)...)
	_, err := c.Runner.Run(ctx, c.bin(), args...)
	return err
}

// Version returns the short helm version, for the report.
func (c *Client) Version(ctx context.Context) string {
	out, err := c.Runner.Run(ctx, c.bin(), "version", "--short")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// InstallCapture is Install that also returns what helm printed on stderr, where the API server's
// warnings (deprecated or removed APIs) end up.
func (c *Client) InstallCapture(ctx context.Context, release, ns, chart string, v Values, extra ...string) (string, error) {
	args := append([]string{"install", release, chart, "--create-namespace"}, c.base(ns)...)
	args = append(args, c.waitArgs()...)
	args = append(args, extra...)
	args = append(args, v.args()...)
	_, stderr, err := c.Runner.RunCapture(ctx, c.bin(), args...)
	return string(stderr), err
}
