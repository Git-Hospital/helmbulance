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

func TestBraceInsideValueIsNotAList(t *testing.T) {
	for set, first := range map[string]string{
		"pw=Ab{cdef9,user=hunter2secret":   "Ab{cdef9",
		"pw=secretval{,user=hunter2secret": "secretval{",
	} {
		if got := SetKeys([]string{set}); !slices.Equal(got, []string{"pw", "user"}) {
			t.Errorf("SetKeys(%q) = %q, want [pw user]", set, got)
		}
		text := "pw: '" + first + "' user: hunter2secret"
		if got := Redact(text, SetValues([]string{set})); got != "pw: '***' user: ***" {
			t.Errorf("Redact with %q = %q, want both values hidden", set, got)
		}
	}
}

func TestRedactQuotedForms(t *testing.T) {
	values := SetValues([]string{`a=pa'ss1234`, `b=pa"ss5678`, "c=P&ss<w>rd!", "d=line1\nline2"})
	for _, text := range []string{
		`at '/a': 'pa\'ss1234' does not match pattern`,
		`Invalid value: "pa\"ss5678"`,
		`{"c":"P&ss<w>rd!"}`,
		`fail: "line1\nline2"`,
	} {
		if got := Redact(text, values); !strings.Contains(got, "***") {
			t.Errorf("Redact(%q) = %q, want the value hidden", text, got)
		}
	}
}

func TestSetValuesSkipsBooleansAndShortValues(t *testing.T) {
	if got := SetValues([]string{"a=true,b=NULL,c=False,d=abc"}); len(got) != 0 {
		t.Errorf("SetValues = %q, want none", got)
	}
	if got := SetValues([]string{"a=şşşş"}); len(got) == 0 {
		t.Error("a four-character value should be redacted")
	}
}
