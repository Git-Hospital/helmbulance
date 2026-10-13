// Package oracle decides whether something broke, from state Kubernetes can observe.
package oracle

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/egezamb/helmbulance/internal/kube"
)

// SecretSnapshot holds a keyed hash of every Secret value at one point in time. The hash key is
// random per run and never stored, so the snapshot cannot be used to guess weak passwords; it only
// tells whether a value changed.
type SecretSnapshot map[string][32]byte // "secret/key" -> HMAC

// Hasher produces SecretSnapshots with one per-run key.
type Hasher struct{ key []byte }

// NewHasher returns a Hasher with a fresh random key.
func NewHasher() (*Hasher, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &Hasher{key: key}, nil
}

// Snapshot hashes every key of every Secret.
func (h *Hasher) Snapshot(secrets []kube.Secret) SecretSnapshot {
	snap := SecretSnapshot{}
	for _, s := range secrets {
		for k, v := range s.Data {
			mac := hmac.New(sha256.New, h.key)
			mac.Write(v)
			var sum [32]byte
			copy(sum[:], mac.Sum(nil))
			snap[s.Name+"/"+k] = sum
		}
	}
	return snap
}

// Change is one Secret key that differs between two snapshots.
type Change struct {
	Ref  string `json:"ref"` // "secret/key"
	Kind string `json:"kind"`
}

// Change kinds.
const (
	Changed = "changed"
	Removed = "removed"
	Added   = "added"
)

// Diff lists the keys that changed, disappeared or appeared, sorted by reference.
func Diff(before, after SecretSnapshot) []Change {
	var changes []Change
	for ref, b := range before {
		a, ok := after[ref]
		switch {
		case !ok:
			changes = append(changes, Change{Ref: ref, Kind: Removed})
		case a != b:
			changes = append(changes, Change{Ref: ref, Kind: Changed})
		}
	}
	for ref := range after {
		if _, ok := before[ref]; !ok {
			changes = append(changes, Change{Ref: ref, Kind: Added})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Ref < changes[j].Ref })
	return changes
}

// ErrNotSettled means the Secrets were still changing when Settle gave up.
var ErrNotSettled = errors.New("Secrets were still changing")

// Settle reads until two reads in a row agree, waiting interval between them, and returns that
// snapshot. A Secret written by a Job or the app just after an install would otherwise land
// between two snapshots and look like a change caused by the upgrade.
func Settle(ctx context.Context, read func() (SecretSnapshot, error), interval time.Duration, reads int) (SecretSnapshot, error) {
	prev, err := read()
	if err != nil {
		return nil, err
	}
	for i := 1; i < reads; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		cur, err := read()
		if err != nil {
			return nil, err
		}
		if maps.Equal(prev, cur) {
			return cur, nil
		}
		prev = cur
	}
	return nil, fmt.Errorf("%w after %d reads, %s apart", ErrNotSettled, reads, interval)
}
