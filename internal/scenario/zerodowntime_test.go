package scenario

import (
	"context"
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

// fakeKubectl answers the calls zero-downtime makes. "logs <pod>" prints $FAKE_LOGS/<pod>. The n-th
// "logs --tail=1 <pod>" (0 just before the operation, then one before each workload after the first
// is restarted) prints the last line of $FAKE_LOGS/<pod>.before.<n>, or of $FAKE_LOGS/<pod>.before.
// The workloads are $FAKE_LOGS/workloads, or one Deployment behind web. "rollout" calls are appended to $FAKE_LOGS/calls.
// The n-th "get endpointslices" (0 when the target is chosen, 1 right after the operation, 2 at the
// end of the tail) prints $FAKE_LOGS/slices.<n>, or no slices.
const fakeKubectl = `#!/bin/sh
for a in "$@"; do last="$a"; done
case "$*" in
  *"get services"*) echo '{"items":[{"metadata":{"name":"web"},"spec":{"selector":{"app":"web"},"ports":[{"name":"http","port":80}]}}]}' ;;
  *"get deployments,statefulsets"*) cat "$FAKE_LOGS/workloads" 2>/dev/null || echo '{"items":[{"kind":"Deployment","metadata":{"name":"web"},"spec":{"replicas":2,"template":{"metadata":{"labels":{"app":"web"}}}}}]}' ;;
  *" rollout "*) echo "$*" | sed 's/.* rollout /rollout /' >> "$FAKE_LOGS/calls" ;;
  *"get endpointslices"*)
    n=$(cat "$FAKE_LOGS/slices.n" 2>/dev/null || echo 0); echo $((n+1)) > "$FAKE_LOGS/slices.n"
    if [ -f "$FAKE_LOGS/slices.$n" ]; then cat "$FAKE_LOGS/slices.$n"; else echo '{"items":[]}'; fi ;;
  *" logs --tail=1 "*)
    n=$(cat "$FAKE_LOGS/tail.n" 2>/dev/null || echo 0); echo $((n+1)) > "$FAKE_LOGS/tail.n"
    f="$FAKE_LOGS/$last.before.$n"; [ -f "$f" ] || f="$FAKE_LOGS/$last.before"
    [ -f "$f" ] && tail -n 1 "$f" ;;
  *" logs "*) cat "$FAKE_LOGS/$last" ;;
esac
exit 0
`

// runZeroDowntime runs the scenario against fake kubectl and helm, with the given traffic logs and
// EndpointSlice reads (see fakeKubectl), after each opt changes the Env.
func runZeroDowntime(t *testing.T, logs map[string]string, opts ...func(*Env)) Result {
	t.Helper()
	return runZeroDowntimePath(t, logs, "", opts...)
}

// runZeroDowntimePath is runZeroDowntime with a --traffic-path.
func runZeroDowntimePath(t *testing.T, logs map[string]string, path string, opts ...func(*Env)) Result {
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
		Runner:      runner,
		Helm:        &helm.Client{Runner: runner, Timeout: time.Minute},
		Kube:        &kube.Client{Runner: runner},
		Chart:       "./chart",
		TrafficPath: path,
		Log:         io.Discard,
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

// A --traffic-path can carry a token; the summary and evidence go into shared reports.
func TestZeroDowntimeHidesTheTrafficPathQuery(t *testing.T) {
	for name, logs := range map[string]map[string]string{
		"failed controls":  {"hb-traffic-control": "100 fail\n", "hb-traffic-control-2": "200 fail\n"},
		"failed treatment": {"hb-traffic-control": "100 ok\n", "hb-traffic": "300 ok\n301 fail\n"},
	} {
		res := runZeroDowntimePath(t, logs, "/health?token=hunter2")
		text := res.Summary
		for _, f := range res.Findings {
			text += strings.Join(f.Evidence, " ") + strings.Join(f.Reproduce, " ")
		}
		if !strings.Contains(text, "token=***") || strings.Contains(text, "hunter2") {
			t.Errorf("%s: want the token hidden, got: %s", name, text)
		}
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

// web serves the target Service; db does not, and web's pods would depend on it.
const twoWorkloads = `{"items":[
	{"kind":"Deployment","metadata":{"name":"web"},"spec":{"replicas":2,"strategy":{"type":"RollingUpdate"},"template":{"metadata":{"labels":{"app":"web"}},"spec":{"containers":[{"readinessProbe":{},"lifecycle":{"preStop":{}}}]}}}},
	{"kind":"StatefulSet","metadata":{"name":"db"},"spec":{"replicas":1,"updateStrategy":{"type":"RollingUpdate"},"template":{"metadata":{"labels":{"app":"db"}},"spec":{"containers":[{}]}}}}
]}`

// The rolling restart replaces one workload at a time and blames each failure on the workload that
// was restarting then, not on the one behind the Service (#10): here web restarts cleanly from second
// 300, and the requests fail after db starts restarting at second 310.
func TestZeroDowntimeBlamesTheWorkloadThatWasRestarting(t *testing.T) {
	logDir := ""
	res := runZeroDowntime(t, map[string]string{
		"workloads":           twoWorkloads,
		"hb-traffic-control":  "100 ok\n",
		"hb-traffic.before.0": "300 ok\n",
		"hb-traffic.before.1": "310 ok\n",
		"hb-traffic":          "300 ok\n305 ok\n310 ok\n311 fail\n312 fail\n313 ok\n",
	}, func(env *Env) { logDir = os.Getenv("FAKE_LOGS") })
	if res.Verdict != Fail || len(res.Findings) != 1 {
		t.Fatalf("verdict = %s (%s), want FAIL", res.Verdict, res.Summary)
	}
	ev := strings.Join(res.Findings[0].Evidence, "\n")
	for _, want := range []string{
		"While `StatefulSet/db` restarted (1 pod): 2 of 4 requests failed",
		"`StatefulSet/db` does not serve `web`; the pods behind `web` may depend on it",
		"`StatefulSet/db` has no readiness probe",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence does not contain %q:\n%s", want, ev)
		}
	}
	if strings.Contains(ev, "While `Deployment/web`") || strings.Contains(ev, "`Deployment/web` has") {
		t.Errorf("evidence blames web, which restarted without a failure:\n%s", ev)
	}
	calls, _ := os.ReadFile(filepath.Join(logDir, "calls"))
	want := "rollout restart deployment/web\nrollout status deployment/web --timeout 1m0s\n" +
		"rollout restart statefulset/db\nrollout status statefulset/db --timeout 1m0s\n"
	if string(calls) != want {
		t.Errorf("rollout calls =\n%s\nwant one workload at a time:\n%s", calls, want)
	}
}

// A PASS says how much was replaced, so an empty restart is not mistaken for a clean one (#10).
func TestZeroDowntimePassCountsTheReplacedPods(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"workloads":          twoWorkloads,
		"hb-traffic-control": "100 ok\n",
		"hb-traffic.before":  "300 ok\n",
		"hb-traffic":         "300 ok\n301 ok\n",
	})
	if res.Verdict != Pass || !strings.Contains(res.Summary, "2 workloads, 3 pods") {
		t.Errorf("verdict = %s (%s), want PASS naming 2 workloads, 3 pods", res.Verdict, res.Summary)
	}
}

// A release whose Service has no Deployment, StatefulSet or DaemonSet behind it has nothing to
// replace, so a restart without failures would be an empty PASS (#10).
func TestZeroDowntimeNothingToRestartIsSkipped(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"workloads":          `{"items":[]}`,
		"hb-traffic-control": "100 ok\n",
		"hb-traffic":         "300 ok\n",
	})
	if res.Verdict != Skipped || !strings.Contains(res.Summary, "nothing would be replaced") {
		t.Errorf("verdict = %s (%s), want SKIPPED", res.Verdict, res.Summary)
	}
}

