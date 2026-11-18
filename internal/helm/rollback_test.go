package helm

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRollbackNamesTheRevision(t *testing.T) {
	c := fakeHelm(t, "")
	bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
	calls := filepath.Join(t.TempDir(), "calls")
	if err := os.WriteFile(filepath.Join(bin, "helm"), []byte("#!/bin/sh\necho \"$*\" > "+calls+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := c.Rollback(t.Context(), "hb", "ns", 3); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	if !strings.HasPrefix(string(b), "rollback hb 3 ") || !strings.Contains(string(b), "--wait") {
		t.Errorf("helm %s; want rollback hb 3 … --wait", b)
	}
}

func TestHistory(t *testing.T) {
	c := fakeHelm(t, `[{"revision":1,"status":"superseded","chart":"app-1.0.0","app_version":"1"},`+
		`{"revision":2,"status":"deployed","chart":"app-1.0.0","app_version":"1","description":"Rollback to 1"}]`)
	got, err := c.History(t.Context(), "hb", "ns")
	if err != nil {
		t.Fatal(err)
	}
	want := []Revision{{1, "superseded", "app-1.0.0", "1"}, {2, "deployed", "app-1.0.0", "1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("History = %+v, want %+v", got, want)
	}
	if _, err := fakeHelm(t, "[]").History(t.Context(), "hb", "ns"); err == nil {
		t.Error("an empty history gave no error")
	}
}
