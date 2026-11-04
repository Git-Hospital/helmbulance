package kube

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Git-Hospital/helmbulance/internal/execx"
)

// Capabilities reads the server version and every API version, as group/version and as
// group/version/Kind, from the right of each api-resources line, since SHORTNAMES is often empty.
func TestCapabilities(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *" version -o json") echo '{"clientVersion":{"gitVersion":"v1.37.1"},"serverVersion":{"gitVersion":"v1.34.11"}}' ;;
  *" api-resources --no-headers") printf '%s\n' \
    'configmaps            cm       v1                        true    ConfigMap' \
    'deployments           deploy   apps/v1                   true    Deployment' \
    'statefulsets          sts      apps/v1                   true    StatefulSet' \
    'servicemonitors                monitoring.coreos.com/v1  true    ServiceMonitor' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := &Client{Runner: &execx.Runner{}}
	version, apis, err := c.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if version != "v1.34.11" {
		t.Errorf("version = %q, want the server's v1.34.11, not the client's", version)
	}
	want := []string{"apps/v1", "apps/v1/Deployment", "apps/v1/StatefulSet", "monitoring.coreos.com/v1",
		"monitoring.coreos.com/v1/ServiceMonitor", "v1", "v1/ConfigMap"}
	if !reflect.DeepEqual(apis, want) {
		t.Errorf("apiVersions =\n%q\nwant\n%q", apis, want)
	}
}

// Without a server version, a render would fall back to helm's own default: refuse instead.
func TestCapabilitiesWithoutAServerVersion(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte("#!/bin/sh\necho '{\"clientVersion\":{\"gitVersion\":\"v1.37.1\"}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, _, err := (&Client{Runner: &execx.Runner{}}).Capabilities(t.Context()); err == nil {
		t.Error("Capabilities without a server version: no error")
	}
}
