package scan

import (
	"reflect"
	"testing"
)

// Only strings a user would write unquoted are tried, with keys --set-json can address.
func TestQuotedScalars(t *testing.T) {
	values := map[string]any{
		"port":    "5432",
		"enabled": "true",
		"off":     "false", // falsy unquoted: the difference is the user's intent
		"zero":    "0",
		"octal":   "0755",
		"name":    "web",
		"count":   3, // already a number
		"env":     map[string]any{"COPIES": "7", "a.b": "8", "REF": map[string]any{"key": "1"}},
	}
	want := []scalar{{"enabled", "true"}, {"env.COPIES", "7"}, {"env.REF.key", "1"}, {"port", "5432"}}
	if got := quotedScalars(values, nil); !reflect.DeepEqual(got, want) {
		t.Errorf("quotedScalars = %v, want %v", got, want)
	}
}

// A field under another name is a difference; the same fields with other values are not, and a
// list of named entries is matched by name, not position.
func TestShapeDiff(t *testing.T) {
	env := func(field string) []object {
		return []object{{Kind: "Deployment", Name: "app", Doc: map[string]any{"spec": map[string]any{
			"containers": []any{map[string]any{"name": "c", "env": []any{
				map[string]any{"name": "A", "value": "1"},
				map[string]any{"name": "PORT", field: "5432"},
			}}},
		}}}}
	}
	gone, added := shapeDiff(shapes(env("value")), shapes(env("valueFrom")))
	if !reflect.DeepEqual(gone, []string{"Deployment/app spec.containers[c].env[PORT].value"}) ||
		!reflect.DeepEqual(added, []string{"Deployment/app spec.containers[c].env[PORT].valueFrom"}) {
		t.Errorf("value to valueFrom: gone %v, added %v", gone, added)
	}
	if gone, added := shapeDiff(shapes(env("value")), shapes(env("value"))); len(gone)+len(added) > 0 {
		t.Errorf("same fields: gone %v, added %v", gone, added)
	}
}
