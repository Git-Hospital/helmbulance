package scenario

import (
	"reflect"
	"testing"

	"github.com/egezamb/helmbulance/internal/kube"
)

func TestPodProblems(t *testing.T) {
	before := map[string]int{"StatefulSet/db": 1}
	pods := []kube.Pod{
		{Name: "web-1", UID: "w1", Owner: "ReplicaSet/web-7d9f8c6b5", Ready: true, Phase: "Running"},
		{Name: "db-0", UID: "d0", Owner: "StatefulSet/db", Ready: true, Phase: "Running", Restarts: 3},
		{Name: "api-1", UID: "a1", Owner: "ReplicaSet/api-55c4d", Phase: "Running"},
	}
	want := []string{
		"`StatefulSet/db` restarted 2 more time(s) after the upgrade",
		"pod `api-1` (ReplicaSet/api-55c4d) is not ready",
	}
	if got := podProblems(pods, before); !reflect.DeepEqual(got, want) {
		t.Errorf("podProblems =\n%q\nwant\n%q", got, want)
	}
}
