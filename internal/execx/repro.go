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
	n := 0
	for i := 0; i < len(fields); i++ {
		switch f := fields[i]; {
		case f == "--kubeconfig" && i+1 < len(fields):
			fields[i+1] = "$KUBECONFIG"
			i++
		case isSetFlag(f) && i+1 < len(fields):
			fields[i+1] = setPlaceholders(fields[i+1], &n)
			i++
		case strings.HasPrefix(f, "--set") && strings.Contains(f, "="):
			flag, value, _ := strings.Cut(f, "=")
			if isSetFlag(flag) {
				fields[i] = flag + "=" + setPlaceholders(value, &n)
			}
		}
	}
	return strings.Join(fields, " ")
}

// setPlaceholders replaces the masked parts of one --set value ("a=***,b.c=***", or "***" for a
// whole JSON object) with shell variables. n numbers the parts that have no key.
func setPlaceholders(value string, n *int) string {
	if !strings.Contains(value, "***") {
		return value
	}
	parts := strings.Split(value, ",")
	for i, p := range parts {
		key, masked, ok := strings.Cut(p, "=")
		switch {
		case ok && masked == "***":
			parts[i] = key + "=" + shellVar("HB_SET_"+envName(key))
		case p == "***":
			*n++
			parts[i] = shellVar("HB_SET_" + strconv.Itoa(*n))
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
