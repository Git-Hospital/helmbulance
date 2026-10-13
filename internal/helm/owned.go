package helm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// OwnedSecrets returns the names of the Secrets in ns that the release renders, from its manifest
// and its hooks. Secrets written by a Job, an operator or the app itself are not in it, so they
// cannot be mistaken for the chart's own.
func (c *Client) OwnedSecrets(ctx context.Context, release, ns string) (map[string]bool, error) {
	owned := map[string]bool{}
	for _, what := range []string{"manifest", "hooks"} {
		out, err := c.run(ctx, append([]string{"get", what, release}, c.base(ns)...)...)
		if err != nil {
			return nil, err
		}
		names, err := secretNames(out, ns)
		if err != nil {
			return nil, fmt.Errorf("helm get %s: %w", what, err)
		}
		for _, n := range names {
			owned[n] = true
		}
	}
	return owned, nil
}

// secretNames lists the Secrets in a multi-document manifest that land in ns: those with no
// namespace (Helm puts them in the release namespace) or with ns itself.
func secretNames(manifest []byte, ns string) ([]string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(manifest))
	var names []string
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
		}
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		if doc.Kind == "Secret" && doc.Metadata.Name != "" && (doc.Metadata.Namespace == "" || doc.Metadata.Namespace == ns) {
			names = append(names, doc.Metadata.Name)
		}
	}
}
