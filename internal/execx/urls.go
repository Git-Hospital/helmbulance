package execx

import (
	"regexp"
	"strings"
)

// urlPattern leaves out trailing punctuation, so "see http://a/b?q=1, then" keeps its comma, and a
// closing quote, bracket or brace, so URL='http://…' and ['http://…'] end before it. A ' inside the
// URL is kept: a password or a token pasted unencoded can hold one.
var urlPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"]*[^\s'".,;:)\]}]`)

// RedactURLs hides the user info, the query values and the key=value parts of the fragment of every
// URL in s, since all of them can carry tokens: a chart URL with credentials, a --traffic-path such
// as /health?token=…, or an access_token in a fragment.
func RedactURLs(s string) string {
	return urlPattern.ReplaceAllStringFunc(s, redactURL)
}

// nextURL is a separator followed by another URL in the same match: a comma list of values files,
// or URLs joined by a quote, a semicolon or a bar ('a','b', a;b, a|b). A / is not one: a password
// pasted unencoded can start with // (https://bob://pw@host).
var nextURL = regexp.MustCompile(`[,';|][a-zA-Z][a-zA-Z0-9+.-]*://`)

// redactURL hides each URL in a match. A single match can hold several, since helm takes a comma
// list of values files ("-f a,b") and urlPattern keeps commas and quotes; each URL is redacted on its
// own, so the second one's credentials are not left behind. The match is cut only where "scheme://"
// starts: a comma or a quote is legal in user info and query values (https://u:pa,ss@host/?t=a,b).
func redactURL(match string) string {
	var out strings.Builder
	start := 0
	for _, loc := range nextURL.FindAllStringIndex(match, -1) {
		out.WriteString(redactOneURL(match[start:loc[0]]))
		out.WriteByte(match[loc[0]])
		start = loc[0] + 1
	}
	out.WriteString(redactOneURL(match[start:]))
	return out.String()
}

func redactOneURL(u string) string {
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		return u
	}
	if at := userInfoEnd(rest); at >= 0 {
		rest = "***" + rest[at:]
	}
	rest, fragment, hasFragment := strings.Cut(rest, "#")
	if path, query, ok := strings.Cut(rest, "?"); ok {
		rest = path + "?" + maskValues(query)
	}
	if hasFragment {
		rest += "#" + maskValues(fragment)
	}
	return scheme + "://" + rest
}

// userInfoEnd returns the index of the @ that ends the user info in rest, the URL after "://", or -1
// if it has none. The authority ends at the first /, ? or #, but a password pasted unencoded can hold
// any of them (https://bob:pa/ss@host/x), so a later @ still ends the user info when a "user:" comes
// before it. An @ further on is not user info when no colon comes before the first /, ? or #, as in
// an OCI digest (oci://ghcr.io/o/app@sha256:…), when that colon starts a port followed by a path
// (localhost:5000/…@…, svc:80/users/@me), or when the host is an IPv6 literal ([::1]:8080/p@v1).
//
// Known limits: a user info with no colon that holds a / (a token pasted unencoded) is not found,
// nor the rest of a password that holds both @ and /, nor a password of digits only before a /.
func userInfoEnd(rest string) int {
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	if at := strings.LastIndex(rest[:end], "@"); at >= 0 {
		return at
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return -1
	}
	if strings.HasPrefix(rest, "[") {
		return -1
	}
	if colon := strings.Index(rest[:at], ":"); colon < 0 || colon > end || (rest[end] == '/' && isPort(rest[colon+1:end])) {
		return -1
	}
	return at
}

// isPort reports whether s, the text between a colon and the end of the authority, is a port number.
func isPort(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// maskValues hides the value of every key=value pair in an & list, a query or a fragment. A part
// with no = (a plain flag or an anchor) is kept.
func maskValues(s string) string {
	parts := strings.Split(s, "&")
	for i, p := range parts {
		if key, _, ok := strings.Cut(p, "="); ok {
			parts[i] = key + "=***"
		}
	}
	return strings.Join(parts, "&")
}
