//go:build unix

package sandbox

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// checkPrivate refuses a state directory that someone else could change under a run. The CA
// fingerprint protects against mistakes, not against a user who can write next to it: with
// HELMBULANCE_STATE_DIR in a shared place such as /tmp, another user could create clusters/ first
// and replace both a kubeconfig and its fingerprint (#155). So clusters/ and the cluster's own
// directory must be real directories of this user that nobody else can enter, and the state
// directory above them must belong to this user or root and be writable only by its owner, or
// sticky like /tmp, so that nobody else can swap clusters/ for their own.
func checkPrivate(state, clusters, dir string) error {
	uid := os.Getuid()
	fi, err := os.Stat(state)
	if err != nil {
		return err
	}
	if o := owner(fi); o != uid && o != 0 {
		return fmt.Errorf("state directory %s belongs to user %d, not to you (%d); refusing to use it", state, o, uid)
	}
	if fi.Mode().Perm()&0o022 != 0 && fi.Mode()&fs.ModeSticky == 0 {
		return fmt.Errorf("state directory %s has mode %04o, so others can replace what is in it; make it writable only by you", state, fi.Mode().Perm())
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
