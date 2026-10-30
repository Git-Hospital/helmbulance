package scan

import (
	"reflect"
	"testing"
)

// Containers are found in any pod spec, init containers included; a name counts once per container.
func TestDuplicateEnv(t *testing.T) {
	env := func(names ...string) []any {
		var list []any
		for _, n := range names {
			list = append(list, map[string]any{"name": n})
		}
		return list
	}
	cronJob := map[string]any{"spec": map[string]any{"jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"initContainers": []any{map[string]any{"name": "init", "env": env("A", "A")}},
		"containers":     []any{map[string]any{"name": "c", "env": env("A", "B", "B", "B")}},
	}}}}}}
	got := containersIn(cronJob)
	if len(got) != 2 || str(got[0]["name"]) != "init" || str(got[1]["name"]) != "c" {
		t.Fatalf("containersIn = %v, want init and c", got)
	}
	if d := duplicateNames(got[1]["env"]); !reflect.DeepEqual(d, []string{"`B` 3 times"}) {
		t.Errorf("duplicateNames = %v, want B 3 times", d)
	}
	if d := duplicateNames(env("A", "B")); len(d) != 0 {
		t.Errorf("distinct names reported: %v", d)
	}
}
