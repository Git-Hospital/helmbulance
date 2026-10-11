package helm

import "testing"

func TestIsLocal(t *testing.T) {
	cases := map[string]bool{
		"../../testdata/charts/keeps-secret": true,
		"oci://registry.example/charts/app":  false,
		"https://example.com/app-1.0.0.tgz":  false,
		"bitnami/postgresql":                 false,
		"":                                   false,
	}
	for ref, want := range cases {
		if got := IsLocal(ref); got != want {
			t.Errorf("IsLocal(%q) = %v, want %v", ref, got, want)
		}
	}
}
