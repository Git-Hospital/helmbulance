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

// fakeStages answers chart-upgrade and upgrade-without-values. Each "helm upgrade" moves to the
// next stage; "kubectl get secrets" prints $FAKE_DIR/secrets.<stage> (or the last one), and the
// release renders the Secret "app". Workloads come from $FAKE_DIR/workloads.
const fakeStages = `#!/bin/sh
stage=$(cat "$FAKE_DIR/stage" 2>/dev/null || echo 0)
case "$(basename "$0") $*" in
  "helm upgrade"*) echo $((stage+1)) > "$FAKE_DIR/stage" ;;
  "helm get metadata"*) echo '{"name":"hb","version":"1.0.0"}' ;;
  "helm get manifest"*) printf -- '---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: app\n' ;;
  "kubectl"*"get secrets"*) f="$FAKE_DIR/secrets.$stage"; [ -f "$f" ] || f=$(ls "$FAKE_DIR"/secrets.* | sort | tail -n 1); cat "$f" ;;
  "kubectl"*"get deployments,statefulsets,daemonsets"*) cat "$FAKE_DIR/workloads" ;;
  "kubectl"*"get pods"*|"kubectl"*"get jobs"*) echo '{"items":[]}' ;;
esac
exit 0
`

// secretJSON renders the Secret "app" with the given keys, each set to its value.
func secretJSON(kv ...string) string {
	var data []string
	for i := 0; i+1 < len(kv); i += 2 {
		data = append(data, `"`+kv[i]+`":"`+kv[i+1]+`"`)
	}
	return `{"items":[{"metadata":{"name":"app"},"type":"Opaque","data":{` + strings.Join(data, ",") + `}}]}`
}

// readsKey is a Deployment that reads app/<key> through secretKeyRef.
func readsKey(key string) string {
	return `{"items":[{"kind":"Deployment","metadata":{"name":"web"},"spec":{"template":{"spec":{"containers":[{"name":"c","env":[{"name":"X","valueFrom":{"secretKeyRef":{"name":"app","key":"` + key + `","optional":true}}}]}]}}}}]}`
}

func stagedEnv(t *testing.T, workloads string, stages ...string) *Env {
	t.Helper()
	old := settleInterval
	t.Cleanup(func() { settleInterval = old })
	settleInterval = time.Millisecond
	bin, dir := t.TempDir(), t.TempDir()
	for _, name := range []string{"helm", "kubectl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(fakeStages), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"workloads": workloads}
	for i, s := range stages {
		files["secrets."+string(rune('0'+i))] = s
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
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
	return &Env{
		Runner:      runner,
		Helm:        &helm.Client{Runner: runner, Timeout: time.Minute},
		Kube:        &kube.Client{Runner: runner},
		Hasher:      hasher,
		Chart:       "./v2",
		From:        "./v1",
		FromVersion: "1.0.0",
		Values:      helm.Values{Files: []string{"values.yaml"}},
		Log:         io.Discard,
	}
}

func TestChartUpgradeSecretKeys(t *testing.T) {
	const pw = "cHc=" // "pw"
	cases := []struct {
		name, workloads string
		upgraded        string
		verdict         string
		inSummary       string
	}{
		{"v2 adds a key", readsKey("password"), secretJSON("password", pw, "extra", "eA=="),
			Pass, "keys added by the new version, not a finding: `app/extra` added"},
		{"v2 renames a key nobody reads", `{"items":[]}`, secretJSON("db-password", pw),
			Pass, "values moved to another key, not a finding: `app/password -> app/db-password` moved"},
		{"v2 renames a key the app still reads", readsKey("password"), secretJSON("db-password", pw),
			Fail, "problem(s) upgrading"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := secretJSON("password", pw)
			res := ChartUpgrade{}.Run(t.Context(), stagedEnv(t, c.workloads, before, before, c.upgraded))
			if res.Verdict != c.verdict || !strings.Contains(res.Summary, c.inSummary) {
				t.Errorf("verdict %s, summary %q; want %s with %q", res.Verdict, res.Summary, c.verdict, c.inSummary)
			}
			for _, f := range res.Findings {
				if f.ID == "HB-CU-002" && c.verdict == Pass {
					t.Errorf("HB-CU-002 for %s: %v", c.name, f.Evidence)
				}
			}
		})
	}
}

// The treatment adding a key is not a finding either.
func TestUpgradeValuesAddedKeyIsNotAFinding(t *testing.T) {
	const pw = "cHc="
	before := secretJSON("password", pw)
	res := UpgradeValues{}.Run(t.Context(), stagedEnv(t, `{"items":[]}`, before, before, secretJSON("password", pw, "extra", "eA==")))
	if res.Verdict != Pass || !strings.Contains(res.Summary, "`app/extra` added") {
		t.Errorf("verdict %s, summary %q; want PASS that notes app/extra", res.Verdict, res.Summary)
	}
}

func TestChartUpgradeWithNoSecretsSaysSo(t *testing.T) {
	none := `{"items":[]}`
	res := ChartUpgrade{}.Run(t.Context(), stagedEnv(t, none, none))
	if res.Verdict != Pass || !strings.HasSuffix(res.Summary, "with workloads healthy; "+noSecretsTested) {
		t.Errorf("verdict %s, summary %q", res.Verdict, res.Summary)
	}
}
