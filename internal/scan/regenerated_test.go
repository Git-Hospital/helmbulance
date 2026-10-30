package scan

import (
	"reflect"
	"strings"
	"testing"
)

// A key that differs between two install renders is sorted by when Helm renders its Secret: on
// upgrade (HB-SCAN-003) or only at install (HB-SCAN-006, #86).
func TestRegenerated(t *testing.T) {
	secret := func(name, events, value string) object {
		return object{Kind: "Secret", Name: name, Hook: events != "", HookEvents: events, Source: "c/templates/" + name + ".yaml",
			Doc: map[string]any{"stringData": map[string]any{"password": value}}}
	}
	type spec struct{ name, events string }
	specs := []spec{
		{"plain", ""},                        // rendered on every upgrade
		{"install-only", ""},                 // under .Release.IsInstall: not in the upgrade render
		{"pre-install", "pre-install"},       // an install-only hook
		{"both", "pre-install,pre-upgrade"},  // a hook that runs on upgrade too
		{"test", "test"},                     // helm test: neither install nor upgrade
		{"looked-up", ""},                    // generated behind lookup
		{"fixed", ""},                        // the same value both times
		{"post", "post-install, pre-delete"}, // install-only, with a delete event
	}
	var a, b, u []object
	for _, s := range specs {
		second := "2"
		if s.name == "fixed" {
			second = "1"
		}
		a = append(a, secret(s.name, s.events, "1"))
		b = append(b, secret(s.name, s.events, second))
		if s.name != "install-only" {
			u = append(u, secret(s.name, s.events, "3"))
		}
	}
	// Not a Secret: a ConfigMap whose value differs is not this check's.
	cm := object{Kind: "ConfigMap", Name: "plain", Doc: map[string]any{"data": map[string]any{"k": "1"}}}
	a, b = append(a, cm), append(b, object{Kind: "ConfigMap", Name: "plain", Doc: map[string]any{"data": map[string]any{"k": "2"}}})

	onUpgrade, onSync := regenerated(a, b, u, func(source string) bool { return strings.HasSuffix(source, "/looked-up.yaml") })
	names := func(lines []string) []string {
		var out []string
		for _, l := range lines {
			out = append(out, strings.TrimPrefix(strings.SplitN(l, "`", 3)[1], "Secret/"))
		}
		return out
	}
	if got, want := names(onUpgrade), []string{"both", "plain"}; !reflect.DeepEqual(got, want) {
		t.Errorf("onUpgrade = %v, want %v:\n%s", got, want, strings.Join(onUpgrade, "\n"))
	}
	if got, want := names(onSync), []string{"install-only", "post", "pre-install"}; !reflect.DeepEqual(got, want) {
		t.Errorf("onSync = %v, want %v:\n%s", got, want, strings.Join(onSync, "\n"))
	}
	for _, want := range []string{"rendered only at install (`.Release.IsInstall`)", "a `pre-install` hook: Helm runs it once, Argo CD on every sync"} {
		if !strings.Contains(strings.Join(onSync, "\n"), want) {
			t.Errorf("onSync lacks %q:\n%s", want, strings.Join(onSync, "\n"))
		}
	}
	if !strings.Contains(strings.Join(onUpgrade, "\n"), "a `pre-install,pre-upgrade` hook, run again on every upgrade") {
		t.Errorf("onUpgrade does not name the upgrade hook:\n%s", strings.Join(onUpgrade, "\n"))
	}
}
