package scenario

import (
	"context"
	"errors"
	"maps"
	"slices"
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

// secretReader snapshots the Secrets that the release itself renders, once two reads in a row
// agree. Secrets written by a Job, an operator or the app are left out, since they change for
// reasons the upgrade did not cause; their names are kept so the report can say what was not
// compared.
type secretReader struct {
	ctx         context.Context
	env         *Env
	release, ns string
	notCompared map[string]bool
}

func newSecretReader(ctx context.Context, env *Env, release, ns string) *secretReader {
	return &secretReader{ctx: ctx, env: env, release: release, ns: ns, notCompared: map[string]bool{}}
}

func (r *secretReader) snapshot() (oracle.SecretSnapshot, error) {
	// What the release renders does not change while the namespace settles, so it is read once.
	owned, err := r.env.Helm.OwnedSecrets(r.ctx, r.release, r.ns)
	if err != nil {
		return nil, err
	}
	return oracle.Settle(r.ctx, func() (oracle.SecretSnapshot, error) {
		secrets, err := r.env.Kube.Secrets(r.ctx, r.ns)
		if err != nil {
			return nil, err
		}
		for _, s := range secrets {
			if !owned[s.Name] {
				r.notCompared[s.Name] = true
			}
		}
		return r.env.Hasher.Snapshot(ownedOnly(secrets, owned)), nil
	}, settleInterval, settleReads)
}

// notComparedNote names the Secrets that were in the namespace but not rendered by the release.
func (r *secretReader) notComparedNote() string {
	names := slices.Sorted(maps.Keys(r.notCompared))
	for i, n := range names {
		names[i] = "`" + n + "`"
	}
	return note("not compared, since the release does not render them", names)
}

// secretsInconclusive says whether reading the Secrets failed or they never stopped changing.
func secretsInconclusive(res Result, after string, err error) Result {
	if errors.Is(err, oracle.ErrNotSettled) {
		return inconclusive(res, "the release's Secrets kept changing "+after, err)
	}
	return inconclusive(res, "could not read Secrets "+after, err)
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

// note renders one item of a summary's notes, or nothing when there are no items.
func note(label string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	return label + ": " + strings.Join(items, ", ")
}

// withNotes appends the non-empty notes to a summary. Notes report what is not a finding.
func withNotes(summary string, notes ...string) string {
	for _, n := range notes {
		if n != "" {
			summary += "; " + n
		}
	}
	return summary
}
