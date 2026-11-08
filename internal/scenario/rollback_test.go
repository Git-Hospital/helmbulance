package scenario

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/helmbulance/internal/execx"
	"github.com/Git-Hospital/helmbulance/internal/helm"
	"github.com/Git-Hospital/helmbulance/internal/kube"
	"github.com/Git-Hospital/helmbulance/internal/oracle"
)

// rollbackFake says how the fake helm answers a rollback run. An empty error string makes the call
// succeed.
type rollbackFake struct {
	controlErr  string // the control rollback, to revision 1
	upgradeErr  string // the upgrade to the chart under test
	rollbackErr string // the rollback after it
	hooks       string // helm get hooks output
	// lastStatus and lastChart are the newest revision helm history shows after that rollback;
	// "deployed" and "rollback-fixture-1.0.0" when empty.
	lastStatus, lastChart string
	// pods is what `kubectl get pods` prints in each phase of the run: "control" after the control
	// rollback, "v2" after the upgrade, "back" after the rollback. An empty namespace by default.
	pods map[string]string
	// secrets is what `kubectl get secrets` prints in each phase, "v1" being the install; the
	// release renders the Secret "app". No Secrets by default.
	secrets map[string]string
}

// runRollback runs the rollback scenario against a fake helm and kubectl. The fake helm keeps the
// phase of the run in a file, so `kubectl get pods` prints what the cluster would show at that
// point, however many times the scenario reads it. It returns the result and every helm call.
func runRollback(t *testing.T, f rollbackFake) (Result, []string) {
	t.Helper()
	bin := t.TempDir()
	for kind, byPhase := range map[string]map[string]string{"pods": f.pods, "secrets": f.secrets} {
		for phase, out := range byPhase {
			if err := os.WriteFile(filepath.Join(bin, kind+"."+phase), []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if f.lastStatus == "" {
		f.lastStatus = "deployed"
	}
	if f.lastChart == "" {
		f.lastChart = "rollback-fixture-1.0.0"
	}
	phase := bin + "/phase"
	fail := func(msg string) string {
		if msg == "" {
			return "exit 0"
		}
		return "echo '" + msg + "' >&2; exit 1"
	}
	helmScript := `#!/bin/sh
echo "$*" >> "` + bin + `/calls"
case "$*" in
  *"get metadata"*) echo '{"version":"1.0.0"}'; exit 0 ;;
  "get manifest"*) printf -- '---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: app\n'; exit 0 ;;
  "get hooks"*) cat <<'HOOKS'
` + f.hooks + `
HOOKS
    exit 0 ;;
  install*) echo v1 > "` + phase + `"; exit 0 ;;
  upgrade*--version*) exit 0 ;;
  upgrade*) echo v2 > "` + phase + `"; ` + fail(f.upgradeErr) + ` ;;
  "rollback hb-rollback 0"*) echo 'rolled back to 0, which the scenario must never ask for' >&2; exit 1 ;;
  "rollback hb-rollback 1 "*) echo control > "` + phase + `"; ` + fail(f.controlErr) + ` ;;
  rollback*) echo back > "` + phase + `"; ` + fail(f.rollbackErr) + ` ;;
  history*)
    if [ "$(cat "` + phase + `")" = back ]; then
      echo '[{"revision":1,"status":"superseded","chart":"rollback-fixture-1.0.0"},{"revision":2,"status":"superseded","chart":"rollback-fixture-1.0.0"},{"revision":3,"status":"superseded","chart":"rollback-fixture-1.0.0"},{"revision":4,"status":"superseded","chart":"rollback-fixture-2.0.0"},{"revision":5,"status":"` + f.lastStatus + `","chart":"` + f.lastChart + `"}]'
    else
      echo '[{"revision":1,"status":"superseded","chart":"rollback-fixture-1.0.0"},{"revision":2,"status":"superseded","chart":"rollback-fixture-1.0.0"},{"revision":3,"status":"deployed","chart":"rollback-fixture-1.0.0"}]'
    fi
    exit 0 ;;
esac
exit 0
`
	kubectlScript := `#!/bin/sh
case "$*" in
  *"get pods"*|*"get secrets"*)
    what=pods; case "$*" in *"get secrets"*) what=secrets ;; esac
    f="` + bin + `/$what.$(cat "` + phase + `" 2>/dev/null)"
    if [ -f "$f" ]; then cat "$f"; else echo '{"items":[]}'; fi ;;
  *) echo '{"items":[]}' ;;
esac
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
	res := Rollback{}.Run(t.Context(), env)
	calls, _ := os.ReadFile(filepath.Join(bin, "calls"))
	return res, strings.Split(strings.TrimSpace(string(calls)), "\n")
}

const rollbackTimeout = "Error: resource StatefulSet/hb-rollback/db not ready. status: InProgress\ncontext deadline exceeded"

// With no --from-version there is no earlier version to roll back to: SKIPPED, as chart-upgrade.
// The CLI refuses --scenario rollback without it and leaves rollback out of the default set.
func TestRollbackWithoutFromVersionIsSkipped(t *testing.T) {
	res := Rollback{}.Run(context.Background(), &Env{})
	if res.Verdict != Skipped || len(res.Findings) > 0 {
		t.Errorf("verdict = %s with %d finding(s) (%s), want SKIPPED and none", res.Verdict, len(res.Findings), res.Summary)
	}
}

func TestRollbackThatComesBackIsAPass(t *testing.T) {
	shortSettle(t, 20*time.Millisecond, 5*time.Millisecond)
	pod := items(podJSON("db", 0, ""))
	res, calls := runRollback(t, rollbackFake{pods: map[string]string{"control": pod, "v2": pod, "back": pod}})
	if res.Verdict != Pass || len(res.Findings) > 0 {
		t.Fatalf("verdict = %s with %d finding(s) (%s), want PASS", res.Verdict, len(res.Findings), res.Summary)
	}
	// Each rollback names its revision from helm history, never 0, so the run is the same on both
	// Helm majors: the control goes back to the install, the treatment to the control's revision.
	var rollbacks []string
	for _, c := range calls {
		if strings.HasPrefix(c, "rollback ") {
			rollbacks = append(rollbacks, strings.Join(strings.Fields(c)[:3], " "))
		}
	}
	if strings.Join(rollbacks, "; ") != "rollback hb-rollback 1; rollback hb-rollback 3" {
		t.Errorf("rollbacks = %q, want the control to revision 1 and the treatment to revision 3", rollbacks)
	}
}

// A rollback the chart makes fail (a pre-rollback hook, a field conflict) is HB-RB-001, with
// helm's message and a reproduce line for each step.
func TestRollbackRefusedByTheChartIsHBRB001(t *testing.T) {
	const msg = "Error: pre-rollback hooks failed: 1 error occurred: job hb-rollback-pre-rollback failed: BackoffLimitExceeded"
	res, _ := runRollback(t, rollbackFake{rollbackErr: msg})
	if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-RB-001" {
		t.Fatalf("verdict = %s with %+v (%s), want FAIL with HB-RB-001", res.Verdict, res.Findings, res.Summary)
	}
	f := res.Findings[0]
	if !strings.Contains(strings.Join(f.Evidence, " "), "pre-rollback hooks failed") {
		t.Errorf("evidence = %q, want helm's message", f.Evidence)
	}
	if len(f.Reproduce) != 5 || !strings.Contains(f.Reproduce[1], "helm upgrade") || !strings.Contains(f.Reproduce[2], "helm rollback hb-rollback 1") || !strings.Contains(f.Reproduce[3], "helm upgrade") || !strings.Contains(f.Reproduce[4], "helm rollback hb-rollback 3") {
		t.Errorf("reproduce = %q, want install, control upgrade, control rollback, treatment upgrade and rollback to revision 3", f.Reproduce)
	}
}

// An error that comes from the run, not the chart, is INCONCLUSIVE: etcd under load, or a --wait
// that ran out with nothing crashing.
func TestRollbackRunErrorsAreInconclusive(t *testing.T) {
	shortSettle(t, 20*time.Millisecond, 5*time.Millisecond)
	for name, msg := range map[string]string{
		"etcd":                  "Error: etcdserver: leader changed",
		"timeout with no crash": rollbackTimeout,
	} {
		t.Run(name, func(t *testing.T) {
			pod := items(podJSON("db", 0, ""))
			res, _ := runRollback(t, rollbackFake{rollbackErr: msg, pods: map[string]string{"control": pod, "back": items(podJSON("db2", 0, "ContainerCreating"))}})
			if res.Verdict != Inconclusive || len(res.Findings) > 0 {
				t.Errorf("verdict = %s with %d finding(s) (%s), want INCONCLUSIVE and none", res.Verdict, len(res.Findings), res.Summary)
			}
		})
	}
}

// The earlier version crash-loops on what the new version left behind, so the rollback's --wait
// runs out: the crash makes it HB-RB-002, with the pod as evidence (#28).
func TestRollbackTimeoutWithACrashIsHBRB002(t *testing.T) {
	res, _ := runRollback(t, rollbackFake{rollbackErr: rollbackTimeout, pods: map[string]string{
		"control": items(podJSON("db", 0, "")),
		"back":    items(podJSON("db2", 3, "CrashLoopBackOff")),
	}})
	if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-RB-002" {
		t.Fatalf("verdict = %s with %+v (%s), want FAIL with HB-RB-002", res.Verdict, res.Findings, res.Summary)
	}
	if strings.Contains(res.Findings[0].Impact, "reports success") {
		t.Error("timeout finding claims helm reported success")
	}
	if !strings.Contains(strings.Join(res.Findings[0].Evidence, " "), "pod `web-db2`") {
		t.Errorf("evidence = %q, want the crashing pod", res.Findings[0].Evidence)
	}
}

// A rollback that returns success but leaves the earlier version crashing once the pods settle is
// HB-RB-002 too.
func TestRollbackUnhealthyAfterSettleIsHBRB002(t *testing.T) {
	shortSettle(t, 20*time.Millisecond, 5*time.Millisecond)
	res, _ := runRollback(t, rollbackFake{pods: map[string]string{
		"control": items(podJSON("db", 0, "")),
		"back":    items(podJSON("db2", 4, "CrashLoopBackOff")),
	}})
	if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-RB-002" || res.Findings[0].Severity != "high" {
		t.Errorf("verdict = %s with %+v (%s), want FAIL with HB-RB-002, high", res.Verdict, res.Findings, res.Summary)
	}
}

// helm rollback can return 0 while the release is not back: the newest revision failed, or it is
// not the earlier version. That is HB-RB-001, not a PASS.
func TestRollbackThatHelmDoesNotFinishIsHBRB001(t *testing.T) {
	for name, f := range map[string]rollbackFake{
		"failed revision": {lastStatus: "failed"},
		"still at v2":     {lastChart: "rollback-fixture-2.0.0"},
	} {
		t.Run(name, func(t *testing.T) {
			res, _ := runRollback(t, f)
			if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-RB-001" ||
				!strings.Contains(strings.Join(res.Findings[0].Evidence, " "), "revision 5") {
				t.Errorf("verdict = %s with %+v (%s), want FAIL with HB-RB-001 naming revision 5", res.Verdict, res.Findings, res.Summary)
			}
		})
	}
}

// D4: when the earlier version cannot even roll back to itself, a failing rollback after the
// upgrade proves nothing about the chart under test.
func TestRollbackControlFailsIsInconclusive(t *testing.T) {
	res, calls := runRollback(t, rollbackFake{controlErr: "Error: pre-rollback hooks failed: job failed", rollbackErr: "Error: never reached"})
	if res.Verdict != Inconclusive || len(res.Findings) > 0 {
		t.Errorf("verdict = %s with %d finding(s) (%s), want INCONCLUSIVE and none", res.Verdict, len(res.Findings), res.Summary)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "upgrade hb-rollback ./v2") {
			t.Errorf("upgraded to the chart under test after the control failed: %q", c)
		}
	}
}

// A failed upgrade is chart-upgrade's finding (HB-CU-001); what Helm's automatic rollback does then
// is HB-RB-003, in the second pull request. Until then there is nothing to roll back.
func TestRollbackAfterAFailedUpgradeIsInconclusive(t *testing.T) {
	res, calls := runRollback(t, rollbackFake{upgradeErr: "Error: UPGRADE FAILED: cannot patch db with kind StatefulSet: spec: Forbidden: field is immutable"})
	if res.Verdict != Inconclusive || len(res.Findings) > 0 || !strings.Contains(res.Summary, "chart-upgrade") {
		t.Errorf("verdict = %s with %d finding(s) (%s), want INCONCLUSIVE pointing at chart-upgrade", res.Verdict, len(res.Findings), res.Summary)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "rollback hb-rollback 3") {
			t.Errorf("rolled back after the upgrade failed: %q", c)
		}
	}
}

// A Secret value that is not what it was before the upgrade, once the earlier version is back, is
// HB-RB-002: the earlier version's data was set up with the old value.
func TestRollbackChangedSecretIsHBRB002(t *testing.T) {
	shortSettle(t, 20*time.Millisecond, 5*time.Millisecond)
	before, changed := secretJSON("password", "YQ=="), secretJSON("password", "Yg==")
	res, _ := runRollback(t, rollbackFake{secrets: map[string]string{"v1": before, "control": before, "v2": changed, "back": changed}})
	if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-RB-002" ||
		!strings.Contains(strings.Join(res.Findings[0].Evidence, " "), "app/password") {
		t.Errorf("verdict = %s with %+v (%s), want FAIL with HB-RB-002 naming app/password", res.Verdict, res.Findings, res.Summary)
	}
}

// A key that already changed in the control changes on every rollback of the earlier version,
// whatever the chart under test does: it is named in the summary and not blamed (D4).
func TestRollbackKeyTheControlChangedIsNotBlamed(t *testing.T) {
	shortSettle(t, 20*time.Millisecond, 5*time.Millisecond)
	res, _ := runRollback(t, rollbackFake{secrets: map[string]string{
		"v1": secretJSON("nonce", "YQ=="), "control": secretJSON("nonce", "Yg=="), "back": secretJSON("nonce", "Yw=="),
	}})
	if res.Verdict != Pass || len(res.Findings) > 0 || !strings.Contains(res.Summary, "change on every rollback") {
		t.Errorf("verdict = %s with %+v (%s), want PASS naming the key the control changed", res.Verdict, res.Findings, res.Summary)
	}
}

func TestRollbackHookCrashIsHBRB001(t *testing.T) {
	hook := `{"items":[{"metadata":{"name":"hook-pod","uid":"hook","ownerReferences":[{"kind":"Job","name":"undo"}]},"status":{"phase":"Running","containerStatuses":[{"name":"hook","restartCount":3,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}]}`
	for _, msg := range []string{
		"Error: resource Job/hb-rollback/undo not ready. status: Failed",
		"Error: resource Job/hb-rollback/undo not ready. status: InProgress\ncontext deadline exceeded",
	} {
		t.Run(msg, func(t *testing.T) {
			res, _ := runRollback(t, rollbackFake{
				rollbackErr: msg,
				hooks:       "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: undo\n  annotations:\n    helm.sh/hook: pre-rollback\n",
				pods:        map[string]string{"back": hook},
			})
			if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-RB-001" {
				t.Fatalf("hook failure: %+v; want HB-RB-001", res)
			}
		})
	}
}