// With one workload, its window is the whole operation: no second line repeats the first.
func TestZeroDowntimeOneWorkloadHasNoWindowLine(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"hb-traffic-control": "100 ok\n",
		"hb-traffic.before":  "300 ok\n",
		"hb-traffic":         "300 ok\n301 fail\n",
	})
	if len(res.Findings) != 1 {
		t.Fatalf("verdict = %s (%s), want one finding", res.Verdict, res.Summary)
	}
	if ev := strings.Join(res.Findings[0].Evidence, "\n"); strings.Contains(ev, "While `") {
		t.Errorf("evidence repeats the whole operation as a window:\n%s", ev)
	}
}

// Review #85, point 1: a restart that replaces nothing behind the target Service proves nothing
// about it, so it is SKIPPED, not a PASS.
func TestZeroDowntimeNothingBehindTheServiceIsSkipped(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"workloads":          `{"items":[{"kind":"Deployment","metadata":{"name":"other"},"spec":{"replicas":2,"template":{"metadata":{"labels":{"app":"other"}}}}}]}`,
		"hb-traffic-control": "100 ok\n",
		"hb-traffic":         "300 ok\n",
	})
	if res.Verdict != Skipped || !strings.Contains(res.Summary, "would not replace the pods behind web") {
		t.Errorf("verdict = %s (%s), want SKIPPED naming web", res.Verdict, res.Summary)
	}
}

// A PASS says how much of what it replaced is behind the target Service.
func TestZeroDowntimePassCountsWhatIsBehindTheService(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"workloads":          twoWorkloads,
		"hb-traffic-control": "100 ok\n",
		"hb-traffic":         "300 ok\n",
	})
	if res.Verdict != Pass || !strings.Contains(res.Summary, "1 workload, 2 pods behind web") {
		t.Errorf("verdict = %s (%s), want PASS naming 1 workload, 2 pods behind web", res.Verdict, res.Summary)
	}
}

