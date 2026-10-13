package scenario

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egezamb/helmbulance/internal/kube"
	"github.com/egezamb/helmbulance/internal/oracle"
)

// settleInterval and settleReads bound how long a Secret snapshot waits for the namespace to stop
// changing: up to about half a minute after helm returns.
const (
	settleInterval = 3 * time.Second
	settleReads    = 10
)

// releaseSecrets snapshots the Secrets that the release itself renders, once two reads in a row
// agree. Secrets written by a Job, an operator or the app are left out: they change for reasons the
// upgrade did not cause.
func releaseSecrets(ctx context.Context, env *Env, release, ns string) (oracle.SecretSnapshot, error) {
	return oracle.Settle(ctx, func() (oracle.SecretSnapshot, error) {
		owned, err := env.Helm.OwnedSecrets(ctx, release, ns)
		if err != nil {
			return nil, err
		}
		secrets, err := env.Kube.Secrets(ctx, ns)
		if err != nil {
			return nil, err
		}
		return env.Hasher.Snapshot(ownedOnly(secrets, owned)), nil
	}, settleInterval, settleReads)
}

func ownedOnly(secrets []kube.Secret, owned map[string]bool) []kube.Secret {
	var out []kube.Secret
	for _, s := range secrets {
		if owned[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

// splitAdded separates keys that appeared from keys that changed or went away. A new key breaks
// nothing that already uses the Secret, so it is not a finding.
func splitAdded(changes []oracle.Change) (lost, added []oracle.Change) {
	for _, c := range changes {
		if c.Kind == oracle.Added {
			added = append(added, c)
		} else {
			lost = append(lost, c)
		}
	}
	return lost, added
}

// noteAdded appends the added keys to a summary, so they are reported without being a finding.
func noteAdded(summary string, added []oracle.Change) string {
	if len(added) == 0 {
		return summary
	}
	refs := make([]string, len(added))
	for i, c := range added {
		refs[i] = "`" + c.Ref + "`"
	}
	return fmt.Sprintf("%s; keys added by an upgrade (not a finding): %s", summary, strings.Join(refs, ", "))
}
