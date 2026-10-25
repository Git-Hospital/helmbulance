package scenario

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
	"github.com/egezamb/helmbulance/internal/helm"
	"github.com/egezamb/helmbulance/internal/kube"
	"github.com/egezamb/helmbulance/internal/oracle"
)

func TestGitopsDrift(t *testing.T) {
	v := func(b byte) [32]byte { return [32]byte{b} }
	live := oracle.SecretSnapshot{"db/password": v(1), "db/user": v(2), "api/token": v(3), "regen/key": v(4), "gone/key": v(5)}
	server := oracle.SecretSnapshot{"db/password": v(1), "db/user": v(2), "regen/key": v(40), "gone/key": v(5)} // api/token: rendered only if not (lookup …)
	client1 := oracle.SecretSnapshot{"db/password": v(10), "db/user": v(2), "api/token": v(30), "regen/key": v(41)}
	client2 := oracle.SecretSnapshot{"db/password": v(11), "db/user": v(2), "api/token": v(30), "regen/key": v(42)}

	changes, everySync := gitopsDrift(live, server, client1, client2)
	want := []oracle.Change{{Ref: "api/token", Kind: oracle.Changed}, {Ref: "db/password", Kind: oracle.Changed}}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("changes = %v, want %v (not db/user: unchanged; not regen/key: Helm changes it too; not gone/key: no client value)", changes, want)
	}
	if !reflect.DeepEqual(everySync, map[string]bool{"db/password": true}) {
		t.Errorf("everySync = %v, want only db/password (api/token renders the same value both times)", everySync)
	}
}

func TestLookupCalls(t *testing.T) {
	chart := t.TempDir()
	files := map[string]string{
		"templates/secret.yaml":             "{{- $old := lookup \"v1\" \"Secret\" .Release.Namespace \"db\" }}\nkind: Secret\n",
		"charts/sub/templates/_helpers.tpl": "{{/* no call here */}}\n{{ $x := (lookup \"v1\" \"Secret\" \"ns\" \"x\") }}\n",
		"values.yaml":                       "# lookup is mentioned here, but values are not templates\n",
		"templates/lookupTable.yaml":        "kind: ConfigMap # lookupTable is not a call\n",
	}
	for name, text := range files {
		p := filepath.Join(chart, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := lookupCalls(chart)
	want := []string{"`lookup` in `charts/sub/templates/_helpers.tpl:2`", "`lookup` in `templates/secret.yaml:1`"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lookupCalls = %q, want %q", got, want)
	}
}

// fakeGitopsTools: helm installs, owns the Secret "db" and renders it with password $FAKE_SERVER
// server-side and $FAKE_CLIENT client-side ("random" prints a new value on every call; an empty
// $FAKE_SERVER renders no Secret, as a chart that renders it only if not (lookup …)). kubectl
// holds db with password "pw" and has nothing else.
const fakeGitopsTools = `#!/bin/sh
secret() { printf -- '---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: db\nstringData:\n  password: %s\n' "$1"; }
case "$(basename "$0") $*" in
  "helm get manifest"*) secret pw ;;
  "helm template"*"--dry-run=server"*) [ -n "$FAKE_SERVER" ] && secret "$FAKE_SERVER" ;;
  "helm template"*) v="$FAKE_CLIENT"; [ "$v" = random ] && v="r$$"; secret "$v" ;;
  "kubectl"*"get secrets"*) echo '{"items":[{"metadata":{"name":"db"},"type":"Opaque","data":{"password":"cHc="}}]}' ;;
  "kubectl"*"get"*) echo '{"items":[]}' ;;
esac
exit 0
`

func runGitopsRender(t *testing.T, server, client, chart string) Result {
	t.Helper()
	old := settleInterval
	t.Cleanup(func() { settleInterval = old })
	settleInterval = time.Millisecond
	bin := t.TempDir()
	for _, name := range []string{"helm", "kubectl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(fakeGitopsTools), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_SERVER", server)
	t.Setenv("FAKE_CLIENT", client)
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
		Chart:  chart,
		Log:    io.Discard,
	}
	return GitopsRender{}.Run(t.Context(), env)
}

func TestGitopsRenderRun(t *testing.T) {
	chart := t.TempDir()
	if err := os.MkdirAll(filepath.Join(chart, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chart, "templates", "secret.yaml"), []byte("{{ lookup \"v1\" \"Secret\" \"ns\" \"db\" }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		server, client, verdict string
		evidence                []string
	}{
		"value from values: the same everywhere":   {"pw", "pw", Pass, nil},
		"lookup with a random fallback":            {"pw", "random", Fail, []string{"`db/password` changed", "on every sync: `db/password`", "`lookup` in `templates/secret.yaml:1`"}},
		"rendered only if not (lookup …)":          {"", "random", Fail, []string{"`db/password` changed"}},
		"regenerated under Helm too: not reported": {"other", "random", Pass, nil},
	} {
		t.Run(name, func(t *testing.T) {
			res := runGitopsRender(t, c.server, c.client, chart)
			if res.Verdict != c.verdict {
				t.Fatalf("verdict = %s (%s), want %s", res.Verdict, res.Summary, c.verdict)
			}
			if c.verdict != Fail {
				return
			}
			if len(res.Findings) != 1 || res.Findings[0].ID != "HB-GR-001" {
				t.Fatalf("findings = %+v, want one HB-GR-001", res.Findings)
			}
			text := strings.Join(res.Findings[0].Evidence, "\n")
			for _, want := range c.evidence {
				if !strings.Contains(text, want) {
					t.Errorf("evidence lacks %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "pw") || strings.Contains(strings.Join(res.Findings[0].Reproduce, " "), "--dry-run=server") {
				t.Errorf("evidence or reproduce shows a value or the server-side render:\n%s\n%v", text, res.Findings[0].Reproduce)
			}
		})
	}
}
