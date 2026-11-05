package execx

import (
	"strconv"
	"strings"
)

// ReproLine turns a transcript line into a command a user can run: the sandbox kubeconfig becomes
// $KUBECONFIG, and each --set value that Mask replaced with *** becomes an environment variable
// the shell fills in, such as auth.password="${HB_SET_AUTH_PASSWORD:?…}". The values are never in
// the report, so the line names where to put them, and the shell stops with that message when one
// is not set. Other *** in the line, such as URL credentials, stay as they are.
func ReproLine(line string) string {
	fields := strings.Fields(line)
	n := &placeholders{byKey: map[string]string{}, taken: map[string]bool{}}
	for i := 0; i < len(fields); i++ {
		switch f := fields[i]; {
		case f == "--kubeconfig" && i+1 < len(fields):
			fields[i+1] = "$KUBECONFIG"
			i++
		case isSetFlag(f) && i+1 < len(fields):
			fields[i+1] = setPlaceholders(fields[i+1], n)
			i++
		case strings.HasPrefix(f, "--set") && strings.Contains(f, "="):
			flag, value, _ := strings.Cut(f, "=")
			if isSetFlag(flag) {
				fields[i] = flag + "=" + setPlaceholders(value, n)
			}
		}
	}
	return strings.Join(fields, " ")
}

// placeholders names the shell variables of one line: one per --set key, distinct even when two
// keys make the same name (a.b and a_b), and numbered ones for parts with no key.
type placeholders struct {
	byKey map[string]string
	taken map[string]bool
	n     int
}

func (p *placeholders) forKey(key string) string {
	if name, ok := p.byKey[key]; ok {
		return name // the same key again: helm keeps the last value, which this variable holds
	}
	name := "HB_SET_" + envName(key)
	for i := 2; p.taken[name]; i++ {
		name = "HB_SET_" + envName(key) + "_" + strconv.Itoa(i)
	}
	p.byKey[key], p.taken[name] = name, true
	return name
}

func (p *placeholders) unkeyed() string {
	for {
		p.n++
		if name := "HB_SET_" + strconv.Itoa(p.n); !p.taken[name] {
			p.taken[name] = true
			return name
		}
	}
}

// setPlaceholders replaces the masked parts of one --set value ("a=***,b.c=***", or "***" for a
// whole JSON object) with shell variables.
func setPlaceholders(value string, p *placeholders) string {
	if !strings.Contains(value, "***") {
		return value
	}
	parts := strings.Split(value, ",")
	for i, part := range parts {
		key, masked, ok := strings.Cut(part, "=")
		switch {
		case ok && masked == "***":
			parts[i] = key + "=" + shellVar(p.forKey(key))
		case part == "***":
			parts[i] = shellVar(p.unkeyed())
		}
	}
	return strings.Join(parts, ",")
}

func shellVar(name string) string {
	return `"${` + name + `:?set ` + name + ` to the value given with --set}"`
}

// envName makes a --set key into the tail of an environment variable name: letters and digits in
// upper case, anything else as _.
func envName(key string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(key) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
