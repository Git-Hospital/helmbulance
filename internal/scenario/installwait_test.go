package scenario

import (
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
