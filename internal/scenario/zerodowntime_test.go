package scenario

import (
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
	"github.com/egezamb/helmbulance/internal/helm"
	"github.com/egezamb/helmbulance/internal/kube"
)

// fakeKubectl answers the calls zero-downtime makes. "logs <pod>" prints $FAKE_LOGS/<pod>, and
// "logs --tail=1 <pod>", read just before the operation, prints the last line of $FAKE_LOGS/<pod>.before.
// The n-th "get endpointslices" (0 when the target is chosen, 1 right after the operation, 2 at the
// end of the tail) prints $FAKE_LOGS/slices.<n>, or no slices.
const fakeKubectl = `#!/bin/sh
for a in "$@"; do last="$a"; done
case "$*" in
  *"get services"*) echo '{"items":[{"metadata":{"name":"web"},"spec":{"selector":{"app":"web"},"ports":[{"name":"http","port":80}]}}]}' ;;
  *"get deployments,statefulsets"*) echo '{"items":[]}' ;;
  *"get endpointslices"*)
    n=$(cat "$FAKE_LOGS/slices.n" 2>/dev/null || echo 0); echo $((n+1)) > "$FAKE_LOGS/slices.n"
    if [ -f "$FAKE_LOGS/slices.$n" ]; then cat "$FAKE_LOGS/slices.$n"; else echo '{"items":[]}'; fi ;;
  *" logs --tail=1 "*) [ -f "$FAKE_LOGS/$last.before" ] && tail -n 1 "$FAKE_LOGS/$last.before" ;;
  *" logs "*) cat "$FAKE_LOGS/$last" ;;
esac
exit 0
`

// runZeroDowntime runs the scenario against fake kubectl and helm, with the given traffic logs and
// EndpointSlice reads (see fakeKubectl), after each opt changes the Env.
func runZeroDowntime(t *testing.T, logs map[string]string, opts ...func(*Env)) Result {
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
	for _, opt := range opts {
		opt(env)
	}
	return ZeroDowntime{}.Run(t.Context(), env)
}

// A chart's own EndpointSlice on web, as the version under test, a hook or a pod with RBAC can add.
const chartSlice = `{"items":[{"kind":"EndpointSlice","metadata":{"labels":{"kubernetes.io/service-name":"web"}},"endpoints":[{"addresses":["203.0.113.9"]}]}]}`

// Review round 2: the check before the operation does not see a slice the upgrade adds, so the
// chosen Service is read again; a gained address makes the run neither PASS nor FAIL.
func TestZeroDowntimeServiceGainsAnOutsideEndpoint(t *testing.T) {
	for name, reads := range map[string]map[string]string{
		"right after the operation": {"slices.1": chartSlice},
		"at the end of the tail":    {"slices.2": chartSlice},
	} {
		t.Run(name, func(t *testing.T) {
			logs := map[string]string{"hb-traffic-control": "100 ok\n", "hb-traffic": "9999999999 ok\n"}
			maps.Copy(logs, reads)
			res := runZeroDowntime(t, logs)
			if res.Verdict != Inconclusive || !strings.Contains(res.Summary, "web gained endpoints the control plane did not write: 203.0.113.9") {
				t.Errorf("verdict = %s (%s), want INCONCLUSIVE naming 203.0.113.9", res.Verdict, res.Summary)
			}
		})
	}
}

func TestZeroDowntimeEndpointsUnreadableAfterTheOperation(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{"hb-traffic-control": "100 ok\n", "hb-traffic": "9999999999 ok\n", "slices.1": "not json"})
	if res.Verdict != Inconclusive || !strings.Contains(res.Summary, "could not read the endpoints of web again") {
		t.Errorf("verdict = %s (%s), want INCONCLUSIVE", res.Verdict, res.Summary)
	}
}

// --traffic-target is the user's choice, so it is used as given and not checked, then or later.
func TestZeroDowntimeGivenTargetIsNotCheckedAgain(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control": "100 ok\n", "hb-traffic": "9999999999 ok\n",
		"slices.0": chartSlice, "slices.1": chartSlice, "slices.2": chartSlice,
	}, func(env *Env) { env.TrafficTarget = "web:80" })
	if res.Verdict != Pass {
		t.Errorf("verdict = %s (%s), want PASS", res.Verdict, res.Summary)
	}
}

