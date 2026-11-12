package scenario

import (
	"slices"
	"strings"

	"github.com/Git-Hospital/helmbulance/internal/oracle"
)

// readers lists the workloads that read the key a change refers to, directly or through the whole
// Secret (envFrom, a volume).
func readers(c oracle.Change, consumers map[string][]string) []string {
	secret, _, _ := strings.Cut(c.Ref, "/")
	return append(append([]string{}, consumers[c.Ref]...), consumers[secret+"/*"]...)
}

// keyClass is the class of the key a change refers to.
func keyClass(c oracle.Change) string {
	_, key, _ := strings.Cut(c.Ref, "/")
	return oracle.KeyClass(key)
}

// changeSeverity rates one changed or removed key (D5): high for a credential that a workload with
// persistent data reads, since that data was likely written with the old value; low for TLS
// material, which charts often rotate on purpose; medium otherwise.
func changeSeverity(c oracle.Change, consumers map[string][]string, stateful map[string]bool, statefulKnown bool) string {
	switch keyClass(c) {
	case oracle.ClassTLS:
		return "low"
	case oracle.ClassCredential:
		for _, w := range readers(c, consumers) {
			if stateful[w] {
				return "high"
			}
		}
		// Without the list of stateful workloads a persistent reader cannot be ruled out, so a
		// credential is kept high rather than silently lowered to medium (review item 4).
		if !statefulKnown {
			return "high"
		}
	}
	return "medium"
}

var severityRank = map[string]int{"low": 1, "medium": 2, "high": 3}

// worstSeverity is the highest severity among the changes, so one finding carries the worst key.
func worstSeverity(changes []oracle.Change, consumers map[string][]string, stateful map[string]bool, statefulKnown bool) string {
	worst := "low"
	for _, c := range changes {
		if s := changeSeverity(c, consumers, stateful, statefulKnown); severityRank[s] > severityRank[worst] {
			worst = s
		}
	}
	return worst
}

// unknownStatefulNote warns that stateful readers could not be listed, so credential severity was
// kept high instead of being lowered (review item 4). It is empty when the listing succeeded or no
// credential changed.
func unknownStatefulNote(statefulKnown bool, changeSets ...[]oracle.Change) string {
	if statefulKnown {
		return ""
	}
	for _, set := range changeSets {
		for _, c := range set {
			if keyClass(c) == oracle.ClassCredential {
				return "could not list stateful workloads, so credential severity was kept high, not lowered"
			}
		}
	}
	return ""
}

// onlyTLS reports whether every change is TLS material, so the finding can say so.
func onlyTLS(changes []oracle.Change) bool {
	for _, c := range changes {
		if keyClass(c) != oracle.ClassTLS {
			return false
		}
	}
	return len(changes) > 0
}

// switchedTo separates the added credential keys that a workload with persistent data moved to: it
// reads the key after an upgrade, did not before, and stopped reading a key, or a whole Secret, that
// it read then. Its data was set up with the old value: a dropped --set existingSecret that makes
// the chart generate a password, or a new chart version that points the workload at a new key. A
// workload the upgrade added, or one that reads the new key next to the old one, moved nothing, so
// those keys stay notes, as does a new key alone. Without the readers from before the upgrade
// (before is nil) no key is taken for a switch (#54).
func switchedTo(added []oracle.Change, before, after map[string][]string, stateful map[string]bool) (switched, rest []oracle.Change) {
	for _, c := range added {
		if before != nil && keyClass(c) == oracle.ClassCredential && newStatefulReader(c, before, after, stateful) {
			switched = append(switched, c)
		} else {
			rest = append(rest, c)
		}
	}
	return switched, rest
}

func newStatefulReader(c oracle.Change, before, after map[string][]string, stateful map[string]bool) bool {
	old := readers(c, before)
	for _, w := range readers(c, after) {
		if stateful[w] && !slices.Contains(old, w) && stoppedReading(w, before, after) {
			return true
		}
	}
	return false
}

// stoppedReading reports whether workload w read a key, or a whole Secret, before the upgrade that
// it does not read after it. A workload that read nothing before did not exist, or had no Secret.
func stoppedReading(w string, before, after map[string][]string) bool {
	for ref, ws := range before {
		if slices.Contains(ws, w) && !slices.Contains(after[ref], w) {
			return true
		}
	}
	return false
}
