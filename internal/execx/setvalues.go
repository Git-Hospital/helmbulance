package execx

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// splitAssignments splits a --set value into its assignments the way Helm does: a comma ends an
// assignment unless it is escaped with a backslash or sits inside a {list}. Like Helm, only a '{'
// right after the first '=' opens a list, and lists do not nest.
func splitAssignments(s string) []string {
	var parts []string
	var cur strings.Builder
	inList, escaped, sawEq, valueStart := false, false, false, false
	for _, r := range s {
		start := valueStart
		valueStart = false
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '=' && !sawEq:
			sawEq, valueStart = true, true
		case r == '{' && start:
			inList = true
		case r == '}' && inList:
			inList = false
		case r == ',' && !inList:
			parts = append(parts, cur.String())
			cur.Reset()
			sawEq = false
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

// minRedact is the shortest value, in characters, that Redact hides. Shorter values ("1", "app")
// are rarely secrets, and hiding them everywhere would make error text unreadable.
const minRedact = 4

// quoted lists the escaped forms a value takes in error text: Go %q (Kubernetes field errors,
// printf "%q" in a chart's fail), JSON (fail with toJson), and Helm's single-quoted schema errors.
func quoted(v string) []string {
	q := strconv.Quote(v)
	j, _ := json.Marshal(v)
	single := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`, "\t", `\t`).Replace(v)
	return []string{q[1 : len(q)-1], string(j[1 : len(j)-1]), single}
}

// SetValues returns the values of --set assignments as they may appear in a command's output:
// as written, unescaped, each item of a {list}, and the quoted forms of each. true, false and null
// are left out: Helm turns them into non-strings, and they are not secrets.
func SetValues(sets []string) []string {
	var values []string
	add := func(v string) {
		if utf8.RuneCountInString(v) < minRedact || slices.Contains(values, v) {
			return
		}
		switch strings.ToLower(v) {
		case "true", "false", "null":
			return
		}
		values = append(values, v)
	}
	addAll := func(v string) {
		add(v)
		for _, q := range quoted(v) {
			add(q)
		}
	}
	for _, s := range sets {
		for _, a := range splitAssignments(s) {
			_, v, ok := cutAssignment(a)
			if !ok {
				continue
			}
			add(v)
			addAll(unescape(v))
			if strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}") {
				for _, item := range splitAssignments(v[1 : len(v)-1]) {
					addAll(unescape(item))
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
