package helm

import (
	"context"
	"encoding/base64"

	"gopkg.in/yaml.v3"

	"github.com/egezamb/helmbulance/internal/kube"
)

// Template renders chart as release in ns without installing it. Client-side (server false) is
// how a GitOps tool such as Argo CD renders: there is no cluster, so `lookup` returns nothing.
// Server-side (--dry-run=server) reads the cluster as install and upgrade do, so `lookup` finds
// what the release already created. The output holds Secret values: it goes to the caller only,
// never to the transcript or the report.
func (c *Client) Template(ctx context.Context, release, ns, chart string, v Values, server bool, extra ...string) ([]byte, error) {
	args := append([]string{"template", release, chart}, c.base(ns)...)
	if server {
		args = append(args, "--dry-run=server")
	}
	args = append(args, extra...)
	args = append(args, v.args()...)
	return c.run(ctx, args...)
}

// secretDoc is the part of a rendered document that says what a Secret will hold.
type secretDoc struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Data       map[string]string `yaml:"data"`
	StringData map[string]string `yaml:"stringData"`
	Items      []secretDoc       `yaml:"items"`
}

// RenderedSecrets returns the Secrets of a rendered manifest that land in ns (no namespace, or ns),
// including the items of a List, with their values as the API server would store them: data
// decoded from base64, and stringData written over it. A document that does not parse only hides
// itself, as in secretNames.
func RenderedSecrets(manifest []byte, ns string) []kube.Secret {
	var secrets []kube.Secret
	for _, text := range docSeparator.Split(string(manifest), -1) {
		var doc secretDoc
		if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
			continue
		}
		for _, d := range append([]secretDoc{doc}, doc.Items...) {
			if d.Kind != "Secret" || d.Metadata.Name == "" || (d.Metadata.Namespace != "" && d.Metadata.Namespace != ns) {
				continue
			}
			data := map[string][]byte{}
			for k, v := range d.Data {
				b, err := base64.StdEncoding.DecodeString(v)
				if err != nil {
					b = []byte(v) // the API server would refuse it; a change still shows as a change
				}
				data[k] = b
			}
			for k, v := range d.StringData {
				data[k] = []byte(v)
			}
			secrets = append(secrets, kube.Secret{Name: d.Metadata.Name, Data: data})
		}
	}
	return secrets
}
