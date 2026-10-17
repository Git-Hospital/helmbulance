package kube

import (
	"reflect"
	"testing"
)

func TestContainersReadsWaitingAndLastRun(t *testing.T) {
	c := fakeKubectl(t, `{"items":[
		{"metadata":{"name":"web-1","uid":"u1","ownerReferences":[{"kind":"ReplicaSet","name":"web-abc"}]},
		 "status":{
		  "initContainerStatuses":[{"name":"migrate","restartCount":0,"state":{"terminated":{"reason":"Completed","exitCode":0}}}],
		  "containerStatuses":[
		   {"name":"app","restartCount":3,"state":{"waiting":{"reason":"CrashLoopBackOff"}},"lastState":{"terminated":{"reason":"Error","exitCode":1}}},
		   {"name":"sidecar","restartCount":0,"state":{"running":{}}}]}},
		{"metadata":{"name":"web-2","uid":"u2","ownerReferences":[{"kind":"ReplicaSet","name":"web-abc"}]},
		 "status":{"containerStatuses":[{"name":"app","restartCount":0,"state":{"waiting":{"reason":"ImagePullBackOff"}}}]}},
		{"metadata":{"name":"old","uid":"u3","deletionTimestamp":"2026-10-17T12:00:00Z"},
		 "status":{"containerStatuses":[{"name":"app","restartCount":9,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}
	]}`)
	got, err := c.Containers(t.Context(), "ns")
	if err != nil {
		t.Fatal(err)
	}
	want := []Container{
		{Pod: "web-1", PodUID: "u1", Owner: "ReplicaSet/web-abc", Name: "migrate", Init: true, LastReason: "Completed"},
		{Pod: "web-1", PodUID: "u1", Owner: "ReplicaSet/web-abc", Name: "app", Restarts: 3, Waiting: "CrashLoopBackOff", LastReason: "Error", LastExit: 1},
		{Pod: "web-1", PodUID: "u1", Owner: "ReplicaSet/web-abc", Name: "sidecar"},
		{Pod: "web-2", PodUID: "u2", Owner: "ReplicaSet/web-abc", Name: "app", Waiting: "ImagePullBackOff"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Containers =\n%+v\nwant\n%+v", got, want)
	}
}
