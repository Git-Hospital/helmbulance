package kube

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Git-Hospital/helmbulance/internal/execx"
)

// keptObjects is what kubectl get prints for the cluster-scoped objects labelled
// app.kubernetes.io/managed-by=Helm: two made for releases in hb- namespaces, one for a release
// elsewhere, and one with the label but no release annotation.
const keptObjects = `{"kind":"List","items":[
	{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"ClusterRole","metadata":{"name":"web-reader",
		"annotations":{"meta.helm.sh/release-name":"hb-upgrade","meta.helm.sh/release-namespace":"hb-upgrade-values","helm.sh/resource-policy":"keep"}}},
	{"apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition","metadata":{"name":"widgets.example.com",
		"annotations":{"meta.helm.sh/release-name":"hb-reinstall","meta.helm.sh/release-namespace":"hb-reinstall"}}},
	{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"app-system",
		"annotations":{"meta.helm.sh/release-name":"prod","meta.helm.sh/release-namespace":"default"}}},
	{"apiVersion":"v1","kind":"PersistentVolume","metadata":{"name":"pv-1","annotations":{}}}
]}`

// fakeKubectlCases is a kubectl that prints the cluster-scoped types for api-resources, objects
// for get, and logs every call to $FAKE_DIR/calls.
func fakeKubectlCases(t *testing.T, types, objects string) (*Client, string) {
	t.Helper()
	bin, dir := t.TempDir(), t.TempDir()
	for name, text := range map[string]string{"types": types, "objects": objects} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\necho \"$*\" >> " + filepath.Join(dir, "calls") + "\ncase \"$*\" in\n" +
		"  *api-resources*) cat " + filepath.Join(dir, "types") + " ;;\n" +
		"  *\" get \"*) cat " + filepath.Join(dir, "objects") + " ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &Client{Runner: &execx.Runner{}}, filepath.Join(dir, "calls")
}

func TestHelmClusterObjectsKeepsOnlyReleasesInTheNamespacesAsked(t *testing.T) {
	c, calls := fakeKubectlCases(t, "clusterroles.rbac.authorization.k8s.io\ncustomresourcedefinitions.apiextensions.k8s.io\nnamespaces\npersistentvolumes\n", keptObjects)
	got, err := c.HelmClusterObjects(t.Context(), func(ns string) bool { return strings.HasPrefix(ns, "hb-") })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"clusterrole.rbac.authorization.k8s.io/web-reader", "customresourcedefinition.apiextensions.k8s.io/widgets.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("HelmClusterObjects = %q, want %q", got, want)
	}
	got, err = c.HelmClusterObjects(t.Context(), func(ns string) bool { return ns == "hb-reinstall" })
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"customresourcedefinition.apiextensions.k8s.io/widgets.example.com"}; !reflect.DeepEqual(got, want) {
		t.Errorf("for hb-reinstall only: %q, want %q", got, want)
	}
	got, err = c.HelmClusterObjects(t.Context(), func(ns string) bool { return ns == "default" })
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"namespace/app-system"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a core kind takes no group: %q, want %q", got, want)
	}
	b, _ := os.ReadFile(calls)
	if !strings.Contains(string(b), " get clusterroles.rbac.authorization.k8s.io,customresourcedefinitions.apiextensions.k8s.io,namespaces,persistentvolumes -l app.kubernetes.io/managed-by=Helm -o json") {
		t.Errorf("want one get over every cluster-scoped type, with Helm's label; calls:\n%s", b)
	}
}

func TestHelmClusterObjectsWithNoTypesListsNothing(t *testing.T) {
	c, calls := fakeKubectlCases(t, "", keptObjects)
	got, err := c.HelmClusterObjects(t.Context(), func(string) bool { return true })
	if err != nil || got != nil {
		t.Errorf("HelmClusterObjects = %q, %v; want nothing", got, err)
	}
	if b, _ := os.ReadFile(calls); strings.Contains(string(b), " get ") {
		t.Errorf("with no type to list, there is nothing to get; calls:\n%s", b)
	}
}

func TestDeleteObjects(t *testing.T) {
	c, calls := fakeKubectlCases(t, "", "")
	if err := c.DeleteObjects(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Error("DeleteObjects with nothing to delete ran kubectl")
	}
	if err := c.DeleteObjects(t.Context(), []string{"clusterrole.rbac.authorization.k8s.io/a", "namespace/b"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	if want := "delete clusterrole.rbac.authorization.k8s.io/a namespace/b --ignore-not-found --wait=true"; !strings.Contains(string(b), want) {
		t.Errorf("want %q; calls:\n%s", want, b)
	}
}
