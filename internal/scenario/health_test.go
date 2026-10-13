package scenario

import (
	"reflect"
	"testing"

	"github.com/egezamb/helmbulance/internal/kube"
)

func TestPodProblems(t *testing.T) {
	before := map[string]int{"d0": 1}
	pods := []kube.Pod{
		{Name: "web-1", UID: "w1", Owner: "ReplicaSet/web-7d9f8c6b5", Ready: true, Phase: "Running"},
		{Name: "db-0", UID: "d0", Owner: "StatefulSet/db", Ready: true, Phase: "Running", Restarts: 3},
		{Name: "api-1", UID: "a1", Owner: "ReplicaSet/api-55c4d", Phase: "Running"},
	}
	want := []string{
		"pod `api-1` (ReplicaSet/api-55c4d) is not ready",
		"pod `db-0` (StatefulSet/db) restarted 2 more time(s) after the upgrade",
	}
	if got := podProblems(pods, before); !reflect.DeepEqual(got, want) {
		t.Errorf("podProblems =\n%q\nwant\n%q", got, want)
	}
}

// A Job's failed first attempt is not an unhealthy app (issue #9).
func TestPodProblemsIgnoresJobAttempts(t *testing.T) {
	pods := []kube.Pod{
		{Name: "web-1", UID: "w1", Owner: "ReplicaSet/web-7d9f8c6b5", Ready: true, Phase: "Running"},
		{Name: "migrate-x", UID: "m1", Owner: "Job/migrate", Phase: "Failed", Restarts: 1},
		{Name: "migrate-y", UID: "m2", Owner: "Job/migrate", Phase: "Running"},
	}
	if got := podProblems(pods, map[string]int{}); len(got) != 0 {
		t.Errorf("podProblems = %q, want none", got)
	}
}

func TestNewlyFailed(t *testing.T) {
	before := []string{"Job/old: BackoffLimitExceeded"}
	now := []string{"Job/migrate: BackoffLimitExceeded", "Job/old: BackoffLimitExceeded"}
	if got := newlyFailed(now, before); !reflect.DeepEqual(got, []string{"Job/migrate: BackoffLimitExceeded"}) {
		t.Errorf("newlyFailed = %q, want only Job/migrate", got)
	}
}

// Restarts are counted per pod, so a Deployment's new pods are checked too, and a pod replaced by
// the rollout starts from zero.
func TestPodProblemsCountsRestartsPerPod(t *testing.T) {
	before := map[string]int{"w1": 4, "d0": 0}
	pods := []kube.Pod{
		{Name: "web-new", UID: "w2", Owner: "ReplicaSet/web-6c8b7d4f9", Ready: true, Phase: "Running", Restarts: 2},
		{Name: "db-0", UID: "d1", Owner: "StatefulSet/db", Ready: true, Phase: "Running"},
	}
	want := []string{"pod `web-new` (ReplicaSet/web-6c8b7d4f9) restarted 2 more time(s) after the upgrade"}
	if got := podProblems(pods, before); !reflect.DeepEqual(got, want) {
		t.Errorf("podProblems =\n%q\nwant\n%q", got, want)
	}
}
