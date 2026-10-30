package scenario

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
	"github.com/egezamb/helmbulance/internal/kube"
	"github.com/egezamb/helmbulance/internal/oracle"
)

// ZeroDowntime measures what users of the release notice while its pods are replaced.
//
// A small pod in the release namespace sends one HTTP request every 200 ms to the release's
// Service, from inside the cluster, as other workloads would. Control: 20 s with no change, which
// must be clean, and is run once more if it is not, so one slow request on a busy machine does not
// end the run. Treatment: the same traffic while the release is upgraded from --from-version, or,
// without it, while every Deployment, StatefulSet and DaemonSet is restarted, one at a time (what
// any upgrade that changes the pods does). Failed requests from the start of the operation on are
// the downtime users see; in a restart, each is blamed on the workload being replaced then.
type ZeroDowntime struct{}

const (
	zeroDowntimeNamespace = "hb-zero-downtime"
	trafficImage          = busyboxImage
)

// Variables, not constants, so tests can shorten them.
var (
	controlDuration = 20 * time.Second
	trafficLead     = 5 * time.Second
	trafficTail     = 10 * time.Second
)

func (ZeroDowntime) Name() string { return "zero-downtime" }

func (ZeroDowntime) Description() string {
	return "Sends a light, steady stream of requests to the release's Service while its pods are replaced, and counts the failures."
}

