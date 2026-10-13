package oracle

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"

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

// reads returns a read function that hands out the given snapshots in order.
func reads(snaps ...SecretSnapshot) (func() (SecretSnapshot, error), *int) {
	n := 0
	return func() (SecretSnapshot, error) {
		s := snaps[min(n, len(snaps)-1)]
		n++
		return s, nil
	}, &n
}

func TestSettleWaitsForALateSecret(t *testing.T) {
	h, _ := NewHasher()
	installed := h.Snapshot([]kube.Secret{{Name: "app", Data: map[string][]byte{"pw": []byte("a")}}})
	late := h.Snapshot([]kube.Secret{
		{Name: "app", Data: map[string][]byte{"pw": []byte("a")}},
		{Name: "late", Data: map[string][]byte{"token": []byte("t")}},
	})
	read, n := reads(installed, late, late)
	got, err := Settle(context.Background(), read, time.Millisecond, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(got, late) || *n != 3 {
		t.Errorf("Settle returned after %d reads with %d keys; want the late snapshot after 3", *n, len(got))
	}
}

func TestSettleGivesUp(t *testing.T) {
	h, _ := NewHasher()
	var snaps []SecretSnapshot
	for _, v := range []string{"1", "2", "3"} {
		snaps = append(snaps, h.Snapshot([]kube.Secret{{Name: "app", Data: map[string][]byte{"k": []byte(v)}}}))
	}
	read, _ := reads(snaps...)
	if _, err := Settle(context.Background(), read, time.Millisecond, 3); !errors.Is(err, ErrNotSettled) {
		t.Errorf("Settle on values that keep changing: err = %v, want ErrNotSettled", err)
	}
}
