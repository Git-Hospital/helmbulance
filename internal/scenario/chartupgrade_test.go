package scenario

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
	"github.com/egezamb/helmbulance/internal/helm"
	"github.com/egezamb/helmbulance/internal/kube"
	"github.com/egezamb/helmbulance/internal/oracle"
)

// runChartUpgrade runs chart-upgrade against a fake helm whose upgrade to the chart under test (the
// only call without --version) prints upgradeErr and fails, and a fake kubectl whose n-th
// `get pods` prints pods[n] (then the last one; an empty namespace when none is given).
func runChartUpgrade(t *testing.T, upgradeErr string, pods ...string) Result {
	t.Helper()
	return runChartUpgradeWith(t, upgradeErr, 0, pods...)
}

// runChartUpgradeWith is runChartUpgrade with two more cases: an empty upgradeErr makes the upgrade
// succeed, and cancelAt > 0 cancels the run once kubectl has answered that many `get pods`. A pods
// entry that starts with "FAIL:" makes that `get pods` fail with the rest as its error.
func runChartUpgradeWith(t *testing.T, upgradeErr string, cancelAt int, pods ...string) Result {
	t.Helper()
	bin := t.TempDir()
	if len(pods) == 0 {
		pods = []string{`{"items":[]}`}
	}
	for i, p := range pods {
		if err := os.WriteFile(filepath.Join(bin, "pods."+strconv.Itoa(i)), []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	kubectlScript := `#!/bin/sh
case "$*" in
  *"get pods"*)
    n=$(cat "` + bin + `/n" 2>/dev/null || echo 0)
    f="` + bin + `/pods.$n"; [ -f "$f" ] || f="` + bin + `/pods.` + strconv.Itoa(len(pods)-1) + `"
    echo $((n+1)) > "` + bin + `/n"
    case "$(cat "$f")" in FAIL:*) cut -c6- "$f" >&2; exit 1 ;; esac
    cat "$f" ;;
  *) echo '{"items":[]}' ;;
esac
`
	helmScript := `#!/bin/sh
case "$*" in
  *"get metadata"*) echo '{"version":"1.0.0"}'; exit 0 ;;
  upgrade*--version*|install*|uninstall*) exit 0 ;;
  upgrade*) [ -z "` + upgradeErr + `" ] && exit 0; echo "` + upgradeErr + `" >&2; exit 1 ;;
esac
exit 0
`
	for name, script := range map[string]string{"helm": helmScript, "kubectl": kubectlScript} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	hasher, err := oracle.NewHasher()
	if err != nil {
		t.Fatal(err)
	}
	runner := &execx.Runner{}
	env := &Env{
		Runner:      runner,
		Helm:        &helm.Client{Runner: runner, Timeout: time.Minute},
		Kube:        &kube.Client{Runner: runner},
		Hasher:      hasher,
		Chart:       "./v2",
		From:        "repo/chart",
		FromVersion: "1.0.0",
		Log:         io.Discard,
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if cancelAt > 0 {
		go func() {
			for ctx.Err() == nil {
				if n, _ := os.ReadFile(filepath.Join(bin, "n")); strings.TrimSpace(string(n)) == strconv.Itoa(cancelAt) {
					cancel()
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}
	return ChartUpgrade{}.Run(ctx, env)
}

// #49 (1): an interrupt while the pods settle, after a clean upgrade, proved neither health nor a
// problem. It was PASS, exit 0.
func TestChartUpgradeInterruptedWhileSettling(t *testing.T) {
	shortSettle(t, time.Hour, time.Hour)
	pod := items(podJSON("web", 0, ""))
	// Reads before Settle: pods before the upgrade, containers before it, pods right after it.
	res := runChartUpgradeWith(t, "", 4, pod)
	if res.Verdict != Inconclusive || len(res.Findings) > 0 || !strings.Contains(res.Summary, "interrupted") {
		t.Errorf("verdict = %s with %d finding(s) (%s), want INCONCLUSIVE saying it was interrupted", res.Verdict, len(res.Findings), res.Summary)
	}
}

// #49 (1): a cluster whose pods cannot be read until the deadline is not a chart whose workloads
// are unhealthy. It was FAIL HB-CU-003, high.
func TestChartUpgradeUnreadablePodsAreInconclusive(t *testing.T) {
	shortSettle(t, 20*time.Millisecond, 5*time.Millisecond)
	pod := items(podJSON("web", 0, ""))
	res := runChartUpgradeWith(t, "", 0, pod, pod, pod, "FAIL:The connection to the server was refused")
	if res.Verdict != Inconclusive || len(res.Findings) > 0 || !strings.Contains(res.Summary, "connection to the server was refused") {
		t.Errorf("verdict = %s with %d finding(s) (%s), want INCONCLUSIVE with the read error", res.Verdict, len(res.Findings), res.Summary)
	}
}

// A finding the Secrets already proved stands when the health check could not finish; the summary
// says what was not checked. Without one the result is INCONCLUSIVE.
func TestHealthUnread(t *testing.T) {
	err := errors.New("interrupted while the pods settled: context canceled")
	res := healthUnread(Result{Verdict: Pass, Summary: "upgraded"}, err)
	if res.Verdict != Inconclusive || !strings.Contains(res.Summary, "context canceled") {
		t.Errorf("no finding: verdict = %s (%s), want INCONCLUSIVE with the reason", res.Verdict, res.Summary)
	}
	res = healthUnread(Result{Verdict: Fail, Summary: "1 problem(s)", Findings: []Finding{{ID: "HB-CU-002"}}}, err)
	if res.Verdict != Fail || len(res.Findings) != 1 || !strings.Contains(res.Summary, "workload health was not checked") {
		t.Errorf("with HB-CU-002: verdict = %s (%s), want FAIL kept and the health check named", res.Verdict, res.Summary)
	}
}

func TestChartUpgradeTimeoutIsInconclusive(t *testing.T) {
	res := runChartUpgrade(t, "Error: UPGRADE FAILED: resource Deployment/ns/web not ready. status: InProgress\ncontext deadline exceeded")
	if res.Verdict != Inconclusive || len(res.Findings) > 0 {
		t.Errorf("verdict = %s with %d finding(s) (%s), want INCONCLUSIVE and none", res.Verdict, len(res.Findings), res.Summary)
	}
}

// etcd under load or a chart host that does not serve the chart is the run, not the chart (#49).
func TestChartUpgradeServerErrorIsInconclusive(t *testing.T) {
	for _, msg := range []string{
		"Error: UPGRADE FAILED: etcdserver: leader changed",
		"Error: failed to fetch https://charts.example.com/x-1.0.0.tgz : 503 Service Unavailable",
	} {
		res := runChartUpgrade(t, msg)
		if res.Verdict != Inconclusive || len(res.Findings) > 0 {
			t.Errorf("%q: verdict = %s with %d finding(s) (%s), want INCONCLUSIVE and none", msg, res.Verdict, len(res.Findings), res.Summary)
		}
	}
}

func TestChartUpgradeRejectionIsAFinding(t *testing.T) {
	for _, msg := range []string{
		"Error: UPGRADE FAILED: cannot patch db with kind StatefulSet: spec: Forbidden: field is immutable",
		// An object over etcd's request limit: the same on every cluster, so the chart's (#84).
		"Error: UPGRADE FAILED: failed to create resource: etcdserver: request is too large",
	} {
		res := runChartUpgrade(t, msg)
		if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-CU-001" {
			t.Errorf("%q: verdict = %s (%s), want FAIL with HB-CU-001", msg, res.Verdict, res.Summary)
		}
	}
}

// podJSON is one pod with one container, for the fake kubectl: its UID, restart count, and the
// reason it waits ("" when it runs).
func podJSON(uid string, restarts int, waiting string) string {
	state, ready := `{"running":{}}`, "True"
	if waiting != "" {
		state, ready = `{"waiting":{"reason":"`+waiting+`"}}`, "False"
	}
	return `{"metadata":{"name":"web-` + uid + `","uid":"` + uid + `","ownerReferences":[{"kind":"ReplicaSet","name":"web"}]},` +
		`"status":{"phase":"Running","conditions":[{"type":"Ready","status":"` + ready + `"}],` +
		`"containerStatuses":[{"name":"app","restartCount":` + strconv.Itoa(restarts) + `,"state":` + state +
		`,"lastState":{"terminated":{"reason":"Error","exitCode":1}}}]}}`
}

func items(pods ...string) string { return `{"items":[` + strings.Join(pods, ",") + `]}` }

// notReady turns a podJSON pod's Ready condition to False, leaving its containers running.
func notReady(pod string) string {
	return strings.Replace(pod, `"type":"Ready","status":"True"`, `"type":"Ready","status":"False"`, 1)
}

// podInitJSON is a Ready pod whose init container restarted initRestarts times before it completed.
func podInitJSON(uid string, initRestarts int) string {
	return `{"metadata":{"name":"web-` + uid + `","uid":"` + uid + `","ownerReferences":[{"kind":"ReplicaSet","name":"web"}]},` +
		`"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],` +
		`"initContainerStatuses":[{"name":"wait","restartCount":` + strconv.Itoa(initRestarts) + `,"state":{"terminated":{"reason":"Completed","exitCode":0}}}],` +
		`"containerStatuses":[{"name":"app","restartCount":0,"state":{"running":{}}}]}}`
}

// ownedBy moves a podJSON pod to another ReplicaSet of the same Deployment, as an upgrade does.
func ownedBy(pod, replicaSet string) string {
	return strings.Replace(pod, `"kind":"ReplicaSet","name":"web"`, `"kind":"ReplicaSet","name":"`+replicaSet+`"`, 1)
}

// #28: a new version that crash-loops ends in helm's --wait timeout; the crash makes it a finding.
func TestChartUpgradeTimeoutWithACrashIsAFinding(t *testing.T) {
	const timeout = "Error: UPGRADE FAILED: resource Deployment/ns/web not ready. status: InProgress\ncontext deadline exceeded"
	for name, c := range map[string]struct {
		before, after    string
		containersBefore string // the containers read before the upgrade, when not the pods before
		want             string
	}{
		"new pod crash-loops":            {items(podJSON("old", 0, "")), items(podJSON("old", 0, ""), podJSON("new", 3, "CrashLoopBackOff")), "", Fail},
		"new pod cannot read its config": {items(podJSON("old", 0, "")), items(podJSON("new", 0, "CreateContainerConfigError")), "", Fail},
		"old pod restarts since":         {items(podJSON("old", 1, "")), items(podJSON("old", 2, ""), podJSON("new", 0, "ContainerCreating")), "", Fail},
		"new pod pulls its image":        {items(podJSON("old", 0, "")), items(podJSON("old", 0, ""), podJSON("new", 0, "ImagePullBackOff")), "", Inconclusive},
		"old restarts from before only":  {items(podJSON("old", 2, "")), items(podJSON("old", 2, ""), podJSON("new", 0, "ContainerCreating")), "", Inconclusive},
		// Review round 1, item 2: an init container that restarted while v1 started (it waits for a
		// database, say) is not a crash of the new version.
		"old init restarts from before": {items(podInitJSON("old", 1)), items(podInitJSON("old", 1), podJSON("new", 0, "ContainerCreating")), "", Inconclusive},
		// Item 3: a crash loop v1 already had (the pod was not Ready before) is not blamed on the upgrade.
		// A v1 pod that runs but was not Ready (its liveness probe restarts it) is not blamed either.
		"v1 pod already unhealthy":  {items(notReady(podJSON("old", 3, ""))), items(notReady(podJSON("old", 5, "")), podJSON("new", 0, "ContainerCreating")), "", Inconclusive},
		"crash loop v1 already had": {items(podJSON("old", 3, "CrashLoopBackOff")), items(podJSON("old", 5, "CrashLoopBackOff"), podJSON("new", 0, "ContainerCreating")), "", Inconclusive},
		// Round 2: a container that crashes once on its first start in both versions is the app's
		// startup (row F of #24), so a timeout from another workload stays INCONCLUSIVE. The new pod
		// is matched to the v1 pods by workload, not by its ReplicaSet, which the upgrade replaced.
		"first-start crash both versions have": {items(ownedBy(podJSON("old", 1, ""), "web-5d8f7c")), items(ownedBy(podJSON("new", 1, ""), "web-7b9c4d")), "", Inconclusive},
		"new pod restarts more than v1's did":  {items(ownedBy(podJSON("old", 1, ""), "web-5d8f7c")), items(ownedBy(podJSON("new", 2, ""), "web-7b9c4d")), "", Fail},
		// Without the containers from before there is nothing to compare restarts with.
		"no containers from before, restarted":  {items(podJSON("old", 0, "")), items(podJSON("new", 1, "")), "not json", Inconclusive},
		"no containers from before, crash loop": {items(podJSON("old", 0, "")), items(podJSON("new", 3, "CrashLoopBackOff")), "not json", Fail},
	} {
		t.Run(name, func(t *testing.T) {
			// Reads: pods before the upgrade, containers before it, containers after the timeout.
			containersBefore := c.before
			if c.containersBefore != "" {
				containersBefore = c.containersBefore
			}
			res := runChartUpgrade(t, timeout, c.before, containersBefore, c.after)
			if res.Verdict != c.want {
				t.Fatalf("verdict = %s (%s), want %s", res.Verdict, res.Summary, c.want)
			}
			if c.want == Fail && (len(res.Findings) != 1 || res.Findings[0].ID != "HB-CU-001" ||
				!strings.Contains(strings.Join(res.Findings[0].Evidence, " "), "pod `web-")) {
				t.Errorf("findings = %+v, want HB-CU-001 naming the pod", res.Findings)
			}
		})
	}
}

// Only a --wait timeout with the release applied is worth reading pods or collecting evidence for;
// a cancelled run, the chart's own webhook, a pull error or a render refusal are not. The upgrade
// (#34) and both installs of reinstall (#30) share it. Helm 4 stops at a resource that reports
// Failed without waiting out the timeout, with the release applied all the same.
func TestTimedOutWaiting(t *testing.T) {
	for msg, want := range map[string]bool{
		"Error: UPGRADE FAILED: context deadline exceeded":                                                              true,
		"Error: UPGRADE FAILED: timed out waiting for the condition":                                                    true,
		"Error: INSTALLATION FAILED: timed out waiting for the condition":                                               true,
		"Error: INSTALLATION FAILED: resource StatefulSet/ns/db not ready. status: Failed, message: 1 pods have failed": true,
		"Error: UPGRADE FAILED: failed calling webhook \"x\": context deadline exceeded":                                false,
		"Error: UPGRADE FAILED: cannot patch db with kind StatefulSet: spec: Forbidden: field is immutable":             false,
		"Failed to pull image: toomanyrequests: rate limit":                                                             false,
		"Error: INSTALLATION FAILED: execution error at (demo/templates/secret.yaml:3:4)":                               false,
		// A failed pre-install or pre-upgrade hook stops before the manifest is applied.
		"Error: INSTALLATION FAILED: failed pre-install: resource Job/ns/x not ready. status: Failed, message: x": false,
		"Error: UPGRADE FAILED: failed pre-upgrade: context deadline exceeded":                                    false,
	} {
		if got := timedOutWaiting(t.Context(), errors.New(msg)); got != want {
			t.Errorf("timedOutWaiting(%q) = %v, want %v", msg, got, want)
		}
	}
	if timedOutWaiting(t.Context(), nil) {
		t.Error("no error counts as a --wait timeout")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if timedOutWaiting(ctx, errors.New("context deadline exceeded")) {
		t.Error("a cancelled run counts as a --wait timeout")
	}
}
