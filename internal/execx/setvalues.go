package execx

import (
	"slices"
	"strings"
)

// splitAssignments splits a --set value into its assignments the way Helm does: a comma ends an
// assignment unless it is escaped with a backslash or sits inside a {list}.
func splitAssignments(s string) []string {
	var parts []string
	var cur strings.Builder
	depth, escaped := 0, false
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '{':
			depth++
		case r == '}' && depth > 0:
			depth--
		case r == ',' && depth == 0:
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	return append(parts, cur.String())
}

// cutAssignment splits "key=value" at the first '=' that is not escaped.
func cutAssignment(a string) (key, value string, ok bool) {
	escaped := false
	for i, r := range a {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '=':
			return a[:i], a[i+1:], true
		}
	}
	return a, "", false
}

// SetKeys returns the keys of --set values, never the values.
func SetKeys(sets []string) []string {
	var keys []string
	for _, s := range sets {
		for _, a := range splitAssignments(s) {
			if k, _, ok := cutAssignment(a); ok {
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// minRedact is the shortest value Redact hides. Shorter values ("1", "true", "app") are rarely
// secrets, and hiding them everywhere would make error text unreadable.
const minRedact = 4

// SetValues returns the values of --set assignments as they may appear in a command's output:
// as written, unescaped, and each item of a {list}.
func SetValues(sets []string) []string {
	var values []string
	add := func(v string) {
		if len(v) >= minRedact && !slices.Contains(values, v) {
			values = append(values, v)
		}
	}
	for _, s := range sets {
		for _, a := range splitAssignments(s) {
			_, v, ok := cutAssignment(a)
			if !ok {
				continue
			}
			add(v)
			add(unescape(v))
			if strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}") {
				for _, item := range splitAssignments(v[1 : len(v)-1]) {
					add(unescape(item))
				}
			}
		}
	}
	return values
}

func unescape(s string) string {
	var b strings.Builder
	escaped := false
	for _, r := range s {
		if !escaped && r == '\\' {
			escaped = true
			continue
		}
		escaped = false
		b.WriteRune(r)
	}
	return b.String()
}

// Redact replaces every occurrence of the given values in text with ***, longest first so a value
// that contains another is hidden whole.
func Redact(text string, values []string) string {
	sorted := slices.Clone(values)
	slices.SortFunc(sorted, func(a, b string) int { return len(b) - len(a) })
	for _, v := range sorted {
		text = strings.ReplaceAll(text, v, "***")
	}
	return text
}
