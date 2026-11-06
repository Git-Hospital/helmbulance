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
		{"a state directory anyone can write", func(state, _, _ string) error { return os.Chmod(state, 0o777) }, "so others can replace what is in it"},
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
	err := checkPrivate(t.TempDir(), "/", "/")
	if err == nil || !strings.Contains(err.Error(), "belongs to user 0") {
		t.Errorf("checkPrivate = %v, want an error with %q", err, "belongs to user 0")
	}
}
