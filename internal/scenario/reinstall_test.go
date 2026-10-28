package scenario

import (
	"errors"
	"strings"
	"testing"

	"github.com/egezamb/helmbulance/internal/oracle"
)

func TestReinstallFindings(t *testing.T) {
	consumers := map[string][]string{"hb-reinstall-db/*": {"StatefulSet/hb-reinstall-db"}}
	stateful := map[string]bool{"StatefulSet/hb-reinstall-db": true}
	pw := []oracle.Change{{Ref: "hb-reinstall-db/password", Kind: oracle.Changed}}
	kept := []volume{{claim: "data-hb-reinstall-db-0", state: "kept"}}
	users := map[string][]string{"data-hb-reinstall-db-0": {"StatefulSet/hb-reinstall-db"}}

	got, _, _ := reinstallFindings(kept, pw, consumers, stateful, true, users, nil)
	if len(got) != 1 || got[0].ID != "HB-RI-001" || got[0].Severity != "high" {
		t.Fatalf("kept data and a new password: findings = %+v, want one high HB-RI-001", got)
	}
	if ev := strings.Join(got[0].Evidence, " "); !strings.Contains(ev, "data-hb-reinstall-db-0") || !strings.Contains(ev, "password` changed") {
		t.Errorf("evidence %q does not name the volume and the key", ev)
	}

	// New credentials with no data left behind are a fresh install: nothing to report.
	deleted := []volume{{claim: "data", state: "deleted"}}
	if got, _, _ := reinstallFindings(deleted, pw, consumers, stateful, true, users, nil); len(got) != 0 {
		t.Errorf("deleted volume and a new password: findings = %+v, want none", got)
	}
	// Kept data and kept credentials: nothing to report.
	if got, _, _ := reinstallFindings(kept, nil, consumers, stateful, true, users, nil); len(got) != 0 {
		t.Errorf("kept data and kept Secrets: findings = %+v, want none", got)
	}
	// A kept claim whose sentinel is gone.
	emptied := []volume{{claim: "data", state: "missing"}, {claim: "logs", state: "replaced"}}
	if got, _, _ := reinstallFindings(emptied, nil, consumers, stateful, true, users, nil); len(got) != 1 || got[0].ID != "HB-RI-002" {
		t.Errorf("emptied volumes: findings = %+v, want HB-RI-002", got)
	}
}

// A key that changed but is read only by a workload without a kept volume is not tied to the data.
func TestReinstallFindingsIgnoreUnrelatedKeys(t *testing.T) {
	consumers := map[string][]string{"webhook-tls/*": {"Deployment/webhook"}}
	kept := []volume{{claim: "data-redis-0", state: "kept"}}
	users := map[string][]string{"data-redis-0": {"StatefulSet/redis"}}
	cert := []oracle.Change{{Ref: "webhook-tls/tls.crt", Kind: oracle.Changed}}
	got, unrelated, _ := reinstallFindings(kept, cert, consumers, nil, true, users, nil)
	if len(got) != 0 || len(unrelated) != 1 {
		t.Errorf("findings = %+v, unrelated = %v; want no finding and the certificate set apart", got, unrelated)
	}
}

// A certificate the kept volume's own workload reads is a reissue, not a credential mismatch: no
// finding, and it is set apart as a certificate rather than reported (review item 3).
func TestReinstallFindingsCertReadByKeeper(t *testing.T) {
	consumers := map[string][]string{"db-tls/*": {"StatefulSet/db"}}
	kept := []volume{{claim: "data-db-0", state: "kept"}}
	users := map[string][]string{"data-db-0": {"StatefulSet/db"}}
	stateful := map[string]bool{"StatefulSet/db": true}
	cert := []oracle.Change{{Ref: "db-tls/tls.crt", Kind: oracle.Changed}}
	got, unrelated, certs := reinstallFindings(kept, cert, consumers, stateful, true, users, nil)
	if len(got) != 0 {
		t.Errorf("findings = %+v, want none for a reissued certificate a keeper reads", got)
	}
	if len(certs) != 1 || certs[0].Ref != "db-tls/tls.crt" {
		t.Errorf("certs = %v, want the certificate set apart", certs)
	}
	if len(unrelated) != 0 {
		t.Errorf("unrelated = %v, want empty since the keeper does read it", unrelated)
	}
}

