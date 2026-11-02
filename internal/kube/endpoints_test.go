package kube

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
)

func TestOutsideEndpoints(t *testing.T) {
	const managed = `"endpointslice.kubernetes.io/managed-by":"endpointslice-controller.k8s.io"`
	c := fakeKubectl(t, `{"kind":"List","items":[
		{"kind":"EndpointSlice","metadata":{"name":"web-abc","labels":{"kubernetes.io/service-name":"web",`+managed+`}},
			"endpoints":[{"addresses":["10.244.0.5"]},{"addresses":["10.244.0.6"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"web-v6","labels":{"kubernetes.io/service-name":"web",`+managed+`}},
			"endpoints":[{"addresses":["fd00::5"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"api-abc","labels":{"kubernetes.io/service-name":"api",`+managed+`}},
			"endpoints":[{"addresses":["10.244.0.6"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"api-extra","labels":{"kubernetes.io/service-name":"api"}},
			"endpoints":[{"addresses":["203.0.113.9"]},{"addresses":["198.51.100.1"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"api-again","labels":{"kubernetes.io/service-name":"api"}},
			"endpoints":[{"addresses":["203.0.113.9"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"node","labels":{"kubernetes.io/service-name":"node","endpointslice.kubernetes.io/managed-by":"my-chart"}},
			"endpoints":[{"addresses":["172.18.0.2"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"legacy-x1","labels":{"kubernetes.io/service-name":"legacy","endpointslice.kubernetes.io/managed-by":"endpointslicemirroring-controller.k8s.io"}},
			"endpoints":[{"addresses":["10.244.0.5"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"names","labels":{"kubernetes.io/service-name":"names"}},
			"addressType":"FQDN","endpoints":[{"addresses":["example.com"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"unlabelled"},"endpoints":[{"addresses":["203.0.113.10"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"forged","labels":{"kubernetes.io/service-name":"forged",`+managed+`}},
			"endpoints":[{"addresses":["10.244.0.5"]},{"addresses":["203.0.113.20"]}]},
		{"kind":"Pod","metadata":{"name":"web-1"},"status":{"podIP":"10.244.0.5","podIPs":[{"ip":"10.244.0.5"},{"ip":"fd00::5"}]}},
		{"kind":"Pod","metadata":{"name":"web-2"},"status":{"podIP":"10.244.0.6","podIPs":[{"ip":"10.244.0.6"}]}},
		{"kind":"Pod","metadata":{"name":"on-node"},"spec":{"hostNetwork":true},"status":{"podIP":"172.18.0.2","podIPs":[{"ip":"172.18.0.2"}]}},
		{"kind":"EndpointSlice","metadata":{"name":"job","labels":{"kubernetes.io/service-name":"job"}},"endpoints":[{"addresses":["10.244.0.7"]}]},
		{"kind":"Pod","metadata":{"name":"done-1"},"status":{"phase":"Succeeded","podIP":"10.244.0.7","podIPs":[{"ip":"10.244.0.7"}]}}
	]}`)
	got, err := c.OutsideEndpoints(t.Context(), "ns")
	if err != nil {
		t.Fatal(err)
	}
	// An address counts as inside only when a pod in the namespace has it, whatever the slice's
	// labels say: a chart can label its own slice as the control plane's ("forged"). A hostNetwork
	// pod's IP is the node's, so it is outside; a pod IP mirrored from the chart's Endpoints object
	// ("legacy") is one of the release's pods and stays inside.
	want := map[string][]string{
		"api":    {"198.51.100.1", "203.0.113.9"},
		"node":   {"172.18.0.2"},
		"names":  {"example.com"},
		"forged": {"203.0.113.20"},
		"job":    {"10.244.0.7"}, // a finished pod's IP may already belong to another pod
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OutsideEndpoints = %v, want %v", got, want)
	}
}

// During a rollout the controller drops a deleted pod from its slice a moment after the pod is gone.
// An address is reported only if both reads show it outside: one seen in a single read, the first
// or the second, is not.
func TestOutsideEndpointsReadsAgain(t *testing.T) {
	old := OutsideRecheck
	OutsideRecheck = time.Millisecond
	defer func() { OutsideRecheck = old }()
	slice := `{"kind":"EndpointSlice","metadata":{"labels":{"kubernetes.io/service-name":"web"}},"endpoints":[{"addresses":[%s]}]}`
	pod := `{"kind":"Pod","metadata":{"name":"web-2"},"status":{"podIP":"10.244.0.6"}}`
	dir := t.TempDir()
	reads := []string{
		`{"kind":"List","items":[` + fmt.Sprintf(slice, `"10.244.0.5","10.244.0.6","203.0.113.9"`) + "," + pod + `]}`,
		`{"kind":"List","items":[` + fmt.Sprintf(slice, `"10.244.0.6","203.0.113.9","198.51.100.7"`) + "," + pod + `]}`,
	}
	for i, r := range reads {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), []byte(r), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\nn=$(cat " + filepath.Join(dir, "n") + " 2>/dev/null || echo 0)\necho $((n+1)) > " + filepath.Join(dir, "n") + "\ncat " + dir + "/$n\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := (&Client{Runner: &execx.Runner{}}).OutsideEndpoints(t.Context(), "ns")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]string{"web": {"203.0.113.9"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("OutsideEndpoints = %v, want %v: 10.244.0.5 was gone by the second read", got, want)
	}
}

func TestOutsideEndpointsBadJSON(t *testing.T) {
	c := fakeKubectl(t, "not json")
	if _, err := c.OutsideEndpoints(t.Context(), "ns"); err == nil {
		t.Error("OutsideEndpoints on bad JSON: want an error")
	}
}
