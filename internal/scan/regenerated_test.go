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
	var a, b []object
	u := map[string]bool{}
	for _, s := range specs {
		second := "2"
		if s.name == "fixed" {
			second = "1"
		}
		a = append(a, secret(s.name, s.events, "1"))
		b = append(b, secret(s.name, s.events, second))
		if s.name != "install-only" {
			u["Secret/"+s.name] = true
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

	// A chart that refuses to render as an upgrade (Bitnami's "PASSWORDS ERROR"): hooks are still
	// sorted by their annotation, every other Secret goes to HB-SCAN-003 as before #107.
	onUpgrade, onSync = regenerated(a, b, nil, func(source string) bool { return strings.HasSuffix(source, "/looked-up.yaml") })
	if got, want := names(onUpgrade), []string{"both", "install-only", "plain"}; !reflect.DeepEqual(got, want) {
		t.Errorf("no upgrade render: onUpgrade = %v, want %v", got, want)
	}
	if got, want := names(onSync), []string{"post", "pre-install"}; !reflect.DeepEqual(got, want) {
		t.Errorf("no upgrade render: onSync = %v, want %v", got, want)
	}
}

func TestHelmError(t *testing.T) {
	for err, want := range map[string]string{
		// helm 4.3, bitnami/postgresql 12.1.6 with --is-upgrade: the message starts on the next line.
		"helm template hb ./postgresql --is-upgrade: exit status 1: Error: execution error at (postgresql/templates/secrets.yaml:17:24): \nPASSWORDS ERROR: You must provide your current passwords when upgrading the release.\n                 Note that even after reinstallation, old credentials may be needed\n": "execution error at (postgresql/templates/secrets.yaml:17:24): PASSWORDS ERROR: You must provide your current passwords when upgrading the release.",
		// A one-line fail, with helm's hint after it.
		"helm template: exit status 1: Error: execution error at (c/templates/s.yaml:1:31): no upgrade\n\nUse --debug flag to render out invalid YAML": "execution error at (c/templates/s.yaml:1:31): no upgrade",
		"signal: killed": "signal: killed",
	} {
		if got := helmError(err); got != want {
			t.Errorf("helmError(%q) = %q, want %q", err, got, want)
		}
	}
}
