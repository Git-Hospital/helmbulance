// Package sandbox creates and removes the disposable kind clusters that scenarios run in.
//
// Helmbulance only ever talks to clusters it created. Each one gets its own kubeconfig under the
// state directory (never the user's ~/.kube/config), and the SHA-256 of the cluster's CA is
// recorded at creation. Reusing a cluster checks that fingerprint on the cluster the kubeconfig's
// current context points at, which is where helm and kubectl go, so a kubeconfig pointing at
// some other cluster is refused.
package sandbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/egezamb/helmbulance/internal/execx"
)

// NamePrefix marks clusters created by Helmbulance.
const NamePrefix = "helmbulance-"

// nameRE is the only name NewCluster makes. Checking it keeps a --cluster value from naming a
// directory outside the state directory (#47).
var nameRE = regexp.MustCompile(`^` + NamePrefix + `[0-9a-f]{8}$`)

// Cluster is a sandbox cluster and the files that describe it.
type Cluster struct {
	Name       string
	Kubeconfig string
	dir        string
}

// StateDir is where cluster kubeconfigs and fingerprints are kept.
func StateDir() (string, error) {
	if d := os.Getenv("HELMBULANCE_STATE_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helmbulance"), nil
}

// NewCluster reserves a cluster with a random name and its own state directory, before any kind
// command runs. The caller prints the name first, so a Ctrl-C during creation still leaves it on
// screen for `kind delete cluster --name`.
func NewCluster() (*Cluster, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return nil, err
	}
	c, err := clusterFiles(NamePrefix + hex.EncodeToString(suffix))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return nil, err
	}
	return c, nil
}

// Create brings up the reserved cluster with kind and records its CA fingerprint. On failure it
// deletes the cluster and its state directory: the caller only deletes a cluster that Create
// returned without error.
func (c *Cluster) Create(ctx context.Context, r *execx.Runner, nodeImage string) error {
	args := []string{"create", "cluster", "--name", c.Name, "--kubeconfig", c.Kubeconfig, "--wait", "180s"}
	if nodeImage != "" {
		args = append(args, "--image", nodeImage)
	}
	_, err := r.Run(ctx, "kind", args...)
	if err == nil {
		err = c.record()
	}
	if err != nil {
		// kind can fail after its node container started, most often because a Ctrl-C reached it:
		// kind has no signal handler, so it dies before its own delete-on-failure and leaves a
		// privileged container running (#54). kind delete succeeds on a cluster that does not
		// exist, so it runs on every failure, without cancellation so it still runs after Ctrl-C.
		if derr := c.Delete(context.WithoutCancel(ctx), r); derr != nil {
			return errors.Join(err, derr)
		}
		return err
	}
	return nil
}

// record makes the new kubeconfig private and saves its CA fingerprint.
func (c *Cluster) record() error {
	if err := os.Chmod(c.Kubeconfig, 0o600); err != nil {
		return err
	}
	fp, err := caFingerprint(c.Kubeconfig, c.Name)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.dir, "ca.sha256"), []byte(fp), 0o600)
}

// Open returns an existing cluster that Helmbulance created, after checking its CA fingerprint.
func Open(name string) (*Cluster, error) {
	if !nameRE.MatchString(name) {
		return nil, fmt.Errorf("cluster %q was not created by helmbulance (name must be %q and 8 hex characters)", name, NamePrefix)
	}
	c, err := clusterFiles(name)
	if err != nil {
		return nil, err
	}
	want, err := os.ReadFile(filepath.Join(c.dir, "ca.sha256"))
	if err != nil {
		return nil, fmt.Errorf("no record of cluster %q in %s: %w", name, c.dir, err)
	}
	got, err := caFingerprint(c.Kubeconfig, name)
	if err != nil {
		return nil, err
	}
	if got != string(want) {
		return nil, errors.New("cluster CA does not match the one recorded at creation; refusing to use it")
	}
	return c, nil
}

// Delete removes the cluster and its files.
func (c *Cluster) Delete(ctx context.Context, r *execx.Runner) error {
	if _, err := r.Run(ctx, "kind", "delete", "cluster", "--name", c.Name, "--kubeconfig", c.Kubeconfig); err != nil {
		return err
	}
	return os.RemoveAll(c.dir)
}

func clusterFiles(name string) (*Cluster, error) {
	state, err := StateDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(state, "clusters", name)
	return &Cluster{Name: name, Kubeconfig: filepath.Join(dir, "kubeconfig"), dir: dir}, nil
}

// kubeconfig holds the fields of a kubeconfig that decide which cluster helm and kubectl reach.
type kubeconfig struct {
	CurrentContext string         `yaml:"current-context"`
	Contexts       []namedContext `yaml:"contexts"`
	Clusters       []namedCluster `yaml:"clusters"`
}

type namedContext struct {
	Name    string `yaml:"name"`
	Context struct {
		Cluster string `yaml:"cluster"`
	} `yaml:"context"`
}

type namedCluster struct {
	Name    string `yaml:"name"`
	Cluster struct {
		CAData string `yaml:"certificate-authority-data"`
	} `yaml:"cluster"`
}

// caFingerprint hashes the CA of the cluster that the kubeconfig's current context points at. helm
// and kubectl follow current-context, so that is the cluster the run would reach; another cluster
// merged into the file, as a cloud credentials helper does, must not be the one checked (#47). kind
// names the context kind-<name>, so any other current context is refused. For a file kind wrote,
// with a single cluster, the hash is the same as that of its only CA line.
func caFingerprint(path, name string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var kc kubeconfig
	if err := yaml.Unmarshal(data, &kc); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if want := "kind-" + name; kc.CurrentContext != want {
		return "", fmt.Errorf("%s: current-context is %q, not the sandbox's %q; refusing to use it", path, kc.CurrentContext, want)
	}
	ctxEntry, err := only(kc.Contexts, kc.CurrentContext, func(e namedContext) string { return e.Name })
	if err != nil {
		return "", fmt.Errorf("%s: context %w", path, err)
	}
	cluster := ctxEntry.Context.Cluster
	clusterEntry, err := only(kc.Clusters, cluster, func(e namedCluster) string { return e.Name })
	if err != nil {
		return "", fmt.Errorf("%s: cluster %w", path, err)
	}
	ca := strings.TrimSpace(clusterEntry.Cluster.CAData)
	if ca == "" {
		return "", fmt.Errorf("%s: cluster %q has no certificate-authority-data", path, cluster)
	}
	sum := sha256.Sum256([]byte(ca))
	return hex.EncodeToString(sum[:]), nil
}

// only returns the single item called name. A missing name, or one given twice, leaves it unclear
// what helm and kubectl would use, so both are errors.
func only[T any](items []T, name string, nameOf func(T) string) (T, error) {
	var item T
	found := 0
	for _, it := range items {
		if nameOf(it) == name {
			item, found = it, found+1
		}
	}
	if found != 1 {
		return item, fmt.Errorf("%q appears %d times, want once", name, found)
	}
	return item, nil
}
