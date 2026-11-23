package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// maxWaitMessage keeps a kubelet message (an image pull error can quote a whole registry
// response) to a length that still reads as one line of a report.
const maxWaitMessage = 300

// podWaitReason says why the pod is not done, from its own status: the first container that waits
// and the kubelet's message for it, such as ErrImagePull and the registry's rate-limit answer, or
// why it is not scheduled yet. It is "" when the status names nothing, and when the pod cannot be
// read: it only adds to an error the caller already has (#176).
func (c *Client) podWaitReason(ctx context.Context, ns, name string) string {
	out, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "--namespace", ns,
		"--request-timeout", readTimeout, "get", "pod", name, "-o", "json")
	if err != nil {
		return ""
	}
	return parsePodWaitReason(out)
}

func parsePodWaitReason(out []byte) string {
	type waiting struct {
		Name  string `json:"name"`
		State struct {
			Waiting *struct {
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"waiting"`
		} `json:"state"`
	}
	var pod struct {
		Status struct {
			Conditions []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"conditions"`
			InitContainerStatuses []waiting `json:"initContainerStatuses"`
			ContainerStatuses     []waiting `json:"containerStatuses"`
		} `json:"status"`
	}
	if json.Unmarshal(out, &pod) != nil {
		return ""
	}
	for _, s := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		if w := s.State.Waiting; w != nil && w.Reason != "" {
			return withMessage(fmt.Sprintf("container %s is waiting: %s", s.Name, w.Reason), w.Message)
		}
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == "PodScheduled" && cond.Status == "False" {
			return withMessage("not scheduled: "+cond.Reason, cond.Message)
		}
	}
	return ""
}

// withMessage adds the kubelet's or scheduler's message to what, on one line and cut to
// maxWaitMessage.
func withMessage(what, msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if msg == "" {
		return what
	}
	if r := []rune(msg); len(r) > maxWaitMessage {
		msg = string(r[:maxWaitMessage]) + "…"
	}
	return what + " (" + msg + ")"
}
