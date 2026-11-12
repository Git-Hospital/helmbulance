package main

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalPaths(t *testing.T) {
	t.Setenv("HOME", "/home/ege")
	t.Setenv("HELMBULANCE_STATE_DIR", "/srv/hb-state")
	want := map[string]string{"/home/ege": "~", "/srv/hb-state": "$HELMBULANCE_STATE_DIR"}
	if got := localPaths(); !maps.Equal(got, want) {
		t.Errorf("localPaths() = %v, want %v", got, want)
	}

	// The default state dir is under $HOME and shows as ~/.helmbulance.
	t.Setenv("HELMBULANCE_STATE_DIR", "")
	want = map[string]string{"/home/ege": "~"}
	if got := localPaths(); !maps.Equal(got, want) {
		t.Errorf("localPaths() = %v, want %v", got, want)
	}

	// A trailing slash on either is dropped, or nothing the tools print would match.
	t.Setenv("HOME", "/home/ege/")
	t.Setenv("HELMBULANCE_STATE_DIR", "/srv/hb-state/")
	want = map[string]string{"/home/ege": "~", "/srv/hb-state": "$HELMBULANCE_STATE_DIR"}
	if got := localPaths(); !maps.Equal(got, want) {
		t.Errorf("localPaths() with trailing slashes = %v, want %v", got, want)
	}
}

// The sandbox names its files by the state dir's real path, so a state dir behind a symlink is
// hidden by both names (#155).
func TestLocalPathsHidesTheRealPathOfASymlink(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "/home/ege")
	t.Setenv("HELMBULANCE_STATE_DIR", link)
	got := localPaths()
	if got[link] != "$HELMBULANCE_STATE_DIR" || got[resolved] != "$HELMBULANCE_STATE_DIR" {
		t.Errorf("localPaths() = %v, want both %s and %s hidden", got, link, resolved)
	}
}
