package scenario

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/helmbulance/internal/execx"
	"github.com/Git-Hospital/helmbulance/internal/helm"
	"github.com/Git-Hospital/helmbulance/internal/oracle"
)

// fakeRotationHelm: `helm show values` prints $FAKE_VALUES, and `helm template` renders a chart
// whose Secrets follow the --set-string it is given. db/password is auth.password; setting
// auth.existingSecret drops db; api/token is api.token; db2/password is extra.dbPassword, a value
// only the -f file has; gen/password is gen.password, or new on every render when it is empty;
// web-tls/tls.key is tls.privateKey, a
// certificate's key; session/key is new on every render and session.token changes it too;
// broken.apiKey makes the render fail, and so does setting two values when $FAKE_TOGETHER_FAILS
// is set. Each render's --set-string values go on one line of
// $FAKE_SETS.
const fakeRotationHelm = `#!/bin/sh
if [ "$1 $2" = "show values" ]; then cat "$FAKE_VALUES"; exit 0; fi
[ "$1" = template ] || exit 0
pw=default-pw token=abc existing= extra=x1 gen= tlskey=k line= prev=
for a in "$@"; do
  if [ "$prev" = --set-string ]; then
    line="$line${line:+ }$a"
    v=${a#*=}
    case "${a%%=*}" in
      auth.password) pw=$v ;;
      auth.existingSecret) existing=$v ;;
      api.token) token=$v ;;
      extra.dbPassword) extra=$v ;;
      gen.password) gen=$v ;;
      tls.privateKey) tlskey=$v ;;
      broken.apiKey) echo "$line" >> "$FAKE_SETS"; echo "Error: execution error: apiKey is not allowed" >&2; exit 1 ;;
    esac
  fi
  prev=$a
done
[ -n "$line" ] && echo "$line" >> "$FAKE_SETS"
case "$line" in *" "*) [ -n "$FAKE_TOGETHER_FAILS" ] && { echo "Error: two values conflict" >&2; exit 1; } ;; esac
[ -z "$existing" ] && printf -- '---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: db\nstringData:\n  password: %s\n  user: app\n' "$pw"
printf -- '---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: api\nstringData:\n  token: %s\n' "$token"
printf -- '---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: db2\nstringData:\n  password: %s\n' "$extra"
printf -- '---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: gen\nstringData:\n  password: %s\n' "${gen:-r$$}"
printf -- '---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: web-tls\nstringData:\n  tls.key: %s\n' "$tlskey"
printf -- '---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: session\nstringData:\n  key: r%s\n' "$$"
printf -- '---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: other-ns\n  namespace: elsewhere\nstringData:\n  password: %s\n' "$pw"
exit 0
`

const rotationDefaults = `auth:
  username: app
  password: ""
  existingSecret: ""
api:
  token: abc
broken:
  apiKey: x
gen:
  password: ""
tls:
  privateKey: k
session:
  token: s
webhook:
  secretKey: unused
monkey: banana
tags: [password]
"dotted.password": x
`

func rotationEnv(t *testing.T) (*Env, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "helm"), []byte(fakeRotationHelm), 0o755); err != nil {
		t.Fatal(err)
	}
	values := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(values, []byte(rotationDefaults), 0o644); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(dir, "user.yaml")
	if err := os.WriteFile(user, []byte("extra:\n  dbPassword: mine\nauth:\n  password: also-mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sets := filepath.Join(dir, "sets")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_VALUES", values)
	t.Setenv("FAKE_SETS", sets)
	hasher, err := oracle.NewHasher()
	if err != nil {
		t.Fatal(err)
	}
	runner := &execx.Runner{}
	return &Env{
		Runner: runner,
		Helm:   &helm.Client{Runner: runner, Timeout: time.Minute},
		Hasher: hasher,
		Chart:  "./chart",
		Values: helm.Values{Files: []string{user}, Sets: []string{"api.token=fixed"}},
		Log:    io.Discard,
	}, sets
}

