package helm

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
)

func TestRenderedSecrets(t *testing.T) {
	manifest := `---
# Source: demo/templates/secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: db
data:
  password: aHVudGVyMg==
  user: YXBw
stringData:
  user: admin
---
# Source: demo/templates/other.yaml
apiVersion: v1
kind: Secret
metadata:
  name: elsewhere
  namespace: kube-system
data:
  token: eA==
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: config
data:
  password: not-a-secret
---
apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: listed
      namespace: hb
    stringData:
      key: plain
---
kind: Secret
metadata: {name: broken
`
	got := map[string]map[string]string{}
	for _, s := range RenderedSecrets([]byte(manifest), "hb") {
		got[s.Name] = map[string]string{}
		for k, v := range s.Data {
			got[s.Name][k] = string(v)
		}
	}
	want := map[string]map[string]string{
		"db":     {"password": "hunter2", "user": "admin"}, // stringData wins, as on the API server
		"listed": {"key": "plain"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RenderedSecrets = %v, want %v", got, want)
	}
}

// Template renders client-side unless asked for the server, so `lookup` sees the cluster only then.
func TestTemplateArgs(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "args")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(bin, "helm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := &Client{Runner: &execx.Runner{}, Kubeconfig: "/kc", Timeout: time.Minute}
	v := Values{Sets: []string{"a=b"}}
	for _, server := range []bool{false, true} {
		if _, err := c.Template(t.Context(), "hb", "ns", "./chart", v, server); err != nil {
			t.Fatal(err)
		}
	}
	out, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	want := []string{
		"template hb ./chart --kubeconfig /kc --namespace ns --set a=b",
		"template hb ./chart --kubeconfig /kc --namespace ns --dry-run=server --set a=b",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("helm was called with\n%q\nwant\n%q", lines, want)
	}
}
