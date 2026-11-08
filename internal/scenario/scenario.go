// Package scenario defines what a scenario is and what it reports.
package scenario

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Git-Hospital/helmbulance/internal/execx"
	"github.com/Git-Hospital/helmbulance/internal/helm"
	"github.com/Git-Hospital/helmbulance/internal/kube"
	"github.com/Git-Hospital/helmbulance/internal/oracle"
)

// Verdicts. A scenario that cannot finish (install fails, timeout) is Inconclusive, never Fail:
// a finding has to be reproduced, not inferred from a broken run.
const (
	Pass         = "PASS"
	Fail         = "FAIL"
	Inconclusive = "INCONCLUSIVE"
	// Skipped: the chart has nothing this scenario tests (no HTTP Service, say). Not a failure.
	Skipped = "SKIPPED"
)

// Env is what a scenario gets to work with.
type Env struct {
	Runner *execx.Runner
	Helm   *helm.Client
	Kube   *kube.Client
	Hasher *oracle.Hasher
	Chart  string
	Values helm.Values

	// From is the release the chart-upgrade scenario starts from: a chart reference (repo/chart,
	// oci://… or a path) and a version. Empty From means "the same chart reference as Chart".
	From        string
	FromVersion string

	// TrafficTarget ("service:port") and TrafficPath are where zero-downtime sends its requests.
	// Empty picks the Service port that looks most like HTTP, and "/".
	TrafficTarget string
	TrafficPath   string

	// ExpectedRotation lists Secret keys ("secret/key", globs allowed) that the chart changes on
	// purpose, such as a webhook certificate it reissues. They are named, not reported.
	ExpectedRotation []string
	Log              io.Writer
}

// Finding is one reproduced problem.
type Finding struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Severity  string   `json:"severity"`
	Evidence  []string `json:"evidence"`
	Impact    string   `json:"impact"`
	Fix       string   `json:"fix"`
	Reproduce []string `json:"reproduce"`
}

// Result is the outcome of one scenario.
type Result struct {
	Scenario string    `json:"scenario"`
	Verdict  string    `json:"verdict"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings,omitempty"`
	// HealthUnchecked marks a result whose workloads were never seen settle, such as an install
	// whose --wait timed out with no crash: its findings say nothing about whether the chart runs.
	// matrix reads it across versions; it is not part of the report.
	HealthUnchecked bool `json:"-"`
}

// Scenario is one day-two situation tested against a live release.
type Scenario interface {
	Name() string
	Description() string
	Run(ctx context.Context, env *Env) Result
}

// cleanupTimeout bounds a scenario's cleanup, which runs even when the run was cancelled.
const cleanupTimeout = 10 * time.Minute

// cleanupContext keeps cleanup running after ctx is cancelled (a deadline or Ctrl-C), so the
// release and namespace do not outlive the run and block the next one on a reused cluster.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}

// startClean removes what an earlier run left under these fixed names, when it was killed before
// its cleanup (kill -9, a second Ctrl-C), so the next run on a reused --cluster starts from nothing
// (#11). The uninstall comes first for the release's cluster-scoped objects, and fails harmlessly
// when there is no release; deleting the namespace takes the rest, Helm's release record included.
// --cluster only accepts a sandbox Helmbulance created, so these names are its own. Last go the
// cluster-scoped objects any scenario's release kept, since the run that left them may have been
// killed before its own cleanup (#52).
func startClean(ctx context.Context, env *Env, release, ns string) error {
	_ = env.Helm.Uninstall(ctx, release, ns)
	if err := env.Kube.DeleteNamespace(ctx, ns); err != nil {
		return err
	}
	return deleteKept(ctx, env, func(ns string) bool { return strings.HasPrefix(ns, "hb-") })
}

// cleanUp removes the scenario's release and namespace, and the cluster-scoped objects the release
// kept with helm.sh/resource-policy: keep, which outlive both: the next scenario installs under
// another release name, and Helm refuses an object that names the old one (#52). It runs to the
// end and logs what fails, so one error does not leave the rest behind.
func cleanUp(ctx context.Context, env *Env, release, ns string) {
	if err := env.Helm.Uninstall(ctx, release, ns); err != nil {
		fmt.Fprintf(env.Log, "cleanup: %v\n", err)
	}
	if err := env.Kube.DeleteNamespace(ctx, ns); err != nil {
		fmt.Fprintf(env.Log, "cleanup: %v\n", err)
	}
	if err := deleteKept(ctx, env, func(n string) bool { return n == ns }); err != nil {
		fmt.Fprintf(env.Log, "cleanup: %v\n", err)
	}
}

// deleteKept deletes the cluster-scoped objects Helm made for a release in a namespace mine accepts.
func deleteKept(ctx context.Context, env *Env, mine func(ns string) bool) error {
	refs, err := env.Kube.HelmClusterObjects(ctx, mine)
	if err != nil {
		return err
	}
	if len(refs) > 0 {
		fmt.Fprintf(env.Log, "deleting cluster-scoped objects a release kept: %s\n", strings.Join(refs, ", "))
	}
	return env.Kube.DeleteObjects(ctx, refs)
}
