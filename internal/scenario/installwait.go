package scenario

import (
	"context"
	"fmt"
	"strings"
)

// timedOutInstall is an install whose --wait ran out earlier in the run: the scenario that waited,
// and helm's error.
type timedOutInstall struct {
	scenario string
	err      error
}

// imagePulls are waiting reasons that can mean the image was still being pulled when --wait ran
// out. The next install of the same chart can then start from the node's cache, so it is waited
// for again.
var imagePulls = map[string]bool{
	"ContainerCreating": true,
	"ErrImagePull":      true,
	"ImagePullBackOff":  true,
}

// install is env.Helm.Install for scenario, except that it does not wait again for an install of
// the same chart, version and values whose --wait already ran out in this run (#165). Every
// scenario but reinstall stops at such an install with INCONCLUSIVE, so the second wait cannot
// change a result; it only spends --timeout again. The release name and namespace differ between
// scenarios and are not part of what is installed.
//
// It remembers a timeout only when no container was waiting on an image, and only for this run:
// scenarios share no state beyond being skipped. reinstall calls env.Helm.Install itself, since it
// goes on after a timed-out first install on purpose.
func install(ctx context.Context, env *Env, scenario, release, ns, chart string, extra ...string) error {
	key := installKey(env, chart, extra)
	if t, ok := env.timedOut[key]; ok {
		fmt.Fprintf(env.Log, "not installing %s again: its --wait already ran out in %s\n", chart, t.scenario)
		return fmt.Errorf("the same install's --wait already ran out in %s, so it was not waited for again: %w", t.scenario, t.err)
	}
	err := env.Helm.Install(ctx, release, ns, chart, env.Values, extra...)
	rememberTimeout(ctx, env, scenario, ns, key, err)
	return err
}

// rememberTimeout records err for install when it is a --wait timeout with no image pull pending.
func rememberTimeout(ctx context.Context, env *Env, scenario, ns, key string, err error) {
	if !timedOutWaiting(ctx, err) {
		return
	}
	containers, cerr := env.Kube.Containers(ctx, ns)
	if cerr != nil {
		fmt.Fprintf(env.Log, "could not read containers after the install timed out, so a later install waits again: %v\n", cerr)
		return
	}
	for _, c := range containers {
		if imagePulls[c.Waiting] {
			return
		}
	}
	if env.timedOut == nil {
		env.timedOut = map[string]timedOutInstall{}
	}
	env.timedOut[key] = timedOutInstall{scenario: scenario, err: err}
}

// installKey is what an install puts in the cluster, apart from its release name and namespace.
func installKey(env *Env, chart string, extra []string) string {
	parts := append([]string{chart}, extra...)
	for _, f := range env.Values.Files {
		parts = append(parts, "-f", f)
	}
	for _, s := range env.Values.Sets {
		parts = append(parts, "--set", s)
	}
	return strings.Join(parts, "\x00")
}
