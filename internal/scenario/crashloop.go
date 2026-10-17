package scenario

import (
	"context"
	"fmt"
	"strings"

	"github.com/egezamb/helmbulance/internal/kube"
)

// timedOutWaiting reports whether a helm command failed because `--wait` timed out with the
// release applied, as opposed to a cancelled run, the chart's own webhook, or a pull or tool
// error. Only then is it worth reading the release's pods for what kept it from being ready.
func timedOutWaiting(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "failed calling webhook") {
		return false
	}
	return strings.Contains(msg, "timed out waiting for the condition") ||
		strings.Contains(msg, "context deadline exceeded")
}

// startFailures are waiting reasons that come from the pod spec the chart renders, not from the
// cluster: the container crashed, or its command, config or image name cannot work. Image pulls
// (ErrImagePull, ImagePullBackOff) are left out: a registry or network can cause those.
var startFailures = map[string]bool{
	"CrashLoopBackOff":           true,
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
	"RunContainerError":          true,
	"InvalidImageName":           true,
}

// crashedSince lists the containers that crashed or could not start since the pods in before were
// read. A pod from before only counts with restarts beyond its count then, so a crash the earlier
// version already had is not blamed on the upgrade.
func crashedSince(containers []kube.Container, before []kube.Pod) []string {
	baseline := map[string]int{}
	for _, p := range before {
		baseline[p.UID] = p.Restarts
	}
	restarts := map[string]int{}
	for _, c := range containers {
		restarts[c.PodUID] += c.Restarts
	}
	var crashed []string
	for _, c := range containers {
		_, old := baseline[c.PodUID]
		restartedSince := restarts[c.PodUID] > baseline[c.PodUID]
		if startFailures[c.Waiting] && (!old || restartedSince) || c.Restarts > 0 && restartedSince {
			crashed = append(crashed, describeContainer(c))
		}
	}
	return crashed
}

// describeContainer is one line of evidence: which container, why it waits, how its last run ended.
func describeContainer(c kube.Container) string {
	kind := "container"
	if c.Init {
		kind = "init container"
	}
	var state []string
	if c.Waiting != "" {
		state = append(state, c.Waiting)
	}
	if c.LastReason != "" {
		state = append(state, fmt.Sprintf("last run %s (exit %d)", c.LastReason, c.LastExit))
	}
	if c.Restarts > 0 {
		state = append(state, fmt.Sprintf("%d restart(s)", c.Restarts))
	}
	return fmt.Sprintf("pod `%s` (%s), %s `%s`: %s", c.Pod, c.Owner, kind, c.Name, strings.Join(state, ", "))
}

// crashesAfterTimeout reads the release's containers after a `--wait` timeout and lists the ones
// that crashed since before. An error reading them is logged and lists nothing, so the result
// stays INCONCLUSIVE.
func crashesAfterTimeout(ctx context.Context, env *Env, ns string, before []kube.Pod) []string {
	containers, err := env.Kube.Containers(ctx, ns)
	if err != nil {
		fmt.Fprintf(env.Log, "could not read containers after the timeout: %v\n", err)
		return nil
	}
	return crashedSince(containers, before)
}
