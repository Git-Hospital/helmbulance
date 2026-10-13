package scenario

import (
	"testing"

	"github.com/egezamb/helmbulance/internal/oracle"
)

func TestChangeSeverity(t *testing.T) {
	consumers := map[string][]string{
		"db/password":   {"StatefulSet/db"},
		"app/*":         {"Deployment/web"},
		"webhook-tls/*": {"Deployment/webhook"},
	}
	stateful := map[string]bool{"StatefulSet/db": true}
	for ref, want := range map[string]string{
		"db/password":         "high",   // a credential the database keeps
		"app/jwtSecret":       "medium", // a credential, but no persistent data reads it
		"db/username":         "medium", // not a credential
		"webhook-tls/tls.crt": "low",    // TLS material
		"orphan/password":     "medium", // read by nothing we know of
	} {
		if got := changeSeverity(oracle.Change{Ref: ref, Kind: oracle.Changed}, consumers, stateful); got != want {
			t.Errorf("changeSeverity(%s) = %s, want %s", ref, got, want)
		}
	}
}

func TestWorstSeverityAndOnlyTLS(t *testing.T) {
	consumers := map[string][]string{"db/*": {"StatefulSet/db"}}
	stateful := map[string]bool{"StatefulSet/db": true}
	tls := []oracle.Change{{Ref: "ca/tls.crt", Kind: oracle.Changed}, {Ref: "ca/tls.key", Kind: oracle.Changed}}
	if got := worstSeverity(tls, consumers, stateful); got != "low" || !onlyTLS(tls) {
		t.Errorf("TLS only: worst = %s, onlyTLS = %v; want low, true", got, onlyTLS(tls))
	}
	mixed := append(tls, oracle.Change{Ref: "db/password", Kind: oracle.Removed})
	if got := worstSeverity(mixed, consumers, stateful); got != "high" || onlyTLS(mixed) {
		t.Errorf("mixed: worst = %s, onlyTLS = %v; want high, false", got, onlyTLS(mixed))
	}
}
