//go:build unix

package sandbox

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// checkPrivate refuses a state directory that someone else could change under a run. The CA
// fingerprint protects against mistakes, not against a user who can write next to it: with
// HELMBULANCE_STATE_DIR in a shared place such as /tmp, another user could create clusters/ first
// and replace both a kubeconfig and its fingerprint (#155). state must be a real path with no
// symlinks. It and every directory above it must belong to this user or root and be writable only
// by its owner, or sticky like /tmp, so that nobody else can rename an entry on the way down. As
// OpenSSH's StrictModes does, the whole path is checked: a check on the last directory alone holds
// only until someone swaps one above it. clusters/ and the cluster's own directory must be real
// directories of this user that nobody else can enter.
func checkPrivate(state, clusters, dir string) error {
	uid := os.Getuid()
	for d := state; ; d = filepath.Dir(d) {
		fi, err := os.Lstat(d)
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s is not a directory; refusing to use it", d)
		}
		if o := owner(fi); o != uid && o != 0 {
			return fmt.Errorf("%s belongs to user %d, not to you (%d), so they could replace the state directory; refusing to use it", d, o, uid)
		}
		if fi.Mode().Perm()&0o022 != 0 && fi.Mode()&fs.ModeSticky == 0 {
			return fmt.Errorf("%s has mode %04o, so others could replace the state directory; make it writable only by its owner", d, fi.Mode().Perm())
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	for _, d := range []string{clusters, dir} {
		fi, err := os.Lstat(d)
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link, not a directory; refusing to use it", d)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s is not a directory; refusing to use it", d)
		}
		if o := owner(fi); o != uid {
			return fmt.Errorf("%s belongs to user %d, not to you (%d); refusing to use it", d, o, uid)
		}
		if p := fi.Mode().Perm(); p&0o077 != 0 {
			return fmt.Errorf("%s has mode %04o, want 0700; refusing to use it", d, p)
		}
	}
	return nil
}

func owner(fi fs.FileInfo) int {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}
