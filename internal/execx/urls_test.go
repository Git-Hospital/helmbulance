package execx

import (
	"strings"
	"testing"
)

func TestRedactURLs(t *testing.T) {
	cases := map[string]string{
		"https://user:hunter2@charts.example.com/c-1.0.0.tgz":     "https://***@charts.example.com/c-1.0.0.tgz",
		"https://ghp_token@github.com/o/r":                        "https://***@github.com/o/r",
		"URL='http://hb-zd-web:80/health?token=abc&x=1'; while …": "URL='http://hb-zd-web:80/health?token=***&x=***'; while …",
		"http://svc:80/?flag#frag":                                "http://svc:80/?flag#frag",
		"oci://ghcr.io/o/charts/app --version 1.0.0":              "oci://ghcr.io/o/charts/app --version 1.0.0",
		"see http://a/b?q=1, then http://u:p@c/d":                 "see http://a/b?q=***, then http://***@c/d",
		"http://svc/p?a=1#frag":                                   "http://svc/p?a=***#frag",
		"no url here, just user@host and a:b":                     "no url here, just user@host and a:b",
		// A comma list of values files is one match; each URL's credentials must go (review item 1).
		"https://u:p1@a.example/v1.yaml,https://u:p2@b.example/v2.yaml": "https://***@a.example/v1.yaml,https://***@b.example/v2.yaml",
		// A comma inside the user info or a query value is not a list separator (review round 2).
		"https://u:pa,ss@host.example/charts/app.tgz":                     "https://***@host.example/charts/app.tgz",
		"--from oci://deploy:s3cr,et@registry.example.com/charts/app":     "--from oci://***@registry.example.com/charts/app",
		"https://h.example/x.tgz?token=abc,def":                           "https://h.example/x.tgz?token=***",
		"https://u:p,1@a.example/v1.yaml,https://u:p,2@b.example/v2.yaml": "https://***@a.example/v1.yaml,https://***@b.example/v2.yaml",
		// A password written unencoded may hold ', /, ? or #: the user info must still go.
		"https://bob:pa'ss@charts.example/x.tgz": "https://***@charts.example/x.tgz",
		"https://bob:pa/ss@charts.example/x.tgz": "https://***@charts.example/x.tgz",
		"https://bob:pa?ss@charts.example/x.tgz": "https://***@charts.example/x.tgz",
		"https://bob:pa#ss@charts.example/x.tgz": "https://***@charts.example/x.tgz",
		// A query value with ' is masked whole, and so is a key=value fragment (an OAuth implicit token).
		"https://charts.example/x.tgz?token=ab'cd":       "https://charts.example/x.tgz?token=***",
		"https://charts.example/x.tgz#access_token=abcd": "https://charts.example/x.tgz#access_token=***",
		"http://svc/p?a=1#k=v&frag":                      "http://svc/p?a=***#k=***&frag",
		// An @ after the path with no user:password before it is not user info, and neither is one
		// after a port or an IPv6 literal (a local registry, a traffic target's path).
		"oci://ghcr.io/o/charts/app@sha256:0123abcd":      "oci://ghcr.io/o/charts/app@sha256:0123abcd",
		"oci://localhost:5000/charts/app@sha256:0123abcd": "oci://localhost:5000/charts/app@sha256:0123abcd",
		"http://svc:80/api/users/@me":                     "http://svc:80/api/users/@me",
		"http://[::1]:8080/p@v1":                          "http://[::1]:8080/p@v1",
		"http://svc:8080/health?email=a@b.example":        "http://svc:8080/health?email=***",
		// URLs joined by a quote, a semicolon or a bar are redacted one by one.
		"'http://a.example/p','http://u:pw-1234@b.example/p'": "'http://a.example/p','http://***@b.example/p'",
		"http://a.example/p;http://u:pw-1234@b.example/p":     "http://a.example/p;http://***@b.example/p",
		"http://a.example/p|http://u:pw-1234@b.example/p":     "http://a.example/p|http://***@b.example/p",
		// A password that starts with // does not start another URL, and digits before a ? in a
		// password are not a port.
		"https://bob://pw-1234@h.example/x": "https://***@h.example/x",
		"https://bob:8?pw-1234@h.example/x": "https://***@h.example/x",
		// A closing bracket or brace after the URL is not part of it.
		"['https://h.example/x?token=s3cret']":    "['https://h.example/x?token=***']",
		"{url: https://h.example/x?token=s3cret}": "{url: https://h.example/x?token=***}",
	}
	for in, want := range cases {
		if got := RedactURLs(in); got != want {
			t.Errorf("RedactURLs(%q) = %q, want %q", in, got, want)
		}
	}
}

// --from, a remote chart and the traffic pod's script all go into report.json's commands.
func TestMaskRedactsURLs(t *testing.T) {
	got := Mask("helm", []string{"install", "r", "https://u:hunter2@example.com/c.tgz"})
	if strings.Contains(got, "hunter2") {
		t.Errorf("Mask = %q, want the URL's credentials hidden", got)
	}
}

func TestRunnerRedactsURLsInErrors(t *testing.T) {
	_, stderr, err := (&Runner{}).RunCapture(t.Context(), "/bin/sh", "-c",
		`echo "Error: failed to fetch https://u:hunter2@example.com/c.tgz : 401" >&2; exit 1`, "https://u:hunter2@example.com/c.tgz")
	if err == nil || strings.Contains(err.Error(), "hunter2") || strings.Contains(string(stderr), "hunter2") {
		t.Errorf("err = %v, stderr = %q, want the credentials hidden", err, stderr)
	}
}
