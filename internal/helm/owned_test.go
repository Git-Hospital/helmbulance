package helm

import (
	"reflect"
	"testing"
)

func TestSecretNames(t *testing.T) {
	manifest := `---
# Source: app/templates/secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: app-auth
type: Opaque
data:
  password: aHVudGVyMg==
---
# Source: app/templates/other-ns.yaml
apiVersion: v1
kind: Secret
metadata:
  name: elsewhere
  namespace: kube-system
---
apiVersion: v1
kind: Secret
metadata:
  name: same-ns
  namespace: hb
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
---
`
	got, err := secretNames([]byte(manifest), "hb")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"app-auth", "same-ns"}; !reflect.DeepEqual(got, want) {
		t.Errorf("secretNames = %v, want %v", got, want)
	}
}

func TestSecretNamesEmpty(t *testing.T) {
	got, err := secretNames(nil, "hb")
	if err != nil || len(got) != 0 {
		t.Errorf("secretNames(nil) = %v, %v; want nothing", got, err)
	}
}
