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
	got := secretNames([]byte(manifest), "hb")
	if want := []string{"app-auth", "same-ns"}; !reflect.DeepEqual(got, want) {
		t.Errorf("secretNames = %v, want %v", got, want)
	}
}

func TestSecretNamesEmpty(t *testing.T) {
	if got := secretNames(nil, "hb"); len(got) != 0 {
		t.Errorf("secretNames(nil) = %v; want nothing", got)
	}
}

func TestSecretNamesSkipsBadDocumentsAndReadsLists(t *testing.T) {
	manifest := `---
apiVersion: v1
kind: Secret
metadata:
  name: broken
metadata:
  name: repeated-key
---
apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: in-a-list
---
apiVersion: v1
kind: Secret
metadata:
  name: after-the-bad-one
`
	got := secretNames([]byte(manifest), "hb")
	if want := []string{"in-a-list", "after-the-bad-one"}; !reflect.DeepEqual(got, want) {
		t.Errorf("secretNames = %v, want %v", got, want)
	}
}
