package scenario

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Git-Hospital/helmbulance/internal/execx"
	"github.com/Git-Hospital/helmbulance/internal/kube"
)

// recordPodSecurity puts the reached level in the result as information. It reads nothing for a
// SKIPPED run or after an interrupt, and never changes the verdict (#115).
func TestRecordPodSecurity(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n  *\" get pods -o name\") echo pod/app ;;\n" +
		"  *\"enforce=restricted\") echo 'Warning: app: seccompProfile' >&2 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	runner := &execx.Runner{}
	env := &Env{Runner: runner, Kube: &kube.Client{Runner: runner}, Log: io.Discard}

	res := Result{Verdict: Fail}
	recordPodSecurity(t.Context(), env, "ns", &res)
	if res.PodSecurity == nil || res.PodSecurity.Level != "baseline" || res.Verdict != Fail {
		t.Errorf("result %+v, want level baseline and the verdict unchanged", res)
	}

	skipped := Result{Verdict: Skipped}
	recordPodSecurity(t.Context(), env, "ns", &skipped)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	interrupted := Result{Verdict: Inconclusive}
	recordPodSecurity(ctx, env, "ns", &interrupted)
	if skipped.PodSecurity != nil || interrupted.PodSecurity != nil {
		t.Errorf("skipped %+v, interrupted %+v; want no level read", skipped.PodSecurity, interrupted.PodSecurity)
	}
}
