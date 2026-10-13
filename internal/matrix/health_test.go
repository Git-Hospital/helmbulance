package matrix

import (
	"testing"

	"github.com/egezamb/helmbulance/internal/kube"
)

func TestNotReady(t *testing.T) {
	pods := []kube.Pod{
		{Name: "web-1", Owner: "ReplicaSet/web-7d9f8c6b5", Ready: true, Phase: "Running"},
		{Name: "web-2", Owner: "ReplicaSet/web-7d9f8c6b5", Phase: "Running", Restarts: 3},
	}
	if got := notReady(pods); len(got) != 1 || got[0] != "pod `web-2` is not ready (3 restart(s))" {
		t.Errorf("notReady = %q, want web-2 only", got)
	}
}

func TestNotReadyIgnoresJobAttempts(t *testing.T) {
	pods := []kube.Pod{
		{Name: "web-1", Owner: "ReplicaSet/web-7d9f8c6b5", Ready: true, Phase: "Running"},
		{Name: "migrate-x", Owner: "Job/migrate", Phase: "Failed"},
	}
	if got := notReady(pods); len(got) != 0 {
		t.Errorf("notReady = %q, want none", got)
	}
}
