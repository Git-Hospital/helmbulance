package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeReinstall answers the reinstall scenario. Every "helm install" and "helm uninstall" moves to
// the next stage: 1 is startClean's uninstall, 2 the first install, 3 the uninstall, 4 the
// reinstall. A helm call fails with $FAKE_DIR/fail.<stage> as its message when that file exists.
// "kubectl get secrets" prints $FAKE_DIR/secrets.<stage>, or fails when $FAKE_DIR/fail.secrets.<stage>
// exists. The release renders what $FAKE_DIR/manifest and $FAKE_DIR/hooks hold. The claims are
// $FAKE_DIR/pvc (none without it), and every volume keeps the sentinel written to it.
const fakeReinstall = `#!/bin/sh
stage=$(cat "$FAKE_DIR/stage" 2>/dev/null || echo 0)
case "$(basename "$0") $*" in
  "helm install"*|"helm uninstall"*)
    stage=$((stage+1)); echo $stage > "$FAKE_DIR/stage"
    if [ -f "$FAKE_DIR/fail.$stage" ]; then cat "$FAKE_DIR/fail.$stage" >&2; exit 1; fi ;;
  "helm get manifest"*) cat "$FAKE_DIR/manifest" ;;
  "helm get hooks"*) cat "$FAKE_DIR/hooks" ;;
  "kubectl"*"get secrets"*)
    if [ -f "$FAKE_DIR/fail.secrets.$stage" ]; then echo "connection refused" >&2; exit 1; fi
    cat "$FAKE_DIR/secrets.$stage" ;;
  "kubectl"*"get deployments,statefulsets,daemonsets"*) cat "$FAKE_DIR/workloads" ;;
  "kubectl"*"get pvc"*) cat "$FAKE_DIR/pvc" 2>/dev/null || echo '{"items":[]}' ;;
  "kubectl"*" run hb-sentinel-w"*) echo "$*" | sed 's/.*printf %s \([^ ]*\) .*/\1/' > "$FAKE_DIR/sentinel" ;;
  "kubectl"*"get pod hb-sentinel"*) printf Succeeded ;;
  "kubectl"*"logs hb-sentinel-w"*) echo written ;;
  "kubectl"*"logs hb-sentinel-r"*) cat "$FAKE_DIR/sentinel" ;;
  "kubectl"*"get pods"*|"kubectl"*"get jobs"*) echo '{"items":[]}' ;;
esac
exit 0
`

// hookSecret is the hooks output of a release whose pre-install hook renders the Secret "app".
const hookSecret = "---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: app\n  annotations:\n    helm.sh/hook: pre-install\n"

// appSecret renders the Secret "app" with one password, kept on uninstall when keep is set.
func appSecret(keep bool, password string) string {
	annotations := ""
	if keep {
		annotations = `,"annotations":{"helm.sh/resource-policy":"keep","helm.sh/hook":"pre-install"}`
	}
	return `{"items":[{"metadata":{"name":"app"` + annotations + `},"type":"Opaque","data":{"password":"` + password + `"}}]}`
}

