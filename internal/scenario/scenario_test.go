package scenario

import (
	"reflect"
	"testing"

	"github.com/egezamb/helmbulance/internal/kube"
)

func TestReplacedPods(t *testing.T) {
	before := []kube.Pod{
		{UID: "a", Owner: "ReplicaSet/web-7d9f8c6b5"},
		{UID: "b", Owner: "StatefulSet/db"},
		{UID: "c", Owner: "ReplicaSet/worker-55c4d"},
	}
	after := []kube.Pod{
		{UID: "a2", Owner: "ReplicaSet/web-6c8b7d4f9"}, // web was rolled
		{UID: "b", Owner: "StatefulSet/db"},            // db kept its pod
		{UID: "c", Owner: "ReplicaSet/worker-55c4d"},
	}
	want := []string{"Deployment/web"}
	if got := replacedPods(before, after); !reflect.DeepEqual(got, want) {
		t.Errorf("replacedPods = %v, want %v", got, want)
	}
}

func TestServing(t *testing.T) {
	web := kube.Workload{Kind: "Deployment", Name: "web", PodLabels: map[string]string{"app": "web", "tier": "front"}}
	db := kube.Workload{Kind: "StatefulSet", Name: "db", PodLabels: map[string]string{"app": "db"}}
	services := []kube.Service{
		{Name: "web", Selector: map[string]string{"app": "web"}},
		{Name: "db", Selector: map[string]string{"app": "db"}},
	}
	got := serving([]kube.Workload{web, db}, services, "web:80")
	if len(got) != 1 || got[0].Name != "web" {
		t.Errorf("serving(web:80) = %v, want only web", got)
	}
	if got := serving([]kube.Workload{web, db}, services, "missing:80"); len(got) != 2 {
		t.Errorf("unknown Service should fall back to every workload, got %v", got)
	}
}

func TestOutageText(t *testing.T) {
	if got := outageText(0); got != "under a second" {
		t.Errorf("outageText(0) = %q", got)
	}
}
