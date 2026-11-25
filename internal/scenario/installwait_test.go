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

// fakeUnreadyTools is helm and kubectl for a chart that never becomes ready: every helm install
// runs out its --wait, and each pod is $FAKE_PODS.
const fakeUnreadyTools = `#!/bin/sh
echo "$(basename "$0") $*" >> "$FAKE_DIR/log"
case "$(basename "$0") $*" in
  "helm install"*) echo "Error: INSTALLATION FAILED: timed out waiting for the condition" >&2; exit 1 ;;
  "kubectl"*" get pods "*) echo "$FAKE_PODS" ;;
  "kubectl"*" get "*) echo '{"items":[]}' ;;
esac
exit 0
`

func unreadyPods(waiting string) string {
	state := `{"running":{}}`
	if waiting != "" {
		state = `{"waiting":{"reason":"` + waiting + `"}}`
	}
	return `{"items":[{"metadata":{"name":"web-1","uid":"u1","ownerReferences":[{"kind":"ReplicaSet","name":"web-1"}]},` +
		`"status":{"phase":"Running","containerStatuses":[{"name":"web","restartCount":0,"state":` + state + `}]}}]}`
}

// runAllUnready runs every scenario in the CLI's order, with --from-version, against a chart that
// never becomes ready, and returns the results and the helm install calls.
func runAllUnready(t *testing.T, pods string) ([]Result, []string) {
	t.Helper()
	bin, dir := t.TempDir(), t.TempDir()
	for _, name := range []string{"helm", "kubectl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(fakeUnreadyTools), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DIR", dir)
	t.Setenv("FAKE_PODS", pods)
	hasher, err := oracle.NewHasher()
	if err != nil {
		t.Fatal(err)
	}
	runner := &execx.Runner{}
	env := &Env{
		Runner: runner,
		Helm:   &helm.Client{Runner: runner, Timeout: time.Minute},
		Kube:   &kube.Client{Runner: runner},
		Hasher: hasher,
		Chart:  "./chart",
		Values: helm.Values{Sets: []string{"a=b"}},
		From:   "repo/chart", FromVersion: "1.0.0",
		Log: io.Discard,
	}
	var results []Result
	for _, s := range []Scenario{UpgradeValues{}, ChartUpgrade{}, Rollback{}, ZeroDowntime{}, Reinstall{}, GitopsRender{}} {
		results = append(results, s.Run(t.Context(), env))
	}
	log, err := os.ReadFile(filepath.Join(dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	var installs []string
	for _, call := range strings.Split(string(log), "\n") {
		if strings.HasPrefix(call, "helm install ") {
			installs = append(installs, call)
		}
	}
	return results, installs
}

// #165: a chart that never becomes ready waited out --timeout in every scenario. An install whose
// --wait already ran out in this run is not waited for again; reinstall still installs twice,
// because it compares what each install left.
func TestScenariosDoNotWaitAgainForAnInstallThatTimedOut(t *testing.T) {
	results, installs := runAllUnready(t, unreadyPods(""))
	// upgrade-without-values and chart-upgrade wait once each, reinstall twice; rollback and
	// zero-downtime repeat chart-upgrade's install, gitops-render repeats upgrade-without-values'.
	if len(installs) != 4 {
		t.Errorf("%d helm installs, want 4:\n%s", len(installs), strings.Join(installs, "\n"))
	}
	for _, c := range []struct{ scenario, waited string }{
		{"rollback", "chart-upgrade"},
		{"zero-downtime", "chart-upgrade"},
		{"gitops-render", "upgrade-without-values"},
	} {
		for _, r := range results {
			if r.Scenario != c.scenario {
				continue
			}
			if r.Verdict != Inconclusive || !strings.Contains(r.Summary, "already ran out in "+c.waited) {
				t.Errorf("%s: %s (%s), want INCONCLUSIVE naming %s", r.Scenario, r.Verdict, r.Summary, c.waited)
			}
		}
	}
}

// A first wait can run out while an image is still being pulled, and the next install of the same
// chart can then succeed from the node's cache: those installs are waited for again.
func TestScenariosWaitAgainWhileAnImageWasPulling(t *testing.T) {
	for _, reason := range []string{"ContainerCreating", "ErrImagePull", "ImagePullBackOff"} {
		t.Run(reason, func(t *testing.T) {
			if _, installs := runAllUnready(t, unreadyPods(reason)); len(installs) != 7 {
				t.Errorf("%d helm installs, want all 7:\n%s", len(installs), strings.Join(installs, "\n"))
			}
		})
	}
}

// unreadyEnv is an Env whose helm and kubectl are fakeUnreadyTools, with no pod waiting on an image,
// and a func that returns the helm install calls so far.
func unreadyEnv(t *testing.T, helmScript string) (*Env, func() []string) {
	t.Helper()
	bin, dir := t.TempDir(), t.TempDir()
	for name, script := range map[string]string{"helm": helmScript, "kubectl": fakeUnreadyTools} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DIR", dir)
	t.Setenv("FAKE_PODS", unreadyPods(""))
	runner := &execx.Runner{}
	env := &Env{Runner: runner, Helm: &helm.Client{Runner: runner, Timeout: time.Minute}, Kube: &kube.Client{Runner: runner}, Log: io.Discard}
	return env, func() []string {
		log, _ := os.ReadFile(filepath.Join(dir, "log"))
		var installs []string
		for _, call := range strings.Split(string(log), "\n") {
			if strings.HasPrefix(call, "helm install ") {
				installs = append(installs, call)
			}
		}
		return installs
	}
}

// installKey is all that tells two installs apart, so an install of another chart version or with
// other values is waited for, even after one of the same chart timed out (review of #174).
func TestInstallWaitsForAnotherVersionOrOtherValues(t *testing.T) {
	env, installs := unreadyEnv(t, fakeUnreadyTools)
	env.Values = helm.Values{Sets: []string{"a=b"}}
	ctx := t.Context()
	_ = install(ctx, env, "first", "r1", "ns1", "./chart")
	_ = install(ctx, env, "version", "r2", "ns2", "./chart", "--version", "2.0.0")
	_ = install(ctx, env, "repo", "r3", "ns3", "repo/chart")
	env.Values = helm.Values{Sets: []string{"a=c"}}
	_ = install(ctx, env, "set", "r4", "ns4", "./chart")
	env.Values = helm.Values{Sets: []string{"a=b"}, Files: []string{"prod.yaml"}}
	_ = install(ctx, env, "file", "r5", "ns5", "./chart")
	if got := installs(); len(got) != 5 {
		t.Errorf("%d helm installs, want 5, one for each chart, version and values:\n%s", len(got), strings.Join(got, "\n"))
	}
	// The same chart and values as the first install: not waited for again.
	env.Values = helm.Values{Sets: []string{"a=b"}}
	if err := install(ctx, env, "again", "r6", "ns6", "./chart"); err == nil || !strings.Contains(err.Error(), "already ran out in first") {
		t.Errorf("install of the first chart again = %v, want it skipped naming first", err)
	}
	if got := installs(); len(got) != 5 {
		t.Errorf("%d helm installs after the repeat, want still 5", len(got))
	}
}

// An install cut short by the run's own deadline (--run-timeout, Ctrl-C) says nothing about the
// chart, so the next install of it in another run step is not skipped (review of #174).
func TestInstallCutByTheRunIsNotRemembered(t *testing.T) {
	// helm prints the waiter's words and then hangs, so only the context ends it.
	slow := strings.Replace(fakeUnreadyTools, `exit 1 ;;`, `exec sleep 2 ;;`, 1)
	env, installs := unreadyEnv(t, slow)
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if err := install(ctx, env, "cut", "r1", "ns1", "./chart"); err == nil {
		t.Fatal("install under an expired run deadline succeeded")
	}
	if len(env.timedOut) != 0 {
		t.Errorf("remembered %v after an install the run cut short, want nothing", env.timedOut)
	}
	env.Helm = &helm.Client{Runner: env.Runner, Timeout: time.Minute}
	_ = install(context.WithoutCancel(t.Context()), env, "next", "r2", "ns2", "./chart")
	// The cut install may be killed before its helm even starts, so only the next one is counted.
	if got := installs(); len(got) == 0 || !strings.HasPrefix(got[len(got)-1], "helm install r2 ") {
		t.Errorf("helm installs %q, want the next one (r2) run, not skipped", got)
	}
}
