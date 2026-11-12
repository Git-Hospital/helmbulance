//go:build unix

package scan

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO or a link to /dev/zero among the templates must not block or fill memory: helm template
// refuses neither when .helmignore lists it, so scan reaches them.
func TestChartFilesSkipsFIFOsAndDevices(t *testing.T) {
	chart := t.TempDir()
	writeFiles(t, chart, map[string]string{"templates/secret.yaml": guarded})
	if err := syscall.Mkfifo(filepath.Join(chart, "templates", "fifo.yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/zero", filepath.Join(chart, "templates", "zero.yaml")); err != nil {
		t.Fatal(err)
	}
	done := make(chan map[string]string)
	go func() { done <- chartFiles(chart) }()
	select {
	case files := <-done:
		if len(files) != 1 || files["templates/secret.yaml"] == "" {
			t.Errorf("files = %v, want only templates/secret.yaml", keys(files))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("chartFiles blocked on a FIFO or a device")
	}
}

// A chart given through a link to its directory is read like the directory.
func TestChartFilesFollowsALinkToTheChart(t *testing.T) {
	chart := t.TempDir()
	writeFiles(t, chart, map[string]string{"templates/secret.yaml": guarded})
	link := filepath.Join(t.TempDir(), "app")
	if err := os.Symlink(chart, link); err != nil {
		t.Fatal(err)
	}
	if uses, readable := lookupChecker(link); !readable || !uses("app/templates/secret.yaml") {
		t.Error("a lookup in a chart given through a link was missed")
	}
}
