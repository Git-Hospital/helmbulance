package helm

import (
	"context"
	"regexp"

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
		for _, n := range secretNames(out, ns) {
			owned[n] = true
		}
	}
	return owned, nil
}

// docSeparator is the line between documents in helm's output.
var docSeparator = regexp.MustCompile(`(?m)^---.*$`)

// secretNames lists the Secrets in a multi-document manifest that land in ns: those with no
// namespace (Helm puts them in the release namespace) or with ns itself, including the items of a
// List. Each document is decoded on its own, so one that does not parse (Helm installs some YAML
// that a strict decoder rejects, such as a repeated key) only hides itself.
func secretNames(manifest []byte, ns string) []string {
	var names []string
	for _, text := range docSeparator.Split(string(manifest), -1) {
		var doc object
		if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
			continue
		}
		for _, o := range append([]object{doc}, doc.Items...) {
			if o.Kind == "Secret" && o.Metadata.Name != "" && (o.Metadata.Namespace == "" || o.Metadata.Namespace == ns) {
				names = append(names, o.Metadata.Name)
			}
		}
	}
	return names
}

type object struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Items []object `yaml:"items"`
}
