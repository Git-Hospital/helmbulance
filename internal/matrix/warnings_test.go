package matrix

import (
	"reflect"
	"strings"
	"testing"
)

func TestAPIWarnings(t *testing.T) {
	stderr := strings.Join([]string{
		`W1011 12:00:00.000000 12345 warnings.go:70] policy/v1beta1 PodDisruptionBudget is deprecated in v1.21+, unavailable in v1.25+`,
		`W1011 12:00:01.000000 12345 warnings.go:70] "policy/v1beta1 PodDisruptionBudget is deprecated in v1.21+, unavailable in v1.25+"`,
		// The format kubectl and a client-go warning writer use: before, this gave a false PASS (#12).
		`Warning: spec.template.spec.containers[0].env[1]: hides previous definition of "MODE"`,
		// Helm's own notice about the kubeconfig is not an API server warning.
		`WARNING: Kubernetes configuration file is group-readable. This is insecure. Location: /kc`,
		`NAME: hb-matrix`,
	}, "\n")
	want := []string{
		"API warning: policy/v1beta1 PodDisruptionBudget is deprecated in v1.21+, unavailable in v1.25+",
		`API warning: spec.template.spec.containers[0].env[1]: hides previous definition of "MODE"`,
	}
	if got := apiWarnings(stderr); !reflect.DeepEqual(got, want) {
		t.Errorf("apiWarnings =\n%q\nwant\n%q", got, want)
	}
}

// Only a deprecation is HB-K8S-003 with its removal impact; another warning, such as a duplicated
// environment variable, is HB-K8S-004, about the manifest (#12).
func TestWarningFindings(t *testing.T) {
	deprecated := "API warning: policy/v1beta1 PodDisruptionBudget is deprecated in v1.21+, unavailable in v1.25+"
	removed := "API warning: batch/v1beta1 CronJob is removed in v1.25+"
	dup := `API warning: spec.template.spec.containers[0].env[1]: hides previous definition of "MODE"`
	ids := func(warnings []string) map[string][]string {
		got := map[string][]string{}
		for _, f := range warningFindings("1.37", warnings, nil) {
			got[f.ID] = f.Evidence
		}
		return got
	}
	if got := ids([]string{deprecated, removed, dup}); !reflect.DeepEqual(got, map[string][]string{
		"HB-K8S-003": {deprecated, removed},
		"HB-K8S-004": {dup},
	}) {
		t.Errorf("findings = %v", got)
	}
	if got := ids([]string{dup}); len(got) != 1 || got["HB-K8S-004"] == nil {
		t.Errorf("a duplicate env alone = %v, want only HB-K8S-004", got)
	}
	if got := ids(nil); len(got) != 0 {
		t.Errorf("no warnings = %v, want no finding", got)
	}
}