// A candidate is a credential-named value that changes a key of a Secret the release renders in
// its namespace, and nothing else is (#187).
func TestRotationCandidates(t *testing.T) {
	env, sets := rotationEnv(t)
	cands, skipped, err := rotationCandidates(t.Context(), env, "hb-rotation", "hb-rotation")
	if err != nil {
		t.Fatal(err)
	}
	want := []rotationCandidate{
		{Key: "auth.password", Refs: []string{"db/password"}},
		{Key: "extra.dbPassword", Refs: []string{"db2/password"}}, // from the -f file only
		{Key: "gen.password", Refs: []string{"gen/password"}},     // random only while it is empty
	}
	// Not: tls.privateKey (TLS material), auth.existingSecret (drops db, changes no key), session.token (session/key changes on
	// every render), webhook.secretKey (changes nothing), api.token (the user's --set), and the
	// names that are no credential, the list and the dotted key.
	if !reflect.DeepEqual(cands, want) {
		t.Errorf("candidates = %+v, want %+v", cands, want)
	}
	if !reflect.DeepEqual(skipped, []string{"broken.apiKey"}) {
		t.Errorf("skipped = %v, want the value whose render failed", skipped)
	}
	b, _ := os.ReadFile(sets)
	var renders []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var keys []string
		for _, a := range strings.Fields(line) {
			k, v, _ := strings.Cut(a, "=")
			keys = append(keys, k)
			if len(v) != 26 || !strings.HasPrefix(v, "hb") || strings.Trim(v, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" {
				t.Errorf("%s set to %q, want hb and 24 letters or digits", k, v)
			}
		}
		renders = append(renders, strings.Join(keys, " "))
	}
	// One render per value in key order, then one with every value that rendered and removed no
	// key (not auth.existingSecret), since some changed a random key.
	wantRenders := []string{"auth.existingSecret", "auth.password", "broken.apiKey", "extra.dbPassword", "gen.password",
		"session.token", "tls.privateKey", "webhook.secretKey",
		"auth.password extra.dbPassword gen.password session.token tls.privateKey webhook.secretKey"}
	if !reflect.DeepEqual(renders, wantRenders) {
		t.Errorf("renders set\n%q\nwant\n%q", renders, wantRenders)
	}
}

// When the render with every value fails, a value that changed a random key is rendered alone
// once more instead.
func TestRotationCandidatesWhenTheValuesConflict(t *testing.T) {
	env, sets := rotationEnv(t)
	t.Setenv("FAKE_TOGETHER_FAILS", "1")
	cands, _, err := rotationCandidates(t.Context(), env, "hb-rotation", "hb-rotation")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, c := range cands {
		keys = append(keys, c.Key)
	}
	if want := []string{"auth.password", "extra.dbPassword", "gen.password"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("candidates %v, want %v; session.token stays out, as its key differs on every render", keys, want)
	}
	b, _ := os.ReadFile(sets)
	if n := strings.Count(string(b), "gen.password="); n != 3 {
		t.Errorf("gen.password rendered %d times, want 3: alone, with the others, alone again", n)
	}
}

func TestRotationCandidatesCapsTheRenders(t *testing.T) {
	old := maxRotationRenders
	t.Cleanup(func() { maxRotationRenders = old })
	maxRotationRenders = 2
	env, _ := rotationEnv(t)
	cands, skipped, err := rotationCandidates(t.Context(), env, "hb-rotation", "hb-rotation")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Key != "auth.password" {
		t.Errorf("candidates = %+v, want auth.password only, from the first two values", cands)
	}
	want := []string{"broken.apiKey", "extra.dbPassword", "gen.password", "session.token", "tls.privateKey", "webhook.secretKey"}
	if !reflect.DeepEqual(skipped, want) {
		t.Errorf("skipped = %v, want %v", skipped, want)
	}
}

func TestRotationCandidatesFailsOnAMissingValuesFile(t *testing.T) {
	env, _ := rotationEnv(t)
	env.Values.Files = []string{filepath.Join(t.TempDir(), "missing.yaml")}
	if _, _, err := rotationCandidates(t.Context(), env, "hb-rotation", "hb-rotation"); err == nil {
		t.Error("want an error for a values file that cannot be read")
	}
}
