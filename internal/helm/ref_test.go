package helm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsLocal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, d := range []string{"chart", "repo/chart", "zz/c"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("app-1.0.0.tgz", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "chart"), "link"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "gone"), "dangling"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"./chart":                           true,
		"chart":                             true,
		" ./chart":                          true, // helm trims the reference
		"repo/chart":                        true, // helm prefers the local directory
		"app-1.0.0.tgz":                     true,
		"link":                              true,
		"dangling":                          false,
		"zz/c ":                             true, // trimmed to zz/c, which exists
		"oci://registry.example/charts/app": false,
		"https://example.com/app-1.0.0.tgz": false,
		"bitnami/postgresql":                false,
		"":                                  false,
	}
	for ref, want := range cases {
		if got := IsLocal(ref); got != want {
			t.Errorf("IsLocal(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestIsPath(t *testing.T) {
	cases := map[string]bool{
		"./nonexistent":     true,
		"~/chart":           true,
		"/abs/chart":        true,
		"typo":              true,
		"bitnami/redis":     false,
		"oci://x/y":         false,
		"https://x/y-1.tgz": false,
	}
	for ref, want := range cases {
		if got := IsPath(ref); got != want {
			t.Errorf("IsPath(%q) = %v, want %v", ref, got, want)
		}
	}
}
