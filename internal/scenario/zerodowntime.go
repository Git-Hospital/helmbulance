package scenario

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egezamb/helmbulance/internal/kube"
	"github.com/egezamb/helmbulance/internal/oracle"
)

// ZeroDowntime measures what users of the release notice while its pods are replaced.
//
// A small pod in the release namespace sends one HTTP request every 200 ms to the release's
// Service, from inside the cluster, as other workloads would. Control: 20 s with no change, which
// must be clean. Treatment: the same traffic while the release is upgraded from --from-version, or,
// without it, while every Deployment and StatefulSet is restarted (what any upgrade that changes
// the pods does). Failed requests in the treatment are the downtime users see.
type ZeroDowntime struct{}

const (
	zeroDowntimeNamespace = "hb-zero-downtime"
	trafficImage          = "busybox:1.36.1"
	controlDuration       = 20 * time.Second
	trafficLead           = 5 * time.Second
	trafficTail           = 10 * time.Second
)

func (ZeroDowntime) Name() string { return "zero-downtime" }

func (ZeroDowntime) Description() string {
	return "Sends a light, steady stream of requests to the release's Service while its pods are replaced, and counts the failures."
}

func (s ZeroDowntime) Run(ctx context.Context, env *Env) (res Result) {
	res.Scenario = s.Name()
	const release, ns = "hb-zd", zeroDowntimeNamespace
	defer func() {
		_ = env.Kube.DeletePod(ctx, ns, "hb-traffic-control")
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
	if err := env.Helm.Install(ctx, release, ns, installChart, env.Values, installExtra...); err != nil {
		return inconclusive(res, "the chart did not install", err)
	}

	target, err := s.target(ctx, env, ns)
	if err != nil {
		res.Verdict = Skipped
		res.Summary = "no Service to send traffic to: " + err.Error()
		return res
	}
	path := env.TrafficPath
	if path == "" {
		path = "/"
	}
	url := "http://" + target + path

	control, err := s.measure(ctx, env, ns, "hb-traffic-control", url, func() error {
		return sleep(ctx, controlDuration)
	})
	if err != nil {
		return inconclusive(res, "the control traffic could not run", err)
	}
	if control.Total == 0 {
		return inconclusive(res, "the control traffic sent no requests", fmt.Errorf("check that %s can be pulled", trafficImage))
	}
	if control.Failed > 0 {
		return inconclusive(res, fmt.Sprintf("%d of %d requests to %s failed with nothing changing", control.Failed, control.Total, url),
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
	operation := "a rolling restart of every Deployment and StatefulSet"
	act := func() error { return env.Kube.RolloutRestart(ctx, ns, workloads, env.Helm.Timeout.String()) }
	if env.FromVersion != "" {
		operation = "the upgrade from " + env.FromVersion
		act = func() error { return env.Helm.Upgrade(ctx, release, ns, env.Chart, env.Values) }
	}
	var actErr error
	treatment, err := s.measure(ctx, env, ns, "hb-traffic", url, func() error {
		if err := sleep(ctx, trafficLead); err != nil {
			return err
		}
		actErr = act()
		return sleep(ctx, trafficTail)
	})
	if err != nil {
		return inconclusive(res, "the traffic during the operation could not run", err)
	}
	if actErr != nil {
		return inconclusive(res, operation+" failed", actErr)
	}

	if treatment.Failed == 0 {
		res.Verdict = Pass
		res.Summary = fmt.Sprintf("no failed requests during %s (%d sent to %s)", operation, treatment.Total, target)
		return res
	}
	res.Verdict = Fail
	res.Summary = fmt.Sprintf("%d of %d requests failed during %s", treatment.Failed, treatment.Total, operation)
	evidence := []string{
		fmt.Sprintf("%d of %d requests to `%s` failed (%.1f%%) during %s; none failed in the %s control.",
			treatment.Failed, treatment.Total, url, treatment.FailedPercent(), operation, controlDuration),
		"Longest outage: " + outageText(treatment.LongestOutage) + ".",
	}
	res.Findings = append(res.Findings, Finding{
		ID:       "HB-ZD-001",
		Title:    "Requests fail while the pods are replaced",
		Severity: "medium",
		Evidence: append(evidence, causes(serving(workloads, services, target))...),
		Impact: "Users and other services see errors on every upgrade or restart, even when nothing " +
			"is wrong with the new version.",
		Fix: "Run at least two replicas with a RollingUpdate that keeps them available " +
			"(maxUnavailable: 0), a readiness probe that passes only when the app can serve, a preStop " +
			"sleep of a few seconds so the pod leaves the Service before it stops, and a " +
			"PodDisruptionBudget. A single replica, or the Recreate strategy, always has a gap.",
	})
	return res
}

// target picks the Service and port to send traffic to: --traffic-target when given, otherwise a
// port named http or web, then a common HTTP port number, then any port with http in its name.
func (ZeroDowntime) target(ctx context.Context, env *Env, ns string) (string, error) {
	if env.TrafficTarget != "" {
		return env.TrafficTarget, nil
	}
	services, err := env.Kube.Services(ctx, ns)
	if err != nil {
		return "", err
	}
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
	for _, s := range services {
		for _, p := range s.Ports {
			if sc := score(p); sc > 0 {
				cands = append(cands, candidate{s.Name, p.Port, sc})
			}
		}
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("no Service port looks like HTTP; give --traffic-target service:port")
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	return cands[0].name + ":" + strconv.Itoa(cands[0].port), nil
}

// measure runs a traffic pod for as long as during() takes and returns what it saw.
func (ZeroDowntime) measure(ctx context.Context, env *Env, ns, pod, url string, during func() error) (oracle.TrafficStats, error) {
	script := "URL=" + shellQuote(url) + "; " + oracle.TrafficScript
	labels := map[string]string{"app.kubernetes.io/name": "helmbulance-traffic"}
	if err := env.Kube.StartPod(ctx, ns, pod, trafficImage, script, labels); err != nil {
		return oracle.TrafficStats{}, err
	}
	if err := env.Kube.WaitPodRunning(ctx, ns, pod, "3m"); err != nil {
		return oracle.TrafficStats{}, err
	}
	if err := during(); err != nil {
		return oracle.TrafficStats{}, err
	}
	log, err := env.Kube.Logs(ctx, ns, pod)
	if err != nil {
		return oracle.TrafficStats{}, err
	}
	_ = env.Kube.DeletePod(ctx, ns, pod)
	return oracle.ParseTraffic(log), nil
}

// causes lists settings of the release's workloads that explain a gap during a rollout.
func causes(workloads []kube.Workload) []string {
	var out []string
	for _, w := range workloads {
		ref := w.Kind + "/" + w.Name
		if w.Strategy == "Recreate" {
			out = append(out, fmt.Sprintf("`%s` uses the Recreate strategy: all pods stop before new ones start.", ref))
		}
		if w.Replicas < 2 && w.Strategy != "Recreate" {
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
	name, _, _ := strings.Cut(target, ":")
	name, _, _ = strings.Cut(name, ".")
	for _, s := range services {
		if s.Name != name {
			continue
		}
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
