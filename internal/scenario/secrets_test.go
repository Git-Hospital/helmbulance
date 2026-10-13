package scenario

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
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

func TestNotes(t *testing.T) {
	if got := withNotes("ok", note("added", nil), ""); got != "ok" {
		t.Errorf("withNotes with nothing to say = %q, want the summary unchanged", got)
	}
	added := describe([]oracle.Change{{Ref: "a/x", Kind: oracle.Added}}, map[string][]string{"a/x": {"Deployment/web"}})
	got := withNotes("ok", note("keys added", added), note("not compared", []string{"`late`"}))
	if want := "ok; keys added: `a/x` added (read by Deployment/web); not compared: `late`"; got != want {
		t.Errorf("withNotes = %q, want %q", got, want)
	}
}

func TestSecretsInconclusive(t *testing.T) {
	notSettled := fmt.Errorf("%w after 10 reads", oracle.ErrNotSettled)
	if got := secretsInconclusive(Result{}, "after install", notSettled).Summary; !strings.HasPrefix(got, "the release's Secrets kept changing after install") {
		t.Errorf("summary for unsettled Secrets = %q", got)
	}
	if got := secretsInconclusive(Result{}, "after install", errors.New("boom")).Summary; !strings.HasPrefix(got, "could not read Secrets after install") {
		t.Errorf("summary for a read error = %q", got)
	}
}