// Review #85, point 2: rollout status returns while the last old pod still shuts down, so the
// scenario waits before it reads the next workload's start; that pod's failures stay in its own
// workload's window.
func TestZeroDowntimeWaitsBeforeTheNextWorkload(t *testing.T) {
	logDir := ""
	s := settle
	t.Cleanup(func() { settle = s })
	settle = func(ctx context.Context, d time.Duration) error {
		f, err := os.OpenFile(filepath.Join(logDir, "calls"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString("settle\n")
		return err
	}
	runZeroDowntime(t, map[string]string{
		"workloads":          twoWorkloads,
		"hb-traffic-control": "100 ok\n",
		"hb-traffic":         "300 ok\n",
	}, func(env *Env) { logDir = os.Getenv("FAKE_LOGS") })
	calls, _ := os.ReadFile(filepath.Join(logDir, "calls"))
	want := "rollout restart deployment/web\nrollout status deployment/web --timeout 1m0s\nsettle\n" +
		"rollout restart statefulset/db\nrollout status statefulset/db --timeout 1m0s\n"
	if string(calls) != want {
		t.Errorf("calls =\n%s\nwant a wait between the workloads, not after the last:\n%s", calls, want)
	}
}

// One pod on one node, replaced with maxSurge 0: the DaemonSet's own update stops the old pod first.
const oneNodeDaemonSet = `{"items":[
	{"kind":"DaemonSet","metadata":{"name":"web"},"spec":{"updateStrategy":{"type":"RollingUpdate","rollingUpdate":{"maxSurge":0,"maxUnavailable":1}},"template":{"metadata":{"labels":{"app":"web"}},"spec":{"containers":[{"readinessProbe":{},"lifecycle":{"preStop":{}}}]}}},"status":{"desiredNumberScheduled":1}}
]}`

// Review #85, point 3: on the one-node sandbox, a DaemonSet with maxSurge 0 always leaves its
// Service without an endpoint while its pod is replaced. That comes from the sandbox, not the
// chart, so it is INCONCLUSIVE, not a finding.
func TestZeroDowntimeOneNodeDaemonSetIsInconclusive(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"workloads":          oneNodeDaemonSet,
		"hb-traffic-control": "100 ok\n",
		"hb-traffic.before":  "300 ok\n",
		"hb-traffic":         "300 ok\n301 fail\n302 ok\n",
	})
	if res.Verdict != Inconclusive || !strings.Contains(res.Summary, "`DaemonSet/web`") || len(res.Findings) != 0 {
		t.Errorf("verdict = %s (%s), %d findings; want INCONCLUSIVE naming DaemonSet/web", res.Verdict, res.Summary, len(res.Findings))
	}
}

// Review #85, smaller point: windows that finished before a later rollout failed are complete
// measurements, so their failures are still a finding.
func TestZeroDowntimeKeepsFailuresBeforeAFailedRollout(t *testing.T) {
	bin := t.TempDir()
	// The fake kubectl, except that the StatefulSet's rollout never finishes.
	script := strings.Replace(fakeKubectl, `  *" rollout "*)`, `  *"rollout status statefulset/db"*) echo "timed out waiting for the condition" >&2; exit 1 ;;
  *" rollout "*)`, 1)
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	res := runZeroDowntime(t, map[string]string{
		"workloads":           twoWorkloads,
		"hb-traffic-control":  "100 ok\n",
		"hb-traffic.before.0": "300 ok\n",
		"hb-traffic.before.1": "310 ok\n",
		// db's window is cut off by its failed rollout, so its failure at 311 does not count.
		"hb-traffic": "300 ok\n301 fail\n302 fail\n303 ok\n310 ok\n311 fail\n",
	}, func(env *Env) { t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH")) })
	if res.Verdict != Fail || len(res.Findings) != 1 || !strings.HasPrefix(res.Summary, "2 of 4 requests failed") {
		t.Fatalf("verdict = %s (%s), want FAIL with 2 of 4, from web's finished window only", res.Verdict, res.Summary)
	}
	ev := strings.Join(res.Findings[0].Evidence, "\n")
	for _, want := range []string{"While `Deployment/web` restarted (2 pods): 2 of 4 requests failed", "the restart of `StatefulSet/db` failed"} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence does not contain %q:\n%s", want, ev)
		}
	}
}

// On two or more nodes the DaemonSet's gap is the chart's, and the cause names maxSurge, not a
// replica count a DaemonSet does not have.
func TestZeroDowntimeDaemonSetCauseIsMaxSurge(t *testing.T) {
	res := runZeroDowntime(t, map[string]string{
		"workloads":          strings.Replace(oneNodeDaemonSet, `"desiredNumberScheduled":1`, `"desiredNumberScheduled":2`, 1),
		"hb-traffic-control": "100 ok\n",
		"hb-traffic.before":  "300 ok\n",
		"hb-traffic":         "300 ok\n301 fail\n302 ok\n",
	})
	if res.Verdict != Fail || len(res.Findings) != 1 {
		t.Fatalf("verdict = %s (%s), want FAIL", res.Verdict, res.Summary)
	}
	ev := strings.Join(res.Findings[0].Evidence, "\n")
	if !strings.Contains(ev, "`DaemonSet/web` updates with maxSurge 0") || strings.Contains(ev, "replica;") {
		t.Errorf("evidence does not name maxSurge as the cause:\n%s", ev)
	}
}