func (s ZeroDowntime) Run(ctx context.Context, env *Env) (res Result) {
	res.Scenario = s.Name()
	const release, ns = "hb-zd", zeroDowntimeNamespace
	defer func() {
		ctx, cancel := cleanupContext(ctx)
		defer cancel()
		_ = env.Kube.DeletePod(ctx, ns, "hb-traffic-control")
		_ = env.Kube.DeletePod(ctx, ns, "hb-traffic-control-2")
		_ = env.Kube.DeletePod(ctx, ns, "hb-traffic")
		if err := env.Helm.Uninstall(ctx, release, ns); err != nil {
			fmt.Fprintf(env.Log, "cleanup: %v\n", err)
		}
		if err := env.Kube.DeleteNamespace(ctx, ns); err != nil {
			fmt.Fprintf(env.Log, "cleanup: %v\n", err)
		}
	}()

	// Install: the earlier version when there is one, so the treatment is the real upgrade.
	installChart, installExtra := env.Chart, []string(nil)
	if env.FromVersion != "" {
		installChart = env.From
		if installChart == "" {
			installChart = env.Chart
		}
		installExtra = []string{"--version", env.FromVersion}
	}
	if err := startClean(ctx, env, release, ns); err != nil {
		return inconclusive(res, "could not clear what an earlier run left", err)
	}
	if err := env.Helm.Install(ctx, release, ns, installChart, env.Values, installExtra...); err != nil {
		return inconclusive(res, "the chart did not install", err)
	}
	if env.FromVersion != "" {
		if _, err := installedFrom(ctx, env, release, ns); err != nil {
			return inconclusive(res, "could not confirm the earlier version", err)
		}
	}

	target, err := s.target(ctx, env, ns)
	if errors.Is(err, errNoTarget) {
		res.Verdict = Skipped
		res.Summary = "no Service to send traffic to: " + err.Error()
		return res
	}
	if err != nil {
		return inconclusive(res, "could not read the release's Services", err)
	}
	path := env.TrafficPath
	if path == "" {
		path = "/"
	}
	url := "http://" + target + path
	shown := execx.RedactURLs(url) // the path can carry a token
	// When helmbulance chose the Service, its endpoints are read again after the operation: the
	// version under test, a hook or a pod with RBAC can add a slice of its own at any time after
	// target ran. This detects such a slice; it does not keep the traffic away from it.
	service := ""
	if env.TrafficTarget == "" {
		service = target[:strings.LastIndex(target, ":")]
	}

	var control, firstControl oracle.TrafficStats
	for i, pod := range []string{"hb-traffic-control", "hb-traffic-control-2"} {
		log, err := s.measure(ctx, env, ns, pod, url, func() error {
			return sleep(ctx, controlDuration)
		})
		if err != nil {
			return inconclusive(res, "the control traffic could not run", err)
		}
		control = oracle.ParseTraffic(log)
		if control.Total == 0 {
			return inconclusive(res, "the control traffic sent no requests", fmt.Errorf("check that %s can be pulled", trafficImage))
		}
		if i == 0 {
			firstControl = control
		}
		if control.Failed == 0 {
			break
		}
		fmt.Fprintf(env.Log, "  control: %d of %d requests failed (at second %s); running it once more\n",
			control.Failed, control.Total, seconds(control.FailedAt))
	}
	if control.Failed > 0 {
		return inconclusive(res, fmt.Sprintf("%d of %d requests to %s failed with nothing changing, in two controls (the second failed at second %s)",
			control.Failed, control.Total, shown, seconds(control.FailedAt)),
			fmt.Errorf("set --traffic-path to a URL that answers 2xx, or check NetworkPolicies that block the traffic pod"))
	}

	workloads, err := env.Kube.Workloads(ctx, ns)
	if err != nil {
		return inconclusive(res, "could not read the workloads", err)
	}
	services, err := env.Kube.Services(ctx, ns)
	if err != nil {
		return inconclusive(res, "could not read the Services", err)
	}
	var actErr, recheckErr error
	var actStart int64
	// restarts holds the second, in the traffic pod's clock, at which each workload's restart began.
	// One workload is restarted at a time, so a failure is blamed on the one being replaced then,
	// not on whatever the Service selects (#10).
	var restarts []int64
	done := 0 // workloads whose rollout finished
	pods, behind, behindPods := 0, 0, 0
	svc, svcKnown := targetService(services, target)
	for _, w := range workloads {
		pods += w.Replicas
		if svcKnown && w.Serves(svc) {
			behind++
			behindPods += w.Replicas
		}
	}
	operation := fmt.Sprintf("a rolling restart of %s, %s", count(len(workloads), "workload"), count(pods, "pod"))
	if svcKnown {
		operation += fmt.Sprintf(" (%s, %s behind %s)", count(behind, "workload"), count(behindPods, "pod"), svc.Name)
	}
	operation += ", one at a time"
	act := func() error {
		for i, w := range workloads {
			start := actStart
			if i > 0 {
				// rollout status returns while the last old pod is still stopping; its failures
				// belong to the workload before (review of #85).
				if err := settle(ctx, trafficTail); err != nil {
					return err
				}
				last, err := env.Kube.LastLogLine(ctx, ns, "hb-traffic")
				if err != nil {
					return err
				}
				// Without a line the window starts where the previous one did.
				if t, ok := oracle.TrafficSecond(last); ok {
					start = t
				} else {
					start = restarts[i-1]
				}
			}
			restarts = append(restarts, start)
			if err := env.Kube.RestartWorkload(ctx, ns, w, env.Helm.Timeout.String()); err != nil {
				return fmt.Errorf("the restart of `%s/%s` failed: %w", w.Kind, w.Name, err)
			}
			done++
		}
		return nil
	}
	if env.FromVersion != "" {
		operation = "the upgrade from " + env.FromVersion
		act = func() error { return env.Helm.Upgrade(ctx, release, ns, env.Chart, env.Values) }
	} else if len(workloads) == 0 {
		res.Verdict = Skipped
		res.Summary = "no Deployment, StatefulSet or DaemonSet to restart, so nothing would be replaced"
		return res
	} else if svcKnown && behind == 0 {
		res.Verdict = Skipped
		res.Summary = fmt.Sprintf("a rolling restart would not replace the pods behind %s: no Deployment, StatefulSet or DaemonSet in the release selects them", svc.Name)
		return res
	}
	gained := map[string]bool{} // addresses the chosen Service gained in slices the control plane did not write
	recheck := func() {
		if service == "" || recheckErr != nil {
			return
		}
		outside, err := env.Kube.OutsideEndpoints(ctx, ns)
		if err != nil {
			recheckErr = err
			return
		}
		for _, addr := range outside[service] {
			gained[addr] = true
		}
	}
	log, err := s.measure(ctx, env, ns, "hb-traffic", url, func() error {
		if err := sleep(ctx, trafficLead); err != nil {
			return err
		}
		// The traffic pod stamps its requests with the node's clock, which can differ from this
		// machine's (a VM after sleep, a remote DOCKER_HOST). So the operation starts at the pod's
		// last request, in its own clock. Without one, every request counts.
		last, err := env.Kube.LastLogLine(ctx, ns, "hb-traffic")
		if err != nil {
			return err
		}
		actStart, _ = oracle.TrafficSecond(last)
		actErr = act()
		recheck()
		if err := sleep(ctx, trafficTail); err != nil {
			return err
		}
		recheck()
		return nil
	})
	if err != nil {
		return inconclusive(res, "the traffic during the operation could not run", err)
	}
	treatment := oracle.ParseTrafficSince(log, actStart)
	// A restart that failed part way still leaves the windows before it complete; anything else that
	// failed leaves nothing proven.
	if actErr != nil && done == 0 {
		return inconclusive(res, operation+" failed", actErr)
	}
	if recheckErr != nil {
		return inconclusive(res, "could not read the endpoints of "+service+" again after "+operation, recheckErr)
	}
	if len(gained) > 0 {
		return inconclusive(res, "the traffic may have left the release's pods during "+operation,
			fmt.Errorf("%s gained endpoints the control plane did not write: %s",
				service, strings.Join(slices.Sorted(maps.Keys(gained)), ", ")))
	}
	finished := len(restarts)
	if actErr != nil {
		// Only the finished windows count. restarts[done], when there is one, is where the failed
		// workload's window began, so the treatment ends there.
		finished = done
		treatment = oracle.ParseTrafficBetween(log, actStart, cutoff(restarts, done))
	}
	if treatment.Total == 0 {
		return inconclusive(res, "no requests were sent during "+operation, fmt.Errorf("the traffic pod logged nothing after the operation started"))
	}

	windows := restartWindows(log, restarts, finished, workloads)
	var real, sandbox []window
	for _, w := range windows {
		if oneNodeGap(w.workload) {
			sandbox = append(sandbox, w)
		} else {
			real = append(real, w)
		}
	}
	switch {
	case treatment.Failed == 0 && actErr != nil:
		return inconclusive(res, operation+" failed", actErr)
	case treatment.Failed == 0:
		res.Verdict = Pass
		res.Summary = fmt.Sprintf("no failed requests during %s (%d sent to %s)", operation, treatment.Total, target)
		return res
	case len(real) == 0 && len(sandbox) > 0:
		var refs []string
		for _, w := range sandbox {
			refs = append(refs, "`"+w.ref()+"`")
		}
		return inconclusive(res, fmt.Sprintf("%d of %d requests failed, all while %s restarted",
			treatment.Failed, treatment.Total, strings.Join(refs, ", ")),
			fmt.Errorf("a DaemonSet with maxSurge 0 stops its pod on a node before it starts the new one, and the sandbox has one node, so its Service had no endpoint for a moment; with more nodes the others keep serving, so this sandbox cannot tell whether the chart has a gap"))
	case actErr != nil && len(real) == 0:
		return inconclusive(res, operation+" failed", actErr)
	}

	res.Verdict = Fail
	res.Summary = fmt.Sprintf("%d of %d requests failed during %s", treatment.Failed, treatment.Total, operation)
	evidence := []string{
		fmt.Sprintf("%d of %d requests to `%s` failed (%.1f%%) during %s; none failed in the %s control.",
			treatment.Failed, treatment.Total, shown, treatment.FailedPercent(), operation, controlDuration),
		"Longest outage: " + outageText(treatment.LongestOutage) + ".",
	}
	if firstControl.Failed > 0 {
		evidence = append(evidence, fmt.Sprintf("The first control lost %d of %d requests with nothing changing and was repeated; the cluster itself dropped requests before the operation.",
			firstControl.Failed, firstControl.Total))
	}
	blamed := serving(workloads, services, target)
	if len(real) > 0 {
		// With one workload its window is the whole operation, which the first line already gives.
		if len(workloads) > 1 || actErr != nil {
			for _, w := range windows {
				evidence = append(evidence, w.line(svc, svcKnown))
			}
		}
		blamed = nil
		for _, w := range real {
			blamed = append(blamed, w.workload)
		}
	}
	if actErr != nil {
		evidence = append(evidence, "Then "+actErr.Error()+"; the windows before it are complete, so their failures count.")
	}
	res.Findings = append(res.Findings, Finding{
		ID:       "HB-ZD-001",
		Title:    "Requests fail while the pods are replaced",
		Severity: "medium",
		Evidence: append(evidence, causes(blamed)...),
		Impact: "Users and other services see errors on every upgrade or restart, even when nothing " +
			"is wrong with the new version.",
		Fix: "Run at least two replicas with a RollingUpdate that keeps them available " +
			"(maxUnavailable: 0), a readiness probe that passes only when the app can serve, a preStop " +
			"sleep of a few seconds so the pod leaves the Service before it stops. A single replica, " +
			"or the Recreate strategy, always has a gap. A PodDisruptionBudget helps with node drains " +
			"and other evictions, not rollouts.",
	})
	return res
}

