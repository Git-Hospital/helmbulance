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

// crashedSince lists the containers that crashed or could not start since before the upgrade.
//   - A container of a new pod counts if it waits with a startFailures reason, or if it restarted
//     more often than the same container (by name, init containers apart) did in any pod of its
//     workload before the upgrade. A container that crashes once on its first start in both
//     versions is the app's startup, as in unhealthyAfter's floor. Without the containers from
//     before (beforeKnown false) there is no such count, so only the waiting reason counts.
//   - A container of a pod from before counts only if that pod was Ready then and the container
//     was not already failing, and only for restarts beyond its own count then (matched by pod
//     UID and container name, init containers apart). So an init container that restarted while
//     v1 started, or a crash loop v1 already had, is not blamed on the upgrade. Without the
//     containers from before, pods from before are left out.
func crashedSince(containers, beforeContainers []kube.Container, beforePods []kube.Pod, beforeKnown bool) []string {
	key := func(c kube.Container) string { return fmt.Sprintf("%s/%t/%s", c.PodUID, c.Init, c.Name) }
	floorKey := func(c kube.Container) string { return fmt.Sprintf("%s/%t/%s", workload(c.Owner), c.Init, c.Name) }
	was, floor := map[string]kube.Container{}, map[string]int{}
	for _, c := range beforeContainers {
		was[key(c)] = c
		floor[floorKey(c)] = max(floor[floorKey(c)], c.Restarts)
	}
	old, ready := map[string]bool{}, map[string]bool{}
	for _, p := range beforePods {
		old[p.UID], ready[p.UID] = true, p.Ready
	}
	var crashed []string
	for _, c := range containers {
		if !old[c.PodUID] {
			if startFailures[c.Waiting] || beforeKnown && c.Restarts > floor[floorKey(c)] {
				crashed = append(crashed, describeContainer(c))
			}
			continue
		}
		b, had := was[key(c)]
		if !beforeKnown || !ready[c.PodUID] || had && startFailures[b.Waiting] {
			continue
		}
		if c.Restarts > b.Restarts || startFailures[c.Waiting] {
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
func crashesAfterTimeout(ctx context.Context, env *Env, ns string, beforePods []kube.Pod, beforeContainers []kube.Container, beforeKnown bool) []string {
	containers, err := env.Kube.Containers(ctx, ns)
	if err != nil {
		fmt.Fprintf(env.Log, "could not read containers after the timeout: %v\n", err)
		return nil
	}
	return crashedSince(containers, beforeContainers, beforePods, beforeKnown)
}
