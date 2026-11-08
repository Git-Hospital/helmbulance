package execx

import (
	"os/exec"
	"strings"
	"testing"
)

func TestReproLine(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{"helm upgrade r ./chart --kubeconfig /home/ege/.helmbulance/clusters/x/kubeconfig --namespace ns",
			"helm upgrade r ./chart --kubeconfig $KUBECONFIG --namespace ns"},
		{"helm install r ./chart --set auth.password=***,image.tag=*** --namespace ns",
			`helm install r ./chart --set 'auth.password'="${HB_SET_AUTH_PASSWORD:?set HB_SET_AUTH_PASSWORD to the value given with --set}",'image.tag'="${HB_SET_IMAGE_TAG:?set HB_SET_IMAGE_TAG to the value given with --set}" --namespace ns`},
		{"helm install r ./chart --set-json=***",
			`helm install r ./chart --set-json="${HB_SET_1:?set HB_SET_1 to the value given with --set}"`},
		// a.b and a_b make the same name; the second gets its own, so each can hold its value.
		{"helm install r ./chart --set a.b=*** --set a_b=***",
			`helm install r ./chart --set 'a.b'="${HB_SET_A_B:?set HB_SET_A_B to the value given with --set}" --set 'a_b'="${HB_SET_A_B_2:?set HB_SET_A_B_2 to the value given with --set}"`},
		{"helm install r oci://***@registry.example/chart --set-string a=b",
			"helm install r oci://***@registry.example/chart --set-string a=b"},
	} {
		if got := ReproLine(c.line); got != c.want {
			t.Errorf("ReproLine(%q) =\n %s\nwant\n %s", c.line, got, c.want)
		}
	}
}

// The line ReproLine makes from a masked --set runs in a shell and passes the value back when the
// variable is set, and stops with its message when it is not (#58).
func TestReproLineRunsInAShell(t *testing.T) {
	line := ReproLine(Mask("echo", []string{"--set", "auth.password=s3cr3t,image.tag=1.2"}))
	out, err := exec.Command("sh", "-c", line).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "set HB_SET_AUTH_PASSWORD to the value given with --set") {
		t.Errorf("unset variable: err %v, output %q; want the shell to stop with the message", err, out)
	}
	cmd := exec.Command("sh", "-c", line)
	cmd.Env = append(cmd.Environ(), "HB_SET_AUTH_PASSWORD=s3cr3t", "HB_SET_IMAGE_TAG=1.2")
	out, err = cmd.CombinedOutput()
	if err != nil || string(out) != "--set auth.password=s3cr3t,image.tag=1.2\n" {
		t.Errorf("with the variables set: %q, %v; want the original --set value back", out, err)
	}
}

func TestReproLinePreservesSetKeysInShells(t *testing.T) {
	for _, shell := range []string{"sh", "bash", "zsh"} {
		if _, err := exec.LookPath(shell); err != nil {
			t.Logf("%s unavailable", shell)
			continue
		}
		for _, key := range []string{`nodeSelector.kubernetes\.io/os`, `ingress.hosts[0].host`, `a'b`} {
			t.Run(shell+"/"+key, func(t *testing.T) {
				line := ReproLine("printf '%s' --set " + key + "=***")
				cmd := exec.Command(shell, "-c", line)
				cmd.Env = append(cmd.Environ(), "HB_SET_"+envName(key)+"=value")
				out, err := cmd.CombinedOutput()
				if err != nil || string(out) != "--set"+key+"=value" {
					t.Fatalf("output %q, error %v; want original key %q", out, err, key)
				}
			})
		}
	}
}