func TestZeroDowntimeRepeatsAFailedControlOnce(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control":   "100 fail\n100 ok\n101 ok\n",
		"hb-traffic-control-2": "200 ok\n201 ok\n",
		"hb-traffic":           "9999999999 ok\n",
	})
	if res.Verdict != Pass {
		t.Errorf("verdict = %s (%s), want PASS", res.Verdict, res.Summary)
	}
}

func TestZeroDowntimeTwoFailedControls(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control":   "100 fail\n101 ok\n",
		"hb-traffic-control-2": "200 ok\n203 fail\n",
		"hb-traffic":           "9999999999 ok\n",
	})
	if res.Verdict != Inconclusive || !strings.Contains(res.Summary, "in two controls (the second failed at second 3)") {
		t.Errorf("verdict = %s (%s), want INCONCLUSIVE after two controls", res.Verdict, res.Summary)
	}
}

func TestZeroDowntimeCountsFailuresDuringTheOperation(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control": "100 ok\n",
		"hb-traffic.before":  "300 ok\n",
		"hb-traffic":         "300 ok\n301 ok\n302 fail\n",
	})
	if res.Verdict != Fail || !strings.HasPrefix(res.Summary, "1 of 3 requests failed") {
		t.Errorf("verdict = %s (%s), want FAIL with 1 of 3", res.Verdict, res.Summary)
	}
}

func TestZeroDowntimeIgnoresFailuresBeforeTheOperation(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control": "100 ok\n",
		"hb-traffic.before":  "298 fail\n299 ok\n",
		"hb-traffic":         "298 fail\n299 ok\n300 ok\n301 ok\n",
	})
	if res.Verdict != Pass {
		t.Errorf("verdict = %s (%s), want PASS", res.Verdict, res.Summary)
	}
}

// The window comes from the traffic pod's clock, so a node clock far behind this machine's does
// not hide the failures. Timestamps near 40 would all be "before" this machine's clock.
func TestZeroDowntimeUsesThePodsClock(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control": "100 ok\n",
		"hb-traffic.before":  "40 ok\n",
		"hb-traffic":         "40 ok\n41 fail\n42 fail\n43 ok\n",
	})
	if res.Verdict != Fail || !strings.HasPrefix(res.Summary, "2 of 4 requests failed") {
		t.Errorf("verdict = %s (%s), want FAIL with 2 of 4", res.Verdict, res.Summary)
	}
}

func TestZeroDowntimeEmptyWindowIsInconclusive(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control": "100 ok\n",
		"hb-traffic.before":  "500 ok\n",
		"hb-traffic":         "400 ok\n",
	})
	if res.Verdict != Inconclusive || !strings.HasPrefix(res.Summary, "no requests were sent during") {
		t.Errorf("verdict = %s (%s), want INCONCLUSIVE for an empty window", res.Verdict, res.Summary)
	}
}

func TestZeroDowntimeReportsANoisyFirstControl(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control":   "100 fail\n100 fail\n101 ok\n",
		"hb-traffic-control-2": "200 ok\n",
		"hb-traffic":           "300 ok\n301 fail\n",
	})
	if res.Verdict != Fail || len(res.Findings) != 1 {
		t.Fatalf("verdict = %s (%s), want FAIL", res.Verdict, res.Summary)
	}
	if ev := strings.Join(res.Findings[0].Evidence, " "); !strings.Contains(ev, "The first control lost 2 of 3 requests") {
		t.Errorf("evidence %q does not mention the noisy first control", ev)
	}
}

func TestTrafficImageIsPinnedByDigest(t *testing.T) {
	if _, digest, ok := strings.Cut(trafficImage, "@sha256:"); !ok || len(digest) != 64 {
		t.Errorf("trafficImage = %q, want it pinned by a sha256 digest", trafficImage)
	}
}

// A PodDisruptionBudget gates evictions, not rollouts, so the fix must not offer it as a fix for
// the rollout gap this scenario measures.
func TestZeroDowntimeFixSaysWhatAPDBDoes(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control": "100 ok\n",
		"hb-traffic":         "300 ok\n301 fail\n",
	})
	if len(res.Findings) != 1 {
		t.Fatalf("verdict = %s (%s), want one finding", res.Verdict, res.Summary)
	}
	fix := res.Findings[0].Fix
	if strings.Contains(fix, "PodDisruptionBudget") && !strings.Contains(fix, "evictions, not rollouts") {
		t.Errorf("fix offers a PodDisruptionBudget for a rollout gap: %s", fix)
	}
}
