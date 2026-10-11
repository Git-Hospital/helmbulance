package execx

import "strings"

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
