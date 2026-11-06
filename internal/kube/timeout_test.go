package kube

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/helmbulance/internal/execx"
)

// Every kubectl read gets --request-timeout, so a cluster that stops answering fails it in a known
// time; a watch (rollout status, wait, delete --wait) gets none, since it would cut the watch short
// before its own --timeout (#54).
func TestReadsHaveARequestTimeoutAndWatchesDoNot(t *testing.T) {
	bin, calls := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\ncase \"$*\" in *\" get \"*) echo '{\"items\":[]}' ;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := &Client{Runner: &execx.Runner{}}
	ctx := t.Context()
	_, _ = c.Secrets(ctx, "ns")
	_, _ = c.Pods(ctx, "ns")
	_, _ = c.Services(ctx, "ns")
	_, _ = c.Workloads(ctx, "ns")
	_, _ = c.SecretConsumers(ctx, "ns")
	_, _ = c.Logs(ctx, "ns", "p")
	_ = c.RestartWorkload(ctx, "ns", Workload{Kind: "Deployment", Name: "web"}, (time.Minute).String())
	_ = c.WaitPodRunning(ctx, "ns", "p", "1m")
	_ = c.DeleteNamespace(ctx, "ns")
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		read := strings.Contains(line, " get ") || strings.Contains(line, " logs ")
		has := strings.Contains(line, "--request-timeout 60s")
		if read != has {
			t.Errorf("read %v, --request-timeout %v: %s", read, has, line)
		}
	}
}
