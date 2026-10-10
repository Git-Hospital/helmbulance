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
	}
	for _, c := range cases {
		if got := Mask("helm", c.args); got != c.want {
			t.Errorf("Mask(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}
