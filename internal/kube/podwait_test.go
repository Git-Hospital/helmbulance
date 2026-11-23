package kube

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/helmbulance/internal/execx"
)

func TestParsePodWaitReason(t *testing.T) {
	long := strings.Repeat("x", maxWaitMessage+50)
	cases := []struct{ name, pod, want string }{
		{"image pull", `{"status":{"phase":"Pending","containerStatuses":[{"name":"sentinel","state":{"waiting":{"reason":"ErrImagePull",
			"message":"failed to pull \"busybox:1.36.1\":\n429 Too Many Requests"}}}]}}`,
			`container sentinel is waiting: ErrImagePull (failed to pull "busybox:1.36.1": 429 Too Many Requests)`},
		{"init container first", `{"status":{"initContainerStatuses":[{"name":"init","state":{"waiting":{"reason":"PodInitializing"}}}],
			"containerStatuses":[{"name":"app","state":{"waiting":{"reason":"PodInitializing"}}}]}}`,
			"container init is waiting: PodInitializing"},
		{"running and reasonless containers skipped", `{"status":{"containerStatuses":[{"name":"a","state":{"running":{}}},{"name":"z","state":{"waiting":{}}},{"name":"b","state":{"waiting":{"reason":"ImagePullBackOff","message":"Back-off pulling image"}}}]}}`,
			"container b is waiting: ImagePullBackOff (Back-off pulling image)"},
		{"not scheduled", `{"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False","reason":"Unschedulable",
			"message":"0/1 nodes are available: pod has unbound immediate PersistentVolumeClaims."}]}}`,
			"not scheduled: Unschedulable (0/1 nodes are available: pod has unbound immediate PersistentVolumeClaims.)"},
		{"scheduled, nothing waiting", `{"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"True"}]}}`, ""},
		{"long message cut", `{"status":{"containerStatuses":[{"name":"c","state":{"waiting":{"reason":"ErrImagePull","message":"` + long + `"}}}]}}`,
			"container c is waiting: ErrImagePull (" + long[:maxWaitMessage] + "…)"},
		{"not json", `error: pods "x" not found`, ""},
	}
	for _, tc := range cases {
		if got := parsePodWaitReason([]byte(tc.pod)); got != tc.want {
			t.Errorf("%s: parsePodWaitReason = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A sentinel pod that never starts says why in the error, so a Docker Hub rate limit is told apart
// from a volume that never mounts (#176).
func TestRunWithPVCSaysWhyThePodWaits(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in *jsonpath=*) printf Pending ;; *\"get pod hb-sentinel -o json\"*) " +
		`echo '{"status":{"containerStatuses":[{"name":"hb-sentinel","state":{"waiting":{"reason":"ErrImagePull","message":"429 Too Many Requests"}}}]}}' ;; esac` + "\n"
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := &Client{Runner: &execx.Runner{}}
	_, err := c.RunWithPVC(t.Context(), "ns", "hb-sentinel", "busybox", "data", "true", time.Millisecond)
	want := "pod hb-sentinel is Pending after 1ms (mounting data): container hb-sentinel is waiting: ErrImagePull (429 Too Many Requests)"
	if err == nil || err.Error() != want {
		t.Errorf("RunWithPVC = %v, want %q", err, want)
	}
}