func TestReinstallPassSummary(t *testing.T) {
	for _, c := range []struct {
		volumes []volume
		changes []oracle.Change
		want    string
	}{
		{nil, nil, "Secrets came back unchanged after uninstall and install; the release has no volumes"},
		{nil, []oracle.Change{{Ref: "s/k", Kind: oracle.Changed}}, "the release has no volumes, so new Secrets after a reinstall lose nothing"},
		{[]volume{{claim: "a", state: "kept"}}, nil, "Secrets came back unchanged after uninstall and install; 1 volume(s) kept their data"},
		{[]volume{{claim: "a", state: "recreated"}}, []oracle.Change{{Ref: "s/k", Kind: oracle.Changed}},
			"no volume kept its data, so new Secrets are expected; deleted with the release (depends on the StorageClass reclaim policy): `a`"},
	} {
		if got := reinstallPassSummary(c.volumes, c.changes); got != c.want {
			t.Errorf("reinstallPassSummary = %q, want %q", got, c.want)
		}
	}
}

func TestReinstallInstallFailed(t *testing.T) {
	for _, c := range []struct {
		err, verdict string
	}{
		{"Error: INSTALLATION FAILED: execution error at (demo/templates/secret.yaml:3:4): set auth.existingSecret to reinstall over kept data", Pass},
		{"Error: INSTALLATION FAILED: context deadline exceeded", Inconclusive},
		{"helm install hb-reinstall ./chart: signal: killed", Inconclusive},
		{"Failed to pull image: toomanyrequests: rate limit", Inconclusive},
		{`Error: INSTALLATION FAILED: Internal error occurred: failed calling webhook "v.example.io": context deadline exceeded`, Fail},
		{`Error: INSTALLATION FAILED: Secret "hb-reinstall-db" exists and cannot be imported into the current release: invalid ownership metadata`, Fail},
	} {
		res := Reinstall{}.installFailed(t.Context(), Result{}, errors.New(c.err), nil)
		if res.Verdict != c.verdict {
			t.Errorf("%q: verdict = %s (%s), want %s", c.err, res.Verdict, res.Summary, c.verdict)
		}
		if c.verdict == Fail && (len(res.Findings) != 1 || res.Findings[0].ID != "HB-RI-003") {
			t.Errorf("%q: findings = %+v, want HB-RI-003", c.err, res.Findings)
		}
	}
}

func TestSentinelRepro(t *testing.T) {
	got := strings.Join(sentinelRepro("write", "echo marker > /pvc/marker"), "\n")
	for _, want := range []string{"# write", "kubectl -n hb-reinstall run marker", `"claimName":"CLAIM"`, "echo marker > /pvc/marker"} {
		if !strings.Contains(got, want) {
			t.Errorf("sentinelRepro does not contain %q:\n%s", want, got)
		}
	}
}

// A Secret the uninstall left with keep that the reinstall rewrote is HB-RI-004, even with no
// volume: what it was set up for, such as an external database, is outside the release.
func TestKeptSecretReplaced(t *testing.T) {
	survivors := map[string]bool{"hb-reinstall-db-secret": true}
	consumers := map[string][]string{"hb-reinstall-db-secret/*": {"StatefulSet/hb-reinstall-filer"}}
	changes := []oracle.Change{
		{Ref: "hb-reinstall-db-secret/password", Kind: oracle.Changed},
		{Ref: "hb-reinstall-db-secret/user", Kind: oracle.Changed},
		{Ref: "hb-reinstall-app/token", Kind: oracle.Changed}, // deleted with the release: a fresh one is fine
	}
	got, ok := keptSecretReplaced(changes, survivors, consumers, map[string]bool{}, true, nil)
	if !ok || got.ID != "HB-RI-004" || got.Severity != "medium" {
		t.Fatalf("kept db secret rewritten: finding = %+v, %v; want a medium HB-RI-004", got, ok)
	}
	ev := strings.Join(got.Evidence, " ")
	for _, want := range []string{"`hb-reinstall-db-secret`", "password` changed", "user` changed", "StatefulSet/hb-reinstall-filer"} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence %q does not contain %q", ev, want)
		}
	}
	if strings.Contains(ev, "hb-reinstall-app") {
		t.Errorf("evidence %q names a Secret the uninstall deleted", ev)
	}

	// A credential a workload with persistent data reads keeps its high rating.
	stateful := map[string]bool{"StatefulSet/hb-reinstall-filer": true}
	if got, _ := keptSecretReplaced(changes[:1], survivors, consumers, stateful, true, nil); got.Severity != "high" {
		t.Errorf("read by a stateful workload: severity = %s, want high", got.Severity)
	}
	// A reissued certificate and a deleted Secret are not this finding; nor is anything when the
	// survivors could not be listed.
	cert := []oracle.Change{{Ref: "hb-reinstall-db-secret/tls.crt", Kind: oracle.Changed}}
	if _, ok := keptSecretReplaced(cert, survivors, consumers, stateful, true, nil); ok {
		t.Error("a reissued certificate in a kept Secret is reported")
	}
	if _, ok := keptSecretReplaced(changes[2:], survivors, consumers, stateful, true, nil); ok {
		t.Error("a Secret the uninstall deleted is reported")
	}
	if _, ok := keptSecretReplaced(changes, nil, consumers, stateful, true, nil); ok {
		t.Error("a finding without knowing what survived the uninstall")
	}
}