// errNoTarget means the release has nothing zero-downtime can send traffic to.
var errNoTarget = errors.New("no Service port looks like HTTP; give --traffic-target service:port")

// target picks the Service and port to send traffic to: --traffic-target when given, otherwise
// pickTarget's choice among the release's Services, minus any whose endpoints are not all pods
// in the namespace.
func (ZeroDowntime) target(ctx context.Context, env *Env, ns string) (string, error) {
	if env.TrafficTarget != "" {
		return env.TrafficTarget, nil
	}
	services, err := env.Kube.Services(ctx, ns)
	if err != nil {
		return "", err
	}
	outside, err := env.Kube.OutsideEndpoints(ctx, ns)
	if err != nil {
		return "", err
	}
	return pickTarget(services, outside)
}

// pickTarget returns "service:port" for a port named http or web, then a common HTTP port number,
// then any port with http in its name. Services whose traffic would not stay on the release's pods
// are skipped: ExternalName (an outside host), headless (no port translation, so a request can
// reach a pod that is not ready), externalIPs, and those with an address in outside (endpoints in
// slices the control plane did not write, keyed by Service name).
func pickTarget(services []kube.Service, outside map[string][]string) (string, error) {
	score := func(p kube.ServicePort) int {
		switch {
		case p.Protocol != "" && p.Protocol != "TCP":
			return 0
		case p.Name == "http" || p.Name == "web":
			return 3
		case p.Port == 80 || p.Port == 8080 || p.Port == 3000 || p.Port == 8000:
			return 2
		case strings.Contains(p.Name, "http"):
			return 1
		}
		return 0
	}
	type candidate struct {
		name  string
		port  int
		score int
	}
	var cands []candidate
	var skipped []string
	for _, s := range services {
		if why := leavesThePods(s, outside[s.Name]); why != "" {
			skipped = append(skipped, s.Name+" ("+why+")")
			continue
		}
		for _, p := range s.Ports {
			if sc := score(p); sc > 0 {
				cands = append(cands, candidate{s.Name, p.Port, sc})
			}
		}
	}
	if len(cands) == 0 {
		if len(skipped) > 0 {
			return "", fmt.Errorf("%w (skipped %s)", errNoTarget, strings.Join(skipped, ", "))
		}
		return "", errNoTarget
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	return cands[0].name + ":" + strconv.Itoa(cands[0].port), nil
}

// leavesThePods says why traffic to s would not stay on the release's pods, or "" if it would.
// outside holds the addresses of s's endpoints that the control plane did not write.
func leavesThePods(s kube.Service, outside []string) string {
	switch {
	case s.Type == "ExternalName":
		return "ExternalName"
	case s.ClusterIP == "None":
		return "headless"
	case len(s.ExternalIPs) > 0:
		return "externalIPs"
	case len(outside) > 0:
		return "endpoints the control plane did not write: " + strings.Join(outside, ", ")
	}
	return ""
}

// measure runs a traffic pod for as long as during() takes and returns its log.
func (ZeroDowntime) measure(ctx context.Context, env *Env, ns, pod, url string, during func() error) (string, error) {
	script := "URL=" + shellQuote(url) + "; " + oracle.TrafficScript
	labels := map[string]string{"app.kubernetes.io/name": "helmbulance-traffic"}
	if err := env.Kube.StartPod(ctx, ns, pod, trafficImage, script, labels); err != nil {
		return "", err
	}
	if err := env.Kube.WaitPodRunning(ctx, ns, pod, "3m"); err != nil {
		return "", err
	}
	if err := during(); err != nil {
		return "", err
	}
	log, err := env.Kube.Logs(ctx, ns, pod)
	if err != nil {
		return "", err
	}
	_ = env.Kube.DeletePod(ctx, ns, pod)
	return log, nil
}

// causes lists settings of the release's workloads that explain a gap during a rollout.
func causes(workloads []kube.Workload) []string {
	var out []string
	for _, w := range workloads {
		ref := w.Kind + "/" + w.Name
		if w.Strategy == "Recreate" {
			out = append(out, fmt.Sprintf("`%s` uses the Recreate strategy: all pods stop before new ones start.", ref))
		}
		switch {
		case w.Kind == "DaemonSet" && (w.MaxSurge == "" || w.MaxSurge == "0" || w.MaxSurge == "0%"):
			out = append(out, fmt.Sprintf("`%s` updates with maxSurge 0: on each node the old pod stops before the new one starts.", ref))
		case w.Kind != "DaemonSet" && w.Replicas < 2 && w.Strategy != "Recreate":
			out = append(out, fmt.Sprintf("`%s` has %d replica; while it restarts nothing else serves.", ref, w.Replicas))
		}
		if !w.HasReadiness {
			out = append(out, fmt.Sprintf("`%s` has no readiness probe, so new pods get traffic before they can serve.", ref))
		}
		if !w.HasPreStop {
			out = append(out, fmt.Sprintf("`%s` has no preStop hook, so a stopping pod can still be sent requests.", ref))
		}
	}
	return out
}

// seconds lists failure times for a summary: "0, 1, 14".
func seconds(at []int64) string {
	out := make([]string, len(at))
	for i, t := range at {
		out[i] = strconv.FormatInt(t, 10)
	}
	return strings.Join(out, ", ")
}

// settle is the wait between two workloads of a restart; tests replace it to see where it runs.
var settle = sleep

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// outageText reads a one-second-resolution outage the way it was measured.
func outageText(d time.Duration) string {
	if d < time.Second {
		return "under a second"
	}
	return "about " + d.String()
}

// serving narrows the workloads to those behind the Service the traffic went to, so the causes
// listed are the ones users of that Service feel. Falls back to all workloads if none match.
func serving(workloads []kube.Workload, services []kube.Service, target string) []kube.Workload {
	if s, ok := targetService(services, target); ok {
		var out []kube.Workload
		for _, w := range workloads {
			if w.Serves(s) {
				out = append(out, w)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return workloads
}

// targetService finds the Service a target (service[.namespace...]:port) names.
func targetService(services []kube.Service, target string) (kube.Service, bool) {
	name, _, _ := strings.Cut(target, ":")
	name, _, _ = strings.Cut(name, ".")
	for _, s := range services {
		if s.Name == name {
			return s, true
		}
	}
	return kube.Service{}, false
}

// window is one workload's share of a one-at-a-time restart, with its failures.
type window struct {
	workload kube.Workload
	stats    oracle.TrafficStats
}

func (w window) ref() string { return w.workload.Kind + "/" + w.workload.Name }

// line is the evidence for a window: its counts, and whether it serves the target Service.
func (w window) line(svc kube.Service, svcKnown bool) string {
	line := fmt.Sprintf("While `%s` restarted (%s): %d of %d requests failed; longest outage %s.",
		w.ref(), count(w.workload.Replicas, "pod"), w.stats.Failed, w.stats.Total, outageText(w.stats.LongestOutage))
	if svcKnown && !w.workload.Serves(svc) {
		line += fmt.Sprintf(" `%s` does not serve `%s`; the pods behind `%s` may depend on it.", w.ref(), svc.Name, svc.Name)
	}
	if oneNodeGap(w.workload) {
		line += " On this one-node sandbox a DaemonSet with maxSurge 0 always has this gap, so it is not counted."
	}
	return line
}

// restartWindows splits the treatment log at the second each workload's restart began and returns
// those of the first n windows with failures. A request in the second the next restart began counts
// for the next one; the last window runs to the end of the log.
func restartWindows(log string, restarts []int64, n int, workloads []kube.Workload) []window {
	var out []window
	for i, start := range restarts[:n] {
		st := oracle.ParseTrafficBetween(log, start, cutoff(restarts, i+1))
		if st.Failed > 0 {
			out = append(out, window{workload: workloads[i], stats: st})
		}
	}
	return out
}

// cutoff is the start of the window after i, or 0 (no end) when there is none.
func cutoff(restarts []int64, next int) int64 {
	if next < len(restarts) {
		return restarts[next]
	}
	return 0
}

// oneNodeGap reports whether a workload is a DaemonSet that, on the one-node sandbox, has no pod
// while it is replaced: one scheduled pod and maxSurge 0 (the default), so the old pod stops
// before the new one starts.
func oneNodeGap(w kube.Workload) bool {
	return w.Kind == "DaemonSet" && w.Replicas == 1 && (w.MaxSurge == "" || w.MaxSurge == "0" || w.MaxSurge == "0%")
}

// count writes n with its noun: "1 pod", "3 pods".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
