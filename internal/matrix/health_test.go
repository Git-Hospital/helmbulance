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

func TestRestartedSince(t *testing.T) {
	first := map[string]int{"w1": 1}
	pods := []kube.Pod{
		{Name: "web-1", UID: "w1", Ready: true, Restarts: 3},
		{Name: "web-2", UID: "w2", Ready: true},
		{Name: "migrate", UID: "m1", Owner: "Job/migrate", Restarts: 2},
	}
	got := restartedSince(pods, first)
	if len(got) != 1 || got[0] != "pod `web-1` restarted 2 time(s) while Helmbulance watched it" {
		t.Errorf("restartedSince = %q, want web-1 only", got)
	}
}
