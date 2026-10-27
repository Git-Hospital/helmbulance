package execx

import (
	"regexp"
	"strings"
)

// urlPattern leaves out trailing punctuation, so "see http://a/b?q=1, then" keeps its comma.
var urlPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s'"]*[^\s'".,;:)]`)

// RedactURLs hides the user info and the query values of every URL in s, since both often carry
// tokens: a chart URL with credentials, or a --traffic-path such as /health?token=….
func RedactURLs(s string) string {
	return urlPattern.ReplaceAllStringFunc(s, redactURL)
}

// nextURL is a comma that starts another URL in the same match.
var nextURL = regexp.MustCompile(`,[a-zA-Z][a-zA-Z0-9+.-]*://`)

// redactURL hides each URL in a match. A single match can hold several, since helm takes a comma
// list of values files ("-f a,b"), and urlPattern keeps the comma; each URL is redacted on its own,
// so the second one's credentials are not left behind. The match is cut only at a comma followed by
// "scheme://": a comma is legal in user info and query values (https://u:pa,ss@host/?t=a,b).
func redactURL(match string) string {
	var out strings.Builder
	start := 0
	for _, loc := range nextURL.FindAllStringIndex(match, -1) {
		out.WriteString(redactOneURL(match[start:loc[0]]))
		out.WriteByte(',')
		start = loc[0] + 1
	}
	out.WriteString(redactOneURL(match[start:]))
	return out.String()
}

func redactOneURL(u string) string {
	scheme, rest, _ := strings.Cut(u, "://")
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	if at := strings.LastIndex(rest[:end], "@"); at >= 0 {
		rest = "***" + rest[at:]
	}
	q := strings.Index(rest, "?")
	if q < 0 {
		return scheme + "://" + rest
	}
	query, fragment, hasFragment := strings.Cut(rest[q+1:], "#")
	params := strings.Split(query, "&")
	for i, p := range params {
		if key, _, ok := strings.Cut(p, "="); ok {
			params[i] = key + "=***"
		}
	}
	rest = rest[:q+1] + strings.Join(params, "&")
	if hasFragment {
		rest += "#" + fragment
	}
	return scheme + "://" + rest
}
