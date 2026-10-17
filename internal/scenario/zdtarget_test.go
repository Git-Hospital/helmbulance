package scenario

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
	"github.com/egezamb/helmbulance/internal/helm"
	"github.com/egezamb/helmbulance/internal/kube"
)

// runZeroDowntimeUntilTarget runs zero-downtime with a helm that always succeeds and the given
// kubectl script, for the paths that end before any traffic is sent.
func runZeroDowntimeUntilTarget(t *testing.T, kubectl string) Result {
	t.Helper()
	bin := t.TempDir()
	for name, script := range map[string]string{"helm": "#!/bin/sh\nexit 0\n", "kubectl": kubectl} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	runner := &execx.Runner{}
	env := &Env{
		Runner: runner,
		Helm:   &helm.Client{Runner: runner, Timeout: time.Minute},
		Kube:   &kube.Client{Runner: runner},
		Chart:  "./chart",
		Log:    io.Discard,
	}
	return ZeroDowntime{}.Run(t.Context(), env)
}

func TestZeroDowntimeKubectlErrorIsInconclusive(t *testing.T) {
	res := runZeroDowntimeUntilTarget(t, "#!/bin/sh\ncase \"$*\" in *\"get services\"*) echo 'Unable to connect to the server' >&2; exit 1 ;; esac\nexit 0\n")
	if res.Verdict != Inconclusive {
		t.Errorf("verdict = %s (%s), want INCONCLUSIVE", res.Verdict, res.Summary)
	}
}

func TestZeroDowntimeWithoutHTTPPortIsSkipped(t *testing.T) {
	res := runZeroDowntimeUntilTarget(t, `#!/bin/sh
echo '{"items":[{"metadata":{"name":"db"},"spec":{"selector":{"app":"db"},"ports":[{"name":"pg","port":5432}]}}]}'
`)
	if res.Verdict != Skipped {
		t.Errorf("verdict = %s (%s), want SKIPPED", res.Verdict, res.Summary)
	}
}
