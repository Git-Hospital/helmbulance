package kube

import (
	"reflect"
	"testing"
)

func TestOutsideEndpoints(t *testing.T) {
	c := fakeKubectl(t, `{"kind":"List","items":[
		{"kind":"Pod","metadata":{"name":"web-1"},"status":{"podIP":"10.244.0.5","podIPs":[{"ip":"10.244.0.5"},{"ip":"fd00::5"}]}},
		{"kind":"Pod","metadata":{"name":"web-2"},"status":{"podIP":"10.244.0.6"}},
		{"kind":"Pod","metadata":{"name":"pending"},"status":{}},
		{"kind":"EndpointSlice","metadata":{"name":"web-abc","labels":{"kubernetes.io/service-name":"web"}},
			"endpoints":[{"addresses":["10.244.0.5"]},{"addresses":["10.244.0.6"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"web-v6","labels":{"kubernetes.io/service-name":"web"}},
			"endpoints":[{"addresses":["fd00::5"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"api-abc","labels":{"kubernetes.io/service-name":"api"}},
			"endpoints":[{"addresses":["10.244.0.6"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"api-extra","labels":{"kubernetes.io/service-name":"api"}},
			"endpoints":[{"addresses":["203.0.113.9"]},{"addresses":["198.51.100.1"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"names","labels":{"kubernetes.io/service-name":"names"}},
			"addressType":"FQDN","endpoints":[{"addresses":["example.com"]}]},
		{"kind":"EndpointSlice","metadata":{"name":"unlabelled"},"endpoints":[{"addresses":["203.0.113.10"]}]}
	]}`)
	got, err := c.OutsideEndpoints(t.Context(), "ns")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"api":   {"198.51.100.1", "203.0.113.9"},
		"names": {"example.com"},
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