// A first install applied but never ready keeps its findings, with the timeout as evidence; with
// none, the run proves nothing.
func TestUnreadyFirstInstall(t *testing.T) {
	wait := errors.New("Error: INSTALLATION FAILED: resource StatefulSet/hb-reinstall/filer not ready. status: Failed, message: 1 pods have failed")
	found := Result{Verdict: Fail, Findings: []Finding{{ID: "HB-RI-004", Evidence: []string{"kept"}}}}
	got := unreadyFirstInstall(found, wait)
	if got.Verdict != Fail || len(got.Findings[0].Evidence) != 2 || !strings.Contains(got.Findings[0].Evidence[0], "first `helm install --wait`") {
		t.Errorf("with a finding: %+v, want FAIL with the timeout first in the evidence", got)
	}
	if got := unreadyFirstInstall(Result{Verdict: Pass, Summary: "ok"}, wait); got.Verdict != Inconclusive || !strings.HasSuffix(got.Summary, "; seen all the same: ok") {
		t.Errorf("no finding: %s %q, want INCONCLUSIVE that keeps what was seen", got.Verdict, got.Summary)
	}
	if got := unreadyFirstInstall(Result{Verdict: Inconclusive, Summary: "x"}, wait); got.Summary != "x" {
		t.Errorf("an INCONCLUSIVE result was rewritten: %q", got.Summary)
	}
	if got := unreadyFirstInstall(Result{Verdict: Pass, Summary: "ok"}, nil); got.Verdict != Pass {
		t.Errorf("a ready install: verdict = %s, want PASS", got.Verdict)
	}
}

// Without a ready first install, HB-RI-001 goes and hands back the changes it covered; HB-RI-002,
// the lost sentinel, stays.
func TestUnobservedCredentials(t *testing.T) {
	pw := oracle.Change{Ref: "db/password", Kind: oracle.Changed}
	other := oracle.Change{Ref: "app/token", Kind: oracle.Changed}
	cert := oracle.Change{Ref: "db/tls.crt", Kind: oracle.Changed}
	findings := []Finding{{ID: "HB-RI-001"}, {ID: "HB-RI-002"}}
	rest, unobserved := unobservedCredentials(findings, []oracle.Change{pw, other, cert}, []oracle.Change{other}, []oracle.Change{cert})
	if len(rest) != 1 || rest[0].ID != "HB-RI-002" {
		t.Errorf("findings = %+v, want only HB-RI-002", rest)
	}
	if len(unobserved) != 1 || unobserved[0] != pw {
		t.Errorf("unobserved = %+v, want only %s", unobserved, pw.Ref)
	}
	if findings[0].ID != "HB-RI-001" {
		t.Error("the caller's findings were changed")
	}
	if rest, unobserved := unobservedCredentials(findings[1:], []oracle.Change{other}, []oracle.Change{other}, nil); len(rest) != 1 || unobserved != nil {
		t.Errorf("without HB-RI-001: %+v, %+v; want the findings unchanged and nothing back", rest, unobserved)
	}
}
