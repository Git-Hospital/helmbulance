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
    cat "$f"; echo $((n+1)) > "` + bin + `/n" ;;
  *) echo '{"items":[]}' ;;
esac
`
	helmScript := `#!/bin/sh
case "$*" in
  *"get metadata"*) echo '{"version":"1.0.0"}'; exit 0 ;;
  upgrade*--version*|install*|uninstall*) exit 0 ;;
  upgrade*) echo "` + upgradeErr + `" >&2; exit 1 ;;
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
	return ChartUpgrade{}.Run(t.Context(), env)
}

func TestChartUpgradeTimeoutIsInconclusive(t *testing.T) {
	res := runChartUpgrade(t, "Error: UPGRADE FAILED: resource Deployment/ns/web not ready. status: InProgress\ncontext deadline exceeded")
	if res.Verdict != Inconclusive || len(res.Findings) > 0 {
		t.Errorf("verdict = %s with %d finding(s) (%s), want INCONCLUSIVE and none", res.Verdict, len(res.Findings), res.Summary)
	}
}

func TestChartUpgradeRejectionIsAFinding(t *testing.T) {
	res := runChartUpgrade(t, "Error: UPGRADE FAILED: cannot patch db with kind StatefulSet: spec: Forbidden: field is immutable")
	if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-CU-001" {
		t.Errorf("verdict = %s (%s), want FAIL with HB-CU-001", res.Verdict, res.Summary)
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

// #28: a new version that crash-loops ends in helm's --wait timeout; the crash makes it a finding.
func TestChartUpgradeTimeoutWithACrashIsAFinding(t *testing.T) {
	const timeout = "Error: UPGRADE FAILED: resource Deployment/ns/web not ready. status: InProgress\ncontext deadline exceeded"
	for name, c := range map[string]struct {
		before, after string
		want          string
	}{
		"new pod crash-loops":            {items(podJSON("old", 0, "")), items(podJSON("old", 0, ""), podJSON("new", 3, "CrashLoopBackOff")), Fail},
		"new pod cannot read its config": {items(podJSON("old", 0, "")), items(podJSON("new", 0, "CreateContainerConfigError")), Fail},
		"old pod restarts since":         {items(podJSON("old", 1, "")), items(podJSON("old", 2, ""), podJSON("new", 0, "ContainerCreating")), Fail},
		"new pod pulls its image":        {items(podJSON("old", 0, "")), items(podJSON("old", 0, ""), podJSON("new", 0, "ImagePullBackOff")), Inconclusive},
		"old restarts from before only":  {items(podJSON("old", 2, "")), items(podJSON("old", 2, ""), podJSON("new", 0, "ContainerCreating")), Inconclusive},
		// Review round 1, item 2: an init container that restarted while v1 started (it waits for a
		// database, say) is not a crash of the new version.
		"old init restarts from before": {items(podInitJSON("old", 1)), items(podInitJSON("old", 1), podJSON("new", 0, "ContainerCreating")), Inconclusive},
		// Item 3: a crash loop v1 already had (the pod was not Ready before) is not blamed on the upgrade.
		// A v1 pod that runs but was not Ready (its liveness probe restarts it) is not blamed either.
		"v1 pod already unhealthy":  {items(notReady(podJSON("old", 3, ""))), items(notReady(podJSON("old", 5, "")), podJSON("new", 0, "ContainerCreating")), Inconclusive},
		"crash loop v1 already had": {items(podJSON("old", 3, "CrashLoopBackOff")), items(podJSON("old", 5, "CrashLoopBackOff"), podJSON("new", 0, "ContainerCreating")), Inconclusive},
	} {
		t.Run(name, func(t *testing.T) {
			// Reads: pods before the upgrade, containers before it, containers after the timeout.
			res := runChartUpgrade(t, timeout, c.before, c.before, c.after)
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

// A cancelled run or the chart's own webhook is not a --wait timeout, so pods are not read for it.
func TestTimedOutWaiting(t *testing.T) {
	for msg, want := range map[string]bool{
		"Error: UPGRADE FAILED: context deadline exceeded":                                                  true,
		"Error: UPGRADE FAILED: timed out waiting for the condition":                                        true,
		"Error: UPGRADE FAILED: failed calling webhook \"x\": context deadline exceeded":                    false,
		"Error: UPGRADE FAILED: cannot patch db with kind StatefulSet: spec: Forbidden: field is immutable": false,
	} {
		if got := timedOutWaiting(t.Context(), errors.New(msg)); got != want {
			t.Errorf("timedOutWaiting(%q) = %v, want %v", msg, got, want)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if timedOutWaiting(ctx, errors.New("context deadline exceeded")) {
		t.Error("a cancelled run counts as a --wait timeout")
	}
}
