package helm

import (
	"context"
	"encoding/base64"
	"strings"

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
		Name        string            `yaml:"name"`
		Namespace   string            `yaml:"namespace"`
		Annotations map[string]string `yaml:"annotations"`
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
	return renderedSecrets(manifest, ns, false)
}

// UpgradedSecrets is RenderedSecrets for a render with --is-upgrade, without the hooks that run
// only at install (helm.sh/hook names pre-install or post-install and no upgrade event). helm
// template prints every hook, but helm upgrade does not run those, so the Secret one made at
// install stays as it is (#45).
func UpgradedSecrets(manifest []byte, ns string) []kube.Secret {
	return renderedSecrets(manifest, ns, true)
}

// InstallOnlyHooks names the Secrets of a rendered manifest that land in ns and are hooks run only
// at install, with their helm.sh/hook events: the ones UpgradedSecrets leaves out.
func InstallOnlyHooks(manifest []byte, ns string) map[string]string {
	hooks := map[string]string{}
	for _, d := range secretDocs(manifest, ns) {
		if events := d.Metadata.Annotations["helm.sh/hook"]; InstallOnlyHook(events) {
			hooks[d.Metadata.Name] = events
		}
	}
	return hooks
}

func renderedSecrets(manifest []byte, ns string, upgrade bool) []kube.Secret {
	var secrets []kube.Secret
	for _, d := range secretDocs(manifest, ns) {
		if upgrade && InstallOnlyHook(d.Metadata.Annotations["helm.sh/hook"]) {
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
	return secrets
}

// secretDocs returns the Secret documents of a rendered manifest that land in ns (no namespace, or
// ns), including the items of a List.
func secretDocs(manifest []byte, ns string) []secretDoc {
	var docs []secretDoc
	for _, text := range docSeparator.Split(string(manifest), -1) {
		var doc secretDoc
		if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
			continue
		}
		for _, d := range append([]secretDoc{doc}, doc.Items...) {
			if d.Kind == "Secret" && d.Metadata.Name != "" && (d.Metadata.Namespace == "" || d.Metadata.Namespace == ns) {
				docs = append(docs, d)
			}
		}
	}
	return docs
}

// UpgradeHook reports whether a helm.sh/hook annotation runs on helm upgrade.
func UpgradeHook(events string) bool {
	for _, e := range strings.Split(events, ",") {
		if e = strings.TrimSpace(e); e == "pre-upgrade" || e == "post-upgrade" {
			return true
		}
	}
	return false
}

// InstallOnlyHook reports whether a helm.sh/hook annotation runs at install and never at upgrade.
func InstallOnlyHook(events string) bool {
	install := false
	for _, e := range strings.Split(events, ",") {
		switch strings.TrimSpace(e) {
		case "pre-install", "post-install":
			install = true
		case "pre-upgrade", "post-upgrade":
			return false
		}
	}
	return install
}
