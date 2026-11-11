//go:build unix

package sandbox

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With HELMBULANCE_STATE_DIR somewhere shared, another user could make clusters/ first and
// replace a kubeconfig and its fingerprint, so a directory others can change is refused (#155).
func TestOpenRefusesAStateDirectoryOthersCanChange(t *testing.T) {
	for _, tc := range []struct {
		name string
		mess func(state, clusters, dir string) error
		why  string
	}{
		{"clusters/ open to others", func(_, clusters, _ string) error { return os.Chmod(clusters, 0o755) }, "has mode 0755, want 0700"},
		{"the cluster's directory open to its group", func(_, _, dir string) error { return os.Chmod(dir, 0o750) }, "has mode 0750, want 0700"},
		{"a state directory anyone can write", func(state, _, _ string) error { return os.Chmod(state, 0o777) }, "so others could replace the state directory"},
		{"the cluster's directory a symlink", func(_, clusters, dir string) error {
			moved := filepath.Join(clusters, "elsewhere")
			if err := os.Rename(dir, moved); err != nil {
				return err
			}
			return os.Symlink(moved, dir)
		}, "is a symbolic link"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeCluster(t, kindConfig)
			c, err := clusterFiles(name)
			if err != nil {
				t.Fatal(err)
			}
			clusters := filepath.Dir(c.dir)
			if err := tc.mess(filepath.Dir(clusters), clusters, c.dir); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(name); err == nil || !strings.Contains(err.Error(), tc.why) {
				t.Errorf("Open = %v, want an error with %q", err, tc.why)
			}
		})
	}
}

// A sticky state directory such as /tmp is fine: nobody else can rename clusters/ in it.
func TestOpenAcceptsAStickyStateDirectory(t *testing.T) {
	writeCluster(t, kindConfig)
	if err := os.Chmod(os.Getenv("HELMBULANCE_STATE_DIR"), 0o777|fs.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(name); err != nil {
		t.Errorf("Open = %v, want the sticky state directory accepted", err)
	}
}

// MkdirAll leaves an existing directory as it is, so NewCluster checks it before kind writes a
// kubeconfig into it.
func TestNewClusterRefusesAClustersDirectoryOthersCanWrite(t *testing.T) {
	state := t.TempDir()
	t.Setenv("HELMBULANCE_STATE_DIR", state)
	if err := os.Mkdir(filepath.Join(state, "clusters"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(state, "clusters"), 0o777); err != nil {
		t.Fatal(err)
	}
	if c, err := NewCluster(); err == nil || !strings.Contains(err.Error(), "want 0700") {
		t.Errorf("NewCluster = %v, %v; want an error with %q", c, err, "want 0700")
	}
}

// A directory of another user is refused even with mode 0700. "/" belongs to root.
func TestCheckPrivateRefusesAnotherUsersDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, who owns /")
	}
	state, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = checkPrivate(state, "/", "/")
	if err == nil || !strings.Contains(err.Error(), "belongs to user 0") {
		t.Errorf("checkPrivate = %v, want an error with %q", err, "belongs to user 0")
	}
}

// helm and kubectl open the kubeconfig by path on every call. With the state directory behind a
// symlink, retargeting the symlink after Open would hand them another file, one with an exec user
// (security review of #156). Open names the files by the real path, so later calls keep reading
// the file it checked.
func TestOpenNamesTheFilesByTheStateDirectorysRealPath(t *testing.T) {
	writeCluster(t, kindConfig)
	real := os.Getenv("HELMBULANCE_STATE_DIR")
	link := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HELMBULANCE_STATE_DIR", link)
	c, err := Open(name)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(resolved, "clusters", name, "kubeconfig.pinned"); c.Kubeconfig != want {
		t.Fatalf("Kubeconfig = %s, want %s", c.Kubeconfig, want)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := caFingerprint(c.Kubeconfig, name); err != nil {
		t.Errorf("the pinned copy after the symlink moved: %v", err)
	}
}

// A state directory of this user inside a directory anyone can write is not safe: they can rename
// it and put their own in its place (security review of #156).
func TestOpenRefusesADirectoryAboveTheStateDirectoryOthersCanWrite(t *testing.T) {
	shared := t.TempDir()
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HELMBULANCE_STATE_DIR", filepath.Join(shared, "hb"))
	if c, err := NewCluster(); err == nil || !strings.Contains(err.Error(), "has mode 0777, so others could replace") {
		t.Errorf("NewCluster = %v, %v; want the shared directory above the state directory refused", c, err)
	}
	if _, err := os.Stat(filepath.Join(shared, "hb", "clusters")); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(filepath.Join(shared, "hb", "clusters")); len(left) != 0 {
		t.Errorf("NewCluster left %d directories behind after refusing", len(left))
	}
}
