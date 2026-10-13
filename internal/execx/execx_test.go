package execx

import (
	"strings"
	"testing"
)

func TestMask(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"upgrade", "r", "c", "--set", "a=1,b.c=secret"}, "helm upgrade r c --set a=***,b.c=***"},
		{[]string{"install", "r", "c", "--set-string=pw=hunter2"}, "helm install r c --set-string=pw=***"},
		{[]string{"install", "r", "c", "-f", "values.yaml"}, "helm install r c -f values.yaml"},
		{[]string{"install", "r", "c", "--set", `pw=hunter\,2secret`}, "helm install r c --set pw=***"},
		{[]string{"install", "r", "c", "--set", "list={a,secret},x=1"}, "helm install r c --set list=***,x=***"},
		{[]string{"install", "r", "c", "--set-literal", "pw=a,b=c"}, "helm install r c --set-literal pw=***"},
		{[]string{"install", "r", "c", `--set-json=cfg={"user":"u","pw":"p"}`}, "helm install r c --set-json=cfg=***"},
	}
	for _, c := range cases {
		if got := Mask("helm", c.args); got != c.want {
			t.Errorf("Mask(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestRunnerKeepsTheErrorMarker(t *testing.T) {
	r := &Runner{Secrets: SetValues([]string{"logLevel=Error", "pw=hunter2secret"})}
	_, stderr, err := r.RunCapture(t.Context(), "/bin/sh", "-c", `echo "W1012 warning: value hunter2secret" >&2; echo "Error: INSTALLATION FAILED: bad hunter2secret" >&2; exit 1`)
	if err == nil || !strings.HasSuffix(err.Error(), ": Error: INSTALLATION FAILED: bad ***") {
		t.Errorf("error = %v, want only the redacted Error line", err)
	}
	if strings.Contains(string(stderr), "hunter2secret") {
		t.Errorf("stderr = %q, want the value redacted", stderr)
	}
}

func TestMaskJSONAndLiteral(t *testing.T) {
	for args, want := range map[[2]string]string{
		{"--set-json", `{"pw":"hunter2=x"}`}: "helm --set-json ***",
		{"--set-literal", `k\=x=secret`}:     `helm --set-literal k\=***`,
	} {
		if got := Mask("helm", args[:]); got != want {
			t.Errorf("Mask(%q) = %q, want %q", args, got, want)
		}
	}
}
