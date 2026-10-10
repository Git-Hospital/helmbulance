// Package kube reads cluster state through kubectl, always with the sandbox's own kubeconfig.
package kube

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/egezamb/helmbulance/internal/execx"
)

// Client reads one sandbox cluster.
type Client struct {
	Runner     *execx.Runner
	Kubeconfig string
}

// Secret is the data of one Secret. Values stay in memory and are never written anywhere.
type Secret struct {
	Name string
	Data map[string][]byte
}

// skippedSecretTypes are Secrets that change for reasons unrelated to the chart.
var skippedSecretTypes = map[string]bool{
	"helm.sh/release.v1":                  true, // Helm's own release record, rewritten on every upgrade
	"kubernetes.io/service-account-token": true,
}

// Secrets returns every Secret in ns that belongs to the chart, sorted by name.
func (c *Client) Secrets(ctx context.Context, ns string) ([]Secret, error) {
	out, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "--namespace", ns, "get", "secrets", "-o", "json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Type string            `json:"type"`
			Data map[string]string `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse secrets: %w", err)
	}
	var secrets []Secret
	for _, item := range list.Items {
		if skippedSecretTypes[item.Type] {
			continue
		}
		s := Secret{Name: item.Metadata.Name, Data: map[string][]byte{}}
		for k, v := range item.Data {
			b, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return nil, fmt.Errorf("secret %s key %s: %w", s.Name, k, err)
			}
			s.Data[k] = b
		}
		secrets = append(secrets, s)
	}
	sort.Slice(secrets, func(i, j int) bool { return secrets[i].Name < secrets[j].Name })
	return secrets, nil
}

// SecretConsumers maps "secret/key" to the workloads (kind/name) whose pod template reads it
// through env, envFrom or a volume. Used to explain the impact of a changed key.
func (c *Client) SecretConsumers(ctx context.Context, ns string) (map[string][]string, error) {
	out, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "--namespace", ns,
		"get", "deployments,statefulsets,daemonsets", "-o", "json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Template struct {
					Spec podSpec `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse workloads: %w", err)
	}
	consumers := map[string][]string{}
	add := func(ref, workload string) {
		for _, w := range consumers[ref] {
			if w == workload {
				return
			}
		}
		consumers[ref] = append(consumers[ref], workload)
	}
	for _, item := range list.Items {
		workload := item.Kind + "/" + item.Metadata.Name
		spec := item.Spec.Template.Spec
		for _, ctr := range append(spec.InitContainers, spec.Containers...) {
			for _, e := range ctr.Env {
				if ref := e.ValueFrom.SecretKeyRef; ref.Name != "" {
					add(ref.Name+"/"+ref.Key, workload)
				}
			}
			for _, e := range ctr.EnvFrom {
				if e.SecretRef.Name != "" {
					add(e.SecretRef.Name+"/*", workload)
				}
			}
		}
		for _, v := range spec.Volumes {
			if v.Secret.SecretName != "" {
				add(v.Secret.SecretName+"/*", workload)
			}
		}
	}
	return consumers, nil
}

type podSpec struct {
	InitContainers []container `json:"initContainers"`
	Containers     []container `json:"containers"`
	Volumes        []struct {
		Secret struct {
			SecretName string `json:"secretName"`
		} `json:"secret"`
	} `json:"volumes"`
}

type container struct {
	Env []struct {
		ValueFrom struct {
			SecretKeyRef struct {
				Name string `json:"name"`
				Key  string `json:"key"`
			} `json:"secretKeyRef"`
		} `json:"valueFrom"`
	} `json:"env"`
	EnvFrom []struct {
		SecretRef struct {
			Name string `json:"name"`
		} `json:"secretRef"`
	} `json:"envFrom"`
}

// DeleteNamespace removes ns and waits until it is gone.
func (c *Client) DeleteNamespace(ctx context.Context, ns string) error {
	_, err := c.Runner.Run(ctx, "kubectl", "--kubeconfig", c.Kubeconfig, "delete", "namespace", ns,
		"--ignore-not-found", "--wait=true", "--timeout=5m")
	return err
}
