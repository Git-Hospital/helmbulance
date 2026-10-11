package execx

import (
	"slices"
	"strings"
	"testing"
)

func TestSetKeys(t *testing.T) {
	got := SetKeys([]string{`pw=hunter\,2secret`, "list={a,b},x=1", `dotted\.key=v`})
	want := []string{"pw", "list", "x", `dotted\.key`}
	if !slices.Equal(got, want) {
		t.Errorf("SetKeys = %q, want %q", got, want)
	}
}

func TestSetValuesAndRedact(t *testing.T) {
	values := SetValues([]string{`pw=hunter\,2secret`, "tags={alpha,bravo},n=1"})
	text := `values don't meet the specifications: pw: "hunter,2secret" is too long; tags: [alpha bravo]; n: 1`
	want := `values don't meet the specifications: pw: "***" is too long; tags: [*** ***]; n: 1`
	if got := Redact(text, values); got != want {
		t.Errorf("Redact = %q, want %q", got, want)
	}
}

func TestRunnerRedactsErrors(t *testing.T) {
	r := &Runner{Env: []string{"V=hunter2"}, Secrets: []string{"hunter2"}}
	_, err := r.Run(t.Context(), "/bin/sh", "-c", `echo "bad value $V" >&2; exit 1`)
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error = %v, want the value redacted", err)
	}
}
