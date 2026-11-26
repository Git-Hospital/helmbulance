package scenario

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Git-Hospital/helmbulance/internal/helm"
	"github.com/Git-Hospital/helmbulance/internal/oracle"
)

// maxRotationRenders caps how many values rotationCandidates tries, one render each.
var maxRotationRenders = 40

// rotationCandidate is a values key whose change alters a Secret the release renders: the value
// secret-rotation changes before it upgrades (#187).
type rotationCandidate struct {
	Key  string   // the values path, as --set takes it
	Refs []string // the "secret/key" entries it changes, sorted
}

// rotationCandidates renders the chart once per credential-named string value (in the chart's
// defaults or the -f files) set to a new random string, and keeps the values that change a key of
// a Secret the release renders in ns. It renders client-side, as `helm template` does, so a key
// that changes between two renders of the same values (randAlphaNum without lookup) changes on its
// own; such a key counts only when a render with every value set agrees on it. A render that fails for one value (a schema, a `fail`) skips that
// value: whether the chart refuses the change is for the upgrade to show. Keys of TLS material
// (oracle.KeyClass) do not count. skipped names the values
// beyond maxRotationRenders and those whose render failed.
func rotationCandidates(ctx context.Context, env *Env, release, ns string) (cands []rotationCandidate, skipped []string, err error) {
	keys, err := credentialValueKeys(ctx, env)
	if err != nil {
		return nil, nil, err
	}
	if len(keys) == 0 {
		return nil, nil, nil
	}
	render := func(extra ...string) (oracle.SecretSnapshot, error) {
		out, err := env.Helm.Template(ctx, release, ns, env.Chart, env.Values, false, extra...)
		if err != nil {
			return nil, err
		}
		return env.Hasher.Snapshot(helm.RenderedSecrets(out, ns)), nil
	}
	base, err := render()
	if err != nil {
		return nil, nil, fmt.Errorf("render the chart: %w", err)
	}
	again, err := render()
	if err != nil {
		return nil, nil, fmt.Errorf("render the chart again: %w", err)
	}
	unstable := map[string]bool{}
	for _, c := range oracle.Diff(base, again) {
		unstable[c.Ref] = true
	}
	if len(keys) > maxRotationRenders {
		skipped = append(skipped, keys[maxRotationRenders:]...)
		keys = keys[:maxRotationRenders]
	}
	type tried struct {
		key, value string
		refs       []string
		random     []string // keys that are random by default and changed with this value
		snap       oracle.SecretSnapshot
	}
	var done []tried
	var all []string
	for _, k := range keys {
		value, err := rotatedValue()
		if err != nil {
			return nil, nil, err
		}
		snap, err := render("--set-string", k+"="+value)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			skipped = append(skipped, k)
			continue
		}
		t := tried{key: k, value: value, snap: snap}
		for _, c := range oracle.Diff(base, snap) {
			switch {
			case c.Kind != oracle.Changed:
			case unstable[c.Ref]:
				t.random = append(t.random, c.Ref)
			default:
				t.refs = append(t.refs, c.Ref)
			}
		}
		done = append(done, t)
		if !removes(base, snap) {
			all = append(all, "--set-string", k+"="+value)
		}
	}
	// A key that is random by default may be the value itself once it is set, as a chart that
	// generates a password only when none is given. One render with every value set, each to the
	// value it had alone, tells: the key is that value's when both renders hash it the same. A
	// key that stays random differs between any two renders. A value that removes a key, such as
	// an existingSecret that stops the chart rendering its own, stays out of that render, and a
	// key the render still lacks is checked with a second render of its value alone.
	var together oracle.SecretSnapshot
	for _, t := range done {
		if len(t.random) > 0 {
			if snap, err := render(all...); err == nil {
				together = snap
			}
			break
		}
	}
	for _, t := range done {
		var again oracle.SecretSnapshot
		for _, ref := range t.random {
			h, ok := together[ref]
			if !ok {
				if again == nil {
					if again, err = render("--set-string", t.key+"="+t.value); err != nil {
						again = oracle.SecretSnapshot{}
					}
				}
				h, ok = again[ref]
			}
			if ok && h == t.snap[ref] {
				t.refs = append(t.refs, ref)
			}
		}
		// A certificate or its key is not something a random string rotates: the app would fail
		// to parse it, which says nothing about how the chart handles a rotation.
		t.refs = slices.DeleteFunc(t.refs, func(ref string) bool {
			_, key, _ := strings.Cut(ref, "/")
			return oracle.KeyClass(key) == oracle.ClassTLS
		})
		if len(t.refs) > 0 {
			sort.Strings(t.refs)
			cands = append(cands, rotationCandidate{Key: t.key, Refs: t.refs})
		}
	}
	return cands, skipped, nil
}

// removes reports whether after lacks a key that before has.
func removes(before, after oracle.SecretSnapshot) bool {
	for ref := range before {
		if _, ok := after[ref]; !ok {
			return true
		}
	}
	return false
}

// credentialValueKeys lists the string values whose name is a credential (oracle.ValuesKeyClass),
// from the chart's defaults and the -f files, in key order and each once. A value the user sets
// with --set is left as it is: helm applies the user's --set after ours, so it would win anyway.
func credentialValueKeys(ctx context.Context, env *Env) ([]string, error) {
	out, err := env.Helm.ShowValues(ctx, env.Chart)
	if err != nil {
		return nil, fmt.Errorf("read the chart's values: %w", err)
	}
	docs := [][]byte{out}
	for _, f := range env.Values.Files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		docs = append(docs, b)
	}
	set := map[string]bool{}
	for _, s := range env.Values.Sets {
		for _, a := range strings.Split(s, ",") {
			if k, _, ok := strings.Cut(a, "="); ok {
				set[k] = true
			}
		}
	}
	var keys []string
	for _, d := range docs {
		var values map[string]any
		if err := yaml.Unmarshal(d, &values); err != nil {
			return nil, fmt.Errorf("parse values: %w", err)
		}
		for _, k := range credentialStrings(values, nil) {
			if !set[k] && !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// credentialStrings walks values and returns the paths of string leaves with a credential name.
// Keys that --set cannot address plainly (a dot, a bracket, a comma, an equals sign) are skipped,
// as are lists.
func credentialStrings(values map[string]any, path []string) []string {
	var out []string
	for k, v := range values {
		if strings.ContainsAny(k, ".[],=\\") {
			continue
		}
		p := append(slices.Clone(path), k)
		switch v := v.(type) {
		case map[string]any:
			out = append(out, credentialStrings(v, p)...)
		case string:
			if oracle.ValuesKeyClass(k) == oracle.ClassCredential {
				out = append(out, strings.Join(p, "."))
			}
		}
	}
	return out
}

// rotatedValue is a new random value: letters and digits only, so a chart's validation of a
// password accepts it, and 24 of them.
func rotatedValue() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return "hb" + string(b), nil
}
