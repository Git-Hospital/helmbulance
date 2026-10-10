package oracle

import (
	"reflect"
	"testing"

	"github.com/egezamb/helmbulance/internal/kube"
)

func TestDiff(t *testing.T) {
	h, err := NewHasher()
	if err != nil {
		t.Fatal(err)
	}
	before := h.Snapshot([]kube.Secret{
		{Name: "app", Data: map[string][]byte{"jwt": []byte("a"), "storage-key": []byte("k"), "same": []byte("x")}},
	})
	after := h.Snapshot([]kube.Secret{
		{Name: "app", Data: map[string][]byte{"jwt": []byte("b"), "same": []byte("x"), "new": []byte("n")}},
	})
	want := []Change{
		{Ref: "app/jwt", Kind: Changed},
		{Ref: "app/new", Kind: Added},
		{Ref: "app/storage-key", Kind: Removed},
	}
	if got := Diff(before, after); !reflect.DeepEqual(got, want) {
		t.Errorf("Diff = %v, want %v", got, want)
	}
}

func TestHasherKeysDiffer(t *testing.T) {
	h1, _ := NewHasher()
	h2, _ := NewHasher()
	s := []kube.Secret{{Name: "s", Data: map[string][]byte{"k": []byte("password")}}}
	if h1.Snapshot(s)["s/k"] == h2.Snapshot(s)["s/k"] {
		t.Error("two runs produced the same hash for the same value; the per-run key is not used")
	}
}
