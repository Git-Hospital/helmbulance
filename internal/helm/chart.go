package helm

import (
	"context"
	"encoding/json"
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

// ReleaseVersion returns the chart version a release in ns runs, so a report can say what was
// really installed rather than what was asked for.
func (c *Client) ReleaseVersion(ctx context.Context, release, ns string) (string, error) {
	args := append([]string{"get", "metadata", release, "-o", "json"}, c.base(ns)...)
	out, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &meta); err != nil {
		return "", fmt.Errorf("parse the metadata of %s: %w", release, err)
	}
	if meta.Version == "" {
		return "", fmt.Errorf("helm gave no chart version for %s", release)
	}
	return meta.Version, nil
}
