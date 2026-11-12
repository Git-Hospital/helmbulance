package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// OutsideEndpoints returns, per Service name in ns, the EndpointSlice addresses that are not the IP
// of a pod in ns. A chart can add a slice of its own with the kubernetes.io/service-name label (or
// an Endpoints object, which is mirrored into one), and kube-proxy sends the Service's traffic to its
// addresses too, wherever they are. The slice's labels prove nothing, since the chart can set the
// control plane's managed-by label on its own slice; only a live pod's IP keeps the traffic on the
// release's pods. A hostNetwork pod's IP is the node's, and a pod that has finished (Succeeded,
// Failed) has given its IP back for reuse, so neither counts. Services with no outside address are
// left out.
//
// kubectl lists slices and then pods, and the endpointslice controller drops a deleted pod from its
// slice a moment after the pod is gone, so a rollout can show an address that just stopped being a
// pod. An address seen outside is read again after OutsideRecheck and reported only if it is still
// outside; one that comes and goes between two reads is not seen.
func (c *Client) OutsideEndpoints(ctx context.Context, ns string) (map[string][]string, error) {
	first, err := c.outsideOnce(ctx, ns)
	if err != nil || len(first) == 0 {
		return first, err
	}
	select {
	case <-time.After(OutsideRecheck):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	second, err := c.outsideOnce(ctx, ns)
	if err != nil {
		return nil, err
	}
	both := map[string][]string{}
	for service, addrs := range second {
		for _, a := range addrs {
			if slices.Contains(first[service], a) {
				both[service] = append(both[service], a)
			}
		}
	}
	return both, nil
}

// OutsideRecheck is how long OutsideEndpoints waits before it reads an outside address again. A
// variable, so tests can shorten it.
var OutsideRecheck = 2 * time.Second

func (c *Client) outsideOnce(ctx context.Context, ns string) (map[string][]string, error) {
	out, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "--request-timeout", readTimeout, "--namespace", ns, "get", "endpointslices,pods", "-o", "json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Endpoints []struct {
				Addresses []string `json:"addresses"`
			} `json:"endpoints"`
			Spec struct {
				HostNetwork bool `json:"hostNetwork"`
			} `json:"spec"`
			Status struct {
				Phase  string `json:"phase"`
				PodIP  string `json:"podIP"`
				PodIPs []struct {
					IP string `json:"ip"`
				} `json:"podIPs"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse endpointslices and pods: %w", err)
	}
	podIPs := map[string]bool{}
	for _, item := range list.Items {
		if item.Kind != "Pod" || item.Spec.HostNetwork || item.Status.Phase == "Succeeded" || item.Status.Phase == "Failed" {
			continue
		}
		podIPs[item.Status.PodIP] = true
		for _, ip := range item.Status.PodIPs {
			podIPs[ip.IP] = true
		}
	}
	delete(podIPs, "")
	outside := map[string][]string{}
	for _, item := range list.Items {
		service := item.Metadata.Labels["kubernetes.io/service-name"]
		if item.Kind != "EndpointSlice" || service == "" {
			continue
		}
		for _, e := range item.Endpoints {
			for _, a := range e.Addresses {
				if !podIPs[a] {
					outside[service] = append(outside[service], a)
				}
			}
		}
	}
	for service, addrs := range outside {
		slices.Sort(addrs)
		outside[service] = slices.Compact(addrs)
	}
	return outside, nil
}
