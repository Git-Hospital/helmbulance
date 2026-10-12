package scenario

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
	"github.com/egezamb/helmbulance/internal/helm"
	"github.com/egezamb/helmbulance/internal/kube"
)

// fakeKubectl answers the calls zero-downtime makes. "logs <pod>" prints $FAKE_LOGS/<pod>.
const fakeKubectl = `#!/bin/sh
for a in "$@"; do last="$a"; done
case "$*" in
  *"get services"*) echo '{"items":[{"metadata":{"name":"web"},"spec":{"selector":{"app":"web"},"ports":[{"name":"http","port":80}]}}]}' ;;
  *"get deployments,statefulsets"*) echo '{"items":[]}' ;;
  *" logs "*) cat "$FAKE_LOGS/$last" ;;
esac
exit 0
`

// runZeroDowntime runs the scenario against fake kubectl and helm, with the given traffic logs.
func runZeroDowntime(t *testing.T, logs map[string]string) Result {
	t.Helper()
	c, l, tl := controlDuration, trafficLead, trafficTail
	t.Cleanup(func() { controlDuration, trafficLead, trafficTail = c, l, tl })
	controlDuration, trafficLead, trafficTail = time.Millisecond, time.Millisecond, time.Millisecond
	bin, logDir := t.TempDir(), t.TempDir()
	for name, script := range map[string]string{"kubectl": fakeKubectl, "helm": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for pod, log := range logs {
		if err := os.WriteFile(filepath.Join(logDir, pod), []byte(log), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_LOGS", logDir)
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

func TestZeroDowntimeCountsFailuresDuringTheOperation(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control": "100 ok\n",
		"hb-traffic":         "9999999998 ok\n9999999999 fail\n",
	})
	if res.Verdict != Fail || !strings.HasPrefix(res.Summary, "1 of 2 requests failed") {
		t.Errorf("verdict = %s (%s), want FAIL with 1 of 2", res.Verdict, res.Summary)
	}
}

func TestZeroDowntimeIgnoresFailuresBeforeTheOperation(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control": "100 ok\n",
		"hb-traffic":         "300 fail\n9999999999 ok\n",
	})
	if res.Verdict != Pass {
		t.Errorf("verdict = %s (%s), want PASS", res.Verdict, res.Summary)
	}
}
