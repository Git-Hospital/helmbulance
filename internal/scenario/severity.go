package scenario

import (
	"strings"

	"github.com/egezamb/helmbulance/internal/oracle"
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
func changeSeverity(c oracle.Change, consumers map[string][]string, stateful map[string]bool) string {
	switch keyClass(c) {
	case oracle.ClassTLS:
		return "low"
	case oracle.ClassCredential:
		for _, w := range readers(c, consumers) {
			if stateful[w] {
				return "high"
			}
		}
	}
	return "medium"
}

var severityRank = map[string]int{"low": 1, "medium": 2, "high": 3}

// worstSeverity is the highest severity among the changes, so one finding carries the worst key.
func worstSeverity(changes []oracle.Change, consumers map[string][]string, stateful map[string]bool) string {
	worst := "low"
	for _, c := range changes {
		if s := changeSeverity(c, consumers, stateful); severityRank[s] > severityRank[worst] {
			worst = s
		}
	}
	return worst
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
