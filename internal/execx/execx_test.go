package execx

import "testing"

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
