package kube

import (
	"reflect"
	"testing"
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
		{"kind":"EndpointSlice","metadata":{"name":"unlabelled"},"endpoints":[{"addresses":["203.0.113.10"]}]}
	]}`)
	got, err := c.OutsideEndpoints(t.Context(), "ns")
	if err != nil {
		t.Fatal(err)
	}
	// A slice the control plane did not write is the chart's whatever its addresses: the node's IP
	// (a hostNetwork pod's) and a pod IP mirrored from the chart's Endpoints object count too.
	want := map[string][]string{
		"api":    {"198.51.100.1", "203.0.113.9"},
		"node":   {"172.18.0.2"},
		"legacy": {"10.244.0.5"},
		"names":  {"example.com"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OutsideEndpoints = %v, want %v", got, want)
	}
}

func TestOutsideEndpointsBadJSON(t *testing.T) {
	c := fakeKubectl(t, "not json")
	if _, err := c.OutsideEndpoints(t.Context(), "ns"); err == nil {
		t.Error("OutsideEndpoints on bad JSON: want an error")
	}
}
