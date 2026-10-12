package helm

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ChartVersion returns the version in a chart's Chart.yaml, as helm reads it. helm ignores
// --version for a chart on disk, so this is the version a local chart installs.
func (c *Client) ChartVersion(ctx context.Context, ref string) (string, error) {
	out, err := c.run(ctx, "show", "chart", ref)
	if err != nil {
		return "", err
	}
	var meta struct {
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal(out, &meta); err != nil {
		return "", fmt.Errorf("parse Chart.yaml of %s: %w", ref, err)
	}
	if meta.Version == "" {
		return "", fmt.Errorf("%s has no version in Chart.yaml", ref)
	}
	return meta.Version, nil
}
