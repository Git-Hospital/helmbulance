package scan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Git-Hospital/helmbulance/internal/scenario"
)

func obj(kind, name string, doc map[string]any) object {
	return object{Kind: kind, Name: name, Source: "c/templates/" + strings.ToLower(kind) + ".yaml", Doc: doc}
}

// HB-SCAN-001 covers a binding's roleRef and a Service's clusterIP, and does not count an API
// default written out in one version and left out in the other (#56).
func TestImmutableChanges(t *testing.T) {
	never := func(string) bool { return false }
	old := []object{
		obj("ClusterRoleBinding", "app", map[string]any{"roleRef": map[string]any{"kind": "ClusterRole", "name": "view"}}),
		obj("RoleBinding", "app", map[string]any{"roleRef": map[string]any{"kind": "Role", "name": "reader"}}),
		obj("Service", "headless", map[string]any{"spec": map[string]any{"clusterIP": "None"}}),
		obj("Service", "web", map[string]any{"spec": map[string]any{}}),
		obj("StatefulSet", "db", map[string]any{"spec": map[string]any{"serviceName": "db"}}),
	}
	cur := []object{
		obj("ClusterRoleBinding", "app", map[string]any{"roleRef": map[string]any{"kind": "ClusterRole", "name": "edit"}}),
		obj("RoleBinding", "app", map[string]any{"roleRef": map[string]any{"kind": "Role", "name": "reader"}}),
		obj("Service", "headless", map[string]any{"spec": map[string]any{}}),
		obj("Service", "web", map[string]any{"spec": map[string]any{"clusterIP": ""}}),
		obj("StatefulSet", "db", map[string]any{"spec": map[string]any{"serviceName": "db", "podManagementPolicy": "OrderedReady"}}),
	}
	got := immutableChanges(old, cur, never)
	want := []string{
		"`ClusterRoleBinding/app` roleRef changes (c/templates/clusterrolebinding.yaml)",
		"`Service/headless` spec.clusterIP changes (c/templates/service.yaml)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("immutableChanges =\n%q\nwant\n%q", got, want)
	}
	// A different podManagementPolicy is still a change.
	cur[4].Doc = map[string]any{"spec": map[string]any{"serviceName": "db", "podManagementPolicy": "Parallel"}}
	if got := immutableChanges(old[4:], cur[4:], never); len(got) != 1 || !strings.Contains(got[0], "podManagementPolicy") {
		t.Errorf("OrderedReady to Parallel: %q, want one change", got)
	}
}

// HB-SCAN-002 sees a checksum written with printf, and one taken in a helper the pod template
// includes, not only include (print $.Template.BasePath "/x") (#56).
func TestFirstUpgradeRestartChecksumForms(t *testing.T) {
	secret := "{{- $s := lookup \"v1\" \"Secret\" .Release.Namespace \"db\" }}\napiVersion: v1\nkind: Secret\nmetadata: {name: db}\ndata:\n  password: {{ $s | ternary (index $s.data \"password\") (randAlphaNum 16 | b64enc) }}\n"
	for name, deployment := range map[string]string{
		"print":  `checksum/secret: {{ include (print $.Template.BasePath "/secret.yaml") . | sha256sum }}`,
		"printf": `checksum/secret: {{ include (printf "%s/secret.yaml" .Template.BasePath) . | sha256sum }}`,
		"helper": `checksum/secret: {{ include "app.checksums" . }}`,
	} {
		t.Run(name, func(t *testing.T) {
			chart := t.TempDir()
			files := map[string]string{
				"secret.yaml":     secret,
				"deployment.yaml": "spec:\n  template:\n    metadata:\n      annotations:\n        " + deployment + "\n",
				"_helpers.tpl":    `{{- define "app.checksums" -}}{{ include (print $.Template.BasePath "/secret.yaml") . | sha256sum }}{{- end -}}`,
			}
			if err := os.MkdirAll(filepath.Join(chart, "templates"), 0o755); err != nil {
				t.Fatal(err)
			}
			for f, text := range files {
				if err := os.WriteFile(filepath.Join(chart, "templates", f), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			res := firstUpgradeRestart(Options{Chart: chart})
			if res.Verdict != scenario.Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-SCAN-002" {
				t.Fatalf("result %s %q %+v, want one HB-SCAN-002", res.Verdict, res.Summary, res.Findings)
			}
			if len(res.Findings[0].Evidence) != 1 {
				t.Errorf("evidence %q, want one line: _helpers.tpl renders nothing of its own", res.Findings[0].Evidence)
			}
			if want := "`templates/deployment.yaml` hashes `templates/secret.yaml`"; !strings.Contains(res.Findings[0].Evidence[0], want) {
				t.Errorf("evidence %q, want %q", res.Findings[0].Evidence, want)
			}
		})
	}
}
