package scenario

import (
	"reflect"
	"testing"

	"github.com/egezamb/helmbulance/internal/kube"
	"github.com/egezamb/helmbulance/internal/oracle"
)

func TestOwnedOnly(t *testing.T) {
	secrets := []kube.Secret{{Name: "app-auth"}, {Name: "written-by-job"}, {Name: "operator-cert"}}
	got := ownedOnly(secrets, map[string]bool{"app-auth": true, "not-installed-yet": true})
	if len(got) != 1 || got[0].Name != "app-auth" {
		t.Errorf("ownedOnly = %v, want only app-auth", got)
	}
}

func TestSplitAdded(t *testing.T) {
	changes := []oracle.Change{
		{Ref: "app/jwt", Kind: oracle.Changed},
		{Ref: "app/new", Kind: oracle.Added},
		{Ref: "app/old", Kind: oracle.Removed},
	}
	lost, added := splitAdded(changes)
	if want := []oracle.Change{changes[0], changes[2]}; !reflect.DeepEqual(lost, want) {
		t.Errorf("lost = %v, want %v", lost, want)
	}
	if want := []oracle.Change{changes[1]}; !reflect.DeepEqual(added, want) {
		t.Errorf("added = %v, want %v", added, want)
	}
}

func TestNoteAdded(t *testing.T) {
	if got := noteAdded("ok", nil); got != "ok" {
		t.Errorf("noteAdded with nothing added = %q, want the summary unchanged", got)
	}
	got := noteAdded("ok", []oracle.Change{{Ref: "a/x", Kind: oracle.Added}, {Ref: "b/y", Kind: oracle.Added}})
	if want := "ok; keys added by an upgrade (not a finding): `a/x`, `b/y`"; got != want {
		t.Errorf("noteAdded = %q, want %q", got, want)
	}
}
