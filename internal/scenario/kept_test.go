package scenario

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/helmbulance/internal/execx"
	"github.com/Git-Hospital/helmbulance/internal/helm"
	"github.com/Git-Hospital/helmbulance/internal/kube"
	"github.com/Git-Hospital/helmbulance/internal/oracle"
)

// fakeKeptTools is helm and kubectl for a cluster that holds two kept ClusterRoles: one made for
// a release in the namespace of the scenario under test ($FAKE_NS), one for a release in another
// scenario's namespace. helm install fails, so a scenario stops after its first install.
const fakeKeptTools = `#!/bin/sh
echo "$(basename "$0") $*" >> "$FAKE_DIR/log"
case "$(basename "$0") $*" in
  "helm install"*) echo "Error: INSTALLATION FAILED: test" >&2; exit 1 ;;
  "kubectl"*" api-resources "*) echo clusterroles.rbac.authorization.k8s.io ;;
  "kubectl"*" get "*" -- clusterroles"*) cat <<JSON
{"items":[
 {"apiVersion":"rbac.authorization.k8s.io/v1","kind":"ClusterRole","metadata":{"name":"own","annotations":{"meta.helm.sh/release-name":"r","meta.helm.sh/release-namespace":"$FAKE_NS"}}},
 {"apiVersion":"rbac.authorization.k8s.io/v1","kind":"ClusterRole","metadata":{"name":"other","annotations":{"meta.helm.sh/release-name":"r","meta.helm.sh/release-namespace":"hb-somewhere-else"}}}
]}
JSON
  ;;
  "kubectl"*" get "*) echo '{"items":[]}' ;;
esac
exit 0
`

// #52: helm uninstall leaves a cluster-scoped object marked helm.sh/resource-policy: keep, and the
// next scenario's install, under another release name, refuses it. Each scenario deletes its own
// release's kept objects when it cleans up, and before it installs deletes those of every scenario,
// which a run killed before its cleanup leaves behind.
func TestScenariosDeleteKeptClusterObjects(t *testing.T) {
	for _, c := range []struct {
		s      Scenario
		ns     string
		modify func(*Env)
	}{
		{UpgradeValues{}, upgradeNamespace, func(e *Env) { e.Values = helm.Values{Sets: []string{"a=b"}} }},
		{ChartUpgrade{}, chartUpgradeNamespace, func(e *Env) { e.From, e.FromVersion = "repo/chart", "1.0.0" }},
		{ZeroDowntime{}, zeroDowntimeNamespace, func(*Env) {}},
		{Reinstall{}, reinstallNamespace, func(*Env) {}},
		{GitopsRender{}, gitopsRenderNamespace, func(*Env) {}},
	} {
		t.Run(c.s.Name(), func(t *testing.T) {
			bin, dir := t.TempDir(), t.TempDir()
			for _, name := range []string{"helm", "kubectl"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(fakeKeptTools), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("FAKE_DIR", dir)
			t.Setenv("FAKE_NS", c.ns)
			hasher, err := oracle.NewHasher()
			if err != nil {
				t.Fatal(err)
			}
			runner := &execx.Runner{}
			env := &Env{
				Runner: runner,
				Helm:   &helm.Client{Runner: runner, Timeout: time.Minute},
				Kube:   &kube.Client{Runner: runner},
				Hasher: hasher,
				Chart:  "./chart",
				Log:    io.Discard,
			}
			c.modify(env)
			c.s.Run(t.Context(), env)

			log, err := os.ReadFile(filepath.Join(dir, "log"))
			if err != nil {
				t.Fatal(err)
			}
			calls := strings.Split(strings.TrimSpace(string(log)), "\n")
			install := -1
			for i, call := range calls {
				if strings.HasPrefix(call, "helm install ") {
					install = i
					break
				}
			}
			if install < 0 {
				t.Fatalf("no helm install in\n%s", log)
			}
			before, after := strings.Join(calls[:install], "\n"), strings.Join(calls[install:], "\n")
			if !strings.Contains(before+"\n", " -- clusterrole.rbac.authorization.k8s.io/other clusterrole.rbac.authorization.k8s.io/own\n") {
				t.Errorf("before the install, want both kept ClusterRoles deleted; got\n%s", before)
			}
			if !strings.Contains(after+"\n", " -- clusterrole.rbac.authorization.k8s.io/own\n") || strings.Contains(after, "/other") {
				t.Errorf("in the cleanup, want only the release's own kept ClusterRole deleted; got\n%s", after)
			}
		})
	}
}
