package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// controlPlaneSlices is the managed-by label value of the EndpointSlices the control plane makes
// from a Service's selector.
const controlPlaneSlices = "endpointslice-controller.k8s.io"

// OutsideEndpoints returns, per Service name in ns, the addresses of the EndpointSlices the control
// plane did not write. A Service with a selector gets its slices from the endpointslice controller,
// with the IPs of the pods it selects. A chart can add a slice of its own with the
// kubernetes.io/service-name label (or an Endpoints object, which is mirrored into one), and
// kube-proxy sends the Service's traffic to its addresses too, wherever they are. Such a slice is
// the chart's whatever its addresses: even a pod IP can belong to a hostNetwork pod, which is the
// node. Services with no such address are left out.
func (c *Client) OutsideEndpoints(ctx context.Context, ns string) (map[string][]string, error) {
	out, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "--namespace", ns, "get", "endpointslices", "-o", "json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Endpoints []struct {
				Addresses []string `json:"addresses"`
			} `json:"endpoints"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse endpointslices: %w", err)
	}
	outside := map[string][]string{}
	for _, item := range list.Items {
		labels := item.Metadata.Labels
		service := labels["kubernetes.io/service-name"]
		if service == "" || labels["endpointslice.kubernetes.io/managed-by"] == controlPlaneSlices {
			continue
		}
		for _, e := range item.Endpoints {
			outside[service] = append(outside[service], e.Addresses...)
		}
	}
	for service, addrs := range outside {
		slices.Sort(addrs)
		outside[service] = slices.Compact(addrs)
	}
	return outside, nil
}