// reinstallEnv is stagedEnv with fakeReinstall in place of fakeStages, and files written to FAKE_DIR.
func reinstallEnv(t *testing.T, files map[string]string) *Env {
	t.Helper()
	env := stagedEnv(t, files["workloads"])
	bin := filepath.SplitList(os.Getenv("PATH"))[0]
	for _, name := range []string{"helm", "kubectl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(fakeReinstall), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir := os.Getenv("FAKE_DIR")
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

// The pre-install hook Secret is read by a stateless Deployment and replaced by the reinstall. It
// outlives the uninstall either way, as every hook does; only with keep is it HB-RI-004 (#43 review
// items 1 and 6).
func TestReinstallKeptHook(t *testing.T) {
	const old, fresh = "b2xk", "bmV3" // "old", "new"
	for _, keep := range []bool{false, true} {
		files := map[string]string{
			"hooks": hookSecret, "manifest": "", "workloads": readsKey("password"),
			"secrets.2": appSecret(keep, old), "secrets.3": appSecret(keep, old), "secrets.4": appSecret(keep, fresh),
		}
		res := Reinstall{}.Run(t.Context(), reinstallEnv(t, files))
		if !keep {
			if res.Verdict != Pass || len(res.Findings) != 0 {
				t.Errorf("hook without keep: %s %q %+v; want PASS, no finding", res.Verdict, res.Summary, res.Findings)
			}
			continue
		}
		if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-RI-004" {
			t.Fatalf("hook with keep: %s %q %+v; want FAIL HB-RI-004", res.Verdict, res.Summary, res.Findings)
		}
		if repro := strings.Join(res.Findings[0].Reproduce, "\n"); strings.Contains(repro, "CLAIM") {
			t.Errorf("a release with no volumes gets sentinel steps:\n%s", repro)
		}
	}
}

// A failed Secret listing after the uninstall leaves HB-RI-004 unchecked: INCONCLUSIVE, not the PASS
// "the release has no volumes" (#43 review item 3).
func TestReinstallUnlistedSurvivors(t *testing.T) {
	files := map[string]string{
		"hooks": hookSecret, "manifest": "", "workloads": readsKey("password"), "fail.secrets.3": "",
		"secrets.2": appSecret(true, "b2xk"), "secrets.4": appSecret(true, "bmV3"),
	}
	res := Reinstall{}.Run(t.Context(), reinstallEnv(t, files))
	if res.Verdict != Inconclusive || !strings.HasPrefix(res.Summary, "could not tell which Secrets outlived the uninstall") {
		t.Errorf("%s %q; want INCONCLUSIVE, could not tell which Secrets outlived the uninstall", res.Verdict, res.Summary)
	}
	// Unchanged Secrets need no list: nothing was replaced.
	files["secrets.4"] = files["secrets.2"]
	if res := (Reinstall{}).Run(t.Context(), reinstallEnv(t, files)); res.Verdict != Pass {
		t.Errorf("unchanged Secrets: %s %q; want PASS", res.Verdict, res.Summary)
	}
}

// A first install that never became ready: a kept Secret the reinstall replaced is still seen
// (HB-RI-004, with the timeout as context), and without it nothing is proven (#43 review items 2
// and 7).
func TestReinstallUnreadyFirstInstall(t *testing.T) {
	const timeout = "Error: INSTALLATION FAILED: resource StatefulSet/hb-reinstall/db not ready, status: InProgress: context deadline exceeded"
	base := map[string]string{
		"hooks": hookSecret, "manifest": "", "workloads": readsKey("password"), "fail.2": timeout,
		"secrets.2": appSecret(true, "b2xk"), "secrets.3": appSecret(true, "b2xk"),
	}
	with := func(extra map[string]string) map[string]string {
		files := map[string]string{}
		for k, v := range base {
			files[k] = v
		}
		for k, v := range extra {
			files[k] = v
		}
		return files
	}

	res := Reinstall{}.Run(t.Context(), reinstallEnv(t, with(map[string]string{"secrets.4": appSecret(true, "bmV3")})))
	if res.Verdict != Fail || len(res.Findings) != 1 || res.Findings[0].ID != "HB-RI-004" ||
		!strings.Contains(res.Findings[0].Evidence[0], "first `helm install --wait`") {
		t.Errorf("kept Secret replaced: %s %q %+v; want FAIL HB-RI-004 with the first install's timeout", res.Verdict, res.Summary, res.Findings)
	}

	res = Reinstall{}.Run(t.Context(), reinstallEnv(t, with(map[string]string{"secrets.4": appSecret(true, "b2xk")})))
	if res.Verdict != Inconclusive || !strings.Contains(res.Summary, "never became ready") {
		t.Errorf("kept Secret reused: %s %q; want INCONCLUSIVE", res.Verdict, res.Summary)
	}

	refusal := "Error: INSTALLATION FAILED: execution error at (x/templates/secret.yaml:3:4): set existingSecret"
	res = Reinstall{}.Run(t.Context(), reinstallEnv(t, with(map[string]string{"fail.4": refusal})))
	if res.Verdict != Inconclusive || !strings.Contains(res.Summary, "refuses safely on reinstall") {
		t.Errorf("render refusal after an unready install: %s %q; want INCONCLUSIVE that says what was seen", res.Verdict, res.Summary)
	}
}

// A StatefulSet whose volume keeps its data reads app/password, which the reinstall replaces. After
// a ready first install that is HB-RI-001. After one that never became ready nothing was written
// with the old password, so HB-RI-001 is not reported; a kept Secret is still HB-RI-004 (#43 review
// item 2).
func TestReinstallUnreadyFirstInstallWithKeptData(t *testing.T) {
	const timeout = "Error: INSTALLATION FAILED: resource StatefulSet/hb-reinstall/db not ready, status: InProgress: context deadline exceeded"
	db := `{"items":[{"kind":"StatefulSet","metadata":{"name":"db"},"spec":{"volumeClaimTemplates":[{"metadata":{"name":"data"}}],` +
		`"template":{"spec":{"containers":[{"name":"c","env":[{"name":"P","valueFrom":{"secretKeyRef":{"name":"app","key":"password"}}}]}]}}}}]}`
	run := func(keep, ready bool) Result {
		files := map[string]string{
			"hooks": "", "manifest": "---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: app\n", "workloads": db,
			"pvc":       `{"items":[{"metadata":{"name":"data-db-0","uid":"u1"},"status":{"phase":"Bound"}}]}`,
			"secrets.2": appSecret(keep, "b2xk"), "secrets.3": appSecret(keep, "b2xk"), "secrets.4": appSecret(keep, "bmV3"),
		}
		if !ready {
			files["fail.2"] = timeout
		}
		return Reinstall{}.Run(t.Context(), reinstallEnv(t, files))
	}
	ids := func(r Result) (s []string) {
		for _, f := range r.Findings {
			s = append(s, f.ID)
		}
		return s
	}

	if res := run(false, true); res.Verdict != Fail || strings.Join(ids(res), ",") != "HB-RI-001" {
		t.Errorf("ready: %s %q %v; want FAIL HB-RI-001", res.Verdict, res.Summary, ids(res))
	}
	res := run(false, false)
	if res.Verdict != Inconclusive || !strings.Contains(res.Summary, "(not HB-RI-001): `app/password` changed") {
		t.Errorf("never ready: %s %q %v; want INCONCLUSIVE that names app/password as not HB-RI-001", res.Verdict, res.Summary, ids(res))
	}
	if res := run(true, false); res.Verdict != Fail || strings.Join(ids(res), ",") != "HB-RI-004" {
		t.Errorf("never ready, kept Secret: %s %q %v; want FAIL HB-RI-004", res.Verdict, res.Summary, ids(res))
	}
}
