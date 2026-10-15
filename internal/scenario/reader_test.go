package scenario

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
	"github.com/egezamb/helmbulance/internal/helm"
	"github.com/egezamb/helmbulance/internal/kube"
	"github.com/egezamb/helmbulance/internal/oracle"
)

// fakeReaderTools: the n-th "helm get manifest" prints $FAKE_DIR/manifest.<n> (or the last one),
// "helm get hooks" prints nothing, and "kubectl get secrets" prints $FAKE_DIR/secrets.
const fakeReaderTools = `#!/bin/sh
case "$(basename "$0") $*" in
  "helm get manifest"*) n=$(cat "$FAKE_DIR/n" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$FAKE_DIR/n"
    f="$FAKE_DIR/manifest.$n"; [ -f "$f" ] || f=$(ls "$FAKE_DIR"/manifest.* | sort | tail -n 1); cat "$f" ;;
  "kubectl"*"get secrets"*) cat "$FAKE_DIR/secrets" ;;
esac
exit 0
`

func secretManifest(names ...string) string {
	out := ""
	for _, n := range names {
		out += "---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: " + n + "\n"
	}
	return out
}

// newFakeReader returns a secretReader whose release renders manifests[i] on the i-th read of
// what it owns, in a namespace that holds secretsJSON.
func newFakeReader(t *testing.T, secretsJSON string, manifests ...string) *secretReader {
	t.Helper()
	old := settleInterval
	t.Cleanup(func() { settleInterval = old })
	settleInterval = time.Millisecond
	bin, dir := t.TempDir(), t.TempDir()
	for _, name := range []string{"helm", "kubectl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(fakeReaderTools), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"secrets": secretsJSON}
	for i, m := range manifests {
		files["manifest."+strconv.Itoa(i+1)] = m
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
	env := &Env{
		Runner: runner,
		Helm:   &helm.Client{Runner: runner, Timeout: time.Minute},
		Kube:   &kube.Client{Runner: runner},
		Hasher: hasher,
		Log:    io.Discard,
	}
	return newSecretReader(t.Context(), env, "hb", "ns")
}

// The generate-once pattern: the install renders the Secret, later upgrades find it with lookup and
// render nothing, and helm.sh/resource-policy: keep leaves it in place, unchanged. It is the
// release's Secret all along, so the second snapshot must still compare it.
func TestSecretReaderKeepsASecretTheReleaseStoppedRendering(t *testing.T) {
	r := newFakeReader(t, `{"items":[{"metadata":{"name":"app-secret"},"type":"Opaque","data":{"password":"cHc="}}]}`,
		secretManifest("app-secret"), secretManifest())
	installed, err := r.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := r.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if changes := oracle.Diff(installed, upgraded); len(changes) != 0 {
		t.Errorf("Diff = %v, want no change for a kept Secret", changes)
	}
	if n := r.notComparedNote(); n != "" {
		t.Errorf("notComparedNote = %q, want nothing: the Secret was compared", n)
	}
}

// A Secret the release stops rendering without keep is deleted by helm: still removed.
func TestSecretReaderReportsADeletedSecret(t *testing.T) {
	r := newFakeReader(t, `{"items":[{"metadata":{"name":"app-secret"},"type":"Opaque","data":{"password":"cHc="}}]}`,
		secretManifest("app-secret"), secretManifest())
	installed, err := r.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("FAKE_DIR"), "secrets"), []byte(`{"items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	upgraded, err := r.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if changes := oracle.Diff(installed, upgraded); len(changes) != 1 || changes[0].Kind != oracle.Removed {
		t.Errorf("Diff = %v, want app-secret/password removed", changes)
	}
}

// A Secret that the release starts rendering later is compared from then on, so the note does not
// call it "not compared".
func TestSecretReaderNoteLeavesOutSecretsRenderedLater(t *testing.T) {
	r := newFakeReader(t, `{"items":[{"metadata":{"name":"late"},"type":"Opaque","data":{"k":"dg=="}},{"metadata":{"name":"job-written"},"type":"Opaque","data":{"k":"dg=="}}]}`,
		secretManifest(), secretManifest("late"))
	for range 2 {
		if _, err := r.snapshot(); err != nil {
			t.Fatal(err)
		}
	}
	if n, want := r.notComparedNote(), "not compared, since the release does not render them: `job-written`"; n != want {
		t.Errorf("notComparedNote = %q, want %q", n, want)
	}
}
