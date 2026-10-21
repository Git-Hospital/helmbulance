package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// OutsideEndpoints returns, per Service name in ns, the endpoint addresses that are not the IP of
// a pod in ns. A Service with a selector gets its EndpointSlices from the control plane, but a
// chart can add one of its own with the kubernetes.io/service-name label, and kube-proxy sends
// the Service's traffic to its addresses too, wherever they are. Services with no such address
// are left out.
func (c *Client) OutsideEndpoints(ctx context.Context, ns string) (map[string][]string, error) {
	out, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "--namespace", ns, "get", "endpointslices,pods", "-o", "json")
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
			Status struct {
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
		if item.Kind != "Pod" {
			continue
		}
		if item.Status.PodIP != "" {
			podIPs[item.Status.PodIP] = true
		}
		for _, ip := range item.Status.PodIPs {
			podIPs[ip.IP] = true
		}
	}
	outside := map[string][]string{}
	for _, item := range list.Items {
		service := item.Metadata.Labels["kubernetes.io/service-name"]
		if item.Kind != "EndpointSlice" || service == "" {
			continue
		}
		for _, e := range item.Endpoints {
			for _, addr := range e.Addresses {
				if !podIPs[addr] {
					outside[service] = append(outside[service], addr)
				}
			}
		}
	}
	for _, addrs := range outside {
		sort.Strings(addrs)
	}
	return outside, nil
}
