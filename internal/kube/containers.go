package kube

import (
	"context"
	"encoding/json"
	"fmt"
)

// Container is the state of one container (or init container) of a pod: why it is waiting, how its
// last run ended, and how often it restarted.
type Container struct {
	Pod      string
	PodUID   string
	Owner    string // kind/name of the pod's controlling object
	Name     string
	Init     bool
	Restarts int
	// Waiting is the reason the container is not running, such as CrashLoopBackOff,
	// ImagePullBackOff or CreateContainerConfigError; empty when it runs.
	Waiting string
	// LastReason and LastExit are how the last finished run ended (Error and 1, OOMKilled and 137);
	// LastReason is empty when no run has finished.
	LastReason string
	LastExit   int
}

// Containers returns the containers and init containers of the pods in ns. Pods that are
// terminating are left out, like in Pods.
func (c *Client) Containers(ctx context.Context, ns string) ([]Container, error) {
	out, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "--namespace", ns, "get", "pods", "-o", "json")
	if err != nil {
		return nil, err
	}
	return parseContainers(out)
}

type containerStatus struct {
	Name         string `json:"name"`
	RestartCount int    `json:"restartCount"`
	State        struct {
		Waiting *struct {
			Reason string `json:"reason"`
		} `json:"waiting"`
		Terminated *struct {
			Reason   string `json:"reason"`
			ExitCode int    `json:"exitCode"`
		} `json:"terminated"`
	} `json:"state"`
	LastState struct {
		Terminated *struct {
			Reason   string `json:"reason"`
			ExitCode int    `json:"exitCode"`
		} `json:"terminated"`
	} `json:"lastState"`
}

func parseContainers(out []byte) ([]Container, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name              string  `json:"name"`
				UID               string  `json:"uid"`
				DeletionTimestamp *string `json:"deletionTimestamp"`
				OwnerReferences   []struct {
					Kind string `json:"kind"`
					Name string `json:"name"`
				} `json:"ownerReferences"`
			} `json:"metadata"`
			Status struct {
				InitContainerStatuses []containerStatus `json:"initContainerStatuses"`
				ContainerStatuses     []containerStatus `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse pods: %w", err)
	}
	var containers []Container
	for _, item := range list.Items {
		if item.Metadata.DeletionTimestamp != nil {
			continue
		}
		owner := ""
		if refs := item.Metadata.OwnerReferences; len(refs) > 0 {
			owner = refs[0].Kind + "/" + refs[0].Name
		}
		add := func(s containerStatus, init bool) {
			c := Container{Pod: item.Metadata.Name, PodUID: item.Metadata.UID, Owner: owner, Name: s.Name, Init: init, Restarts: s.RestartCount}
			if s.State.Waiting != nil {
				c.Waiting = s.State.Waiting.Reason
			}
			// A run that just ended is in State; an earlier one, once the container restarted, in LastState.
			if t := s.State.Terminated; t != nil {
				c.LastReason, c.LastExit = t.Reason, t.ExitCode
			} else if t := s.LastState.Terminated; t != nil {
				c.LastReason, c.LastExit = t.Reason, t.ExitCode
			}
			containers = append(containers, c)
		}
		for _, s := range item.Status.InitContainerStatuses {
			add(s, true)
		}
		for _, s := range item.Status.ContainerStatuses {
			add(s, false)
		}
	}
	return containers, nil
}
