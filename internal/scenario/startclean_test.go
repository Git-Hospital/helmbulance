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
	"github.com/egezamb/helmbulance/internal/oracle"
)

// fakeLoggingTools logs every call to $FAKE_DIR/log; helm install fails, so a scenario stops right
// after its first install and the log shows what ran before it.
const fakeLoggingTools = `#!/bin/sh
echo "$(basename "$0") $*" >> "$FAKE_DIR/log"
case "$(basename "$0") $*" in
  "helm install"*) echo "Error: INSTALLATION FAILED: test" >&2; exit 1 ;;
  "kubectl"*" get "*) echo '{"items":[]}' ;;
esac
exit 0
`

// #11: a run killed before its cleanup leaves the release and namespace behind under fixed names,
// and the next run on a reused --cluster then fails. Each scenario clears both before it installs.
func TestScenariosStartFromACleanNamespace(t *testing.T) {
	for _, c := range []struct {
		s      Scenario
		ns     string
		modify func(*Env)
	}{
		{UpgradeValues{}, upgradeNamespace, func(e *Env) { e.Values = helm.Values{Sets: []string{"a=b"}} }},
		{ChartUpgrade{}, chartUpgradeNamespace, func(e *Env) { e.From, e.FromVersion = "repo/chart", "1.0.0" }},
		{ZeroDowntime{}, zeroDowntimeNamespace, func(*Env) {}},
		{Reinstall{}, reinstallNamespace, func(*Env) {}},
	} {
		t.Run(c.s.Name(), func(t *testing.T) {
			bin, dir := t.TempDir(), t.TempDir()
			for _, name := range []string{"helm", "kubectl"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(fakeLoggingTools), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("FAKE_DIR", dir)
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
				Log:    io.Discard,
			}
			c.modify(env)
			c.s.Run(t.Context(), env)

			log, err := os.ReadFile(filepath.Join(dir, "log"))
			if err != nil {
				t.Fatal(err)
			}
			calls := strings.Split(strings.TrimSpace(string(log)), "\n")
			install := -1
			for i, call := range calls {
				if strings.HasPrefix(call, "helm install ") {
					install = i
					break
				}
			}
			if install < 0 {
				t.Fatalf("no helm install in\n%s", log)
			}
			before := strings.Join(calls[:install], "\n")
			if !strings.Contains(before, "helm uninstall ") || !strings.Contains(before, "delete namespace "+c.ns+" ") {
				t.Errorf("before the install, want an uninstall and a delete of namespace %s; got\n%s", c.ns, before)
			}
			// The clean-up before the install is not part of the reproduction steps.
			if res := c.s.Run(t.Context(), env); strings.Contains(res.Summary+strings.Join(reproOf(res), " "), "uninstall") {
				t.Errorf("the start-clean calls leak into the result: %+v", res)
			}
		})
	}
}

func reproOf(res Result) []string {
	var lines []string
	for _, f := range res.Findings {
		lines = append(lines, f.Reproduce...)
	}
	return lines
}
