// Command helmbulance runs day-two scenarios against a Helm chart in a disposable kind cluster.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/egezamb/helmbulance/internal/execx"
	"github.com/egezamb/helmbulance/internal/helm"
	"github.com/egezamb/helmbulance/internal/kube"
	"github.com/egezamb/helmbulance/internal/oracle"
	"github.com/egezamb/helmbulance/internal/report"
	"github.com/egezamb/helmbulance/internal/sandbox"
	"github.com/egezamb/helmbulance/internal/scenario"
)

// Exit codes.
const (
	exitClean      = 0
	exitFindings   = 1
	exitError      = 2
	exitIncomplete = 3 // a scenario could not finish, e.g. the chart did not install
)

var scenarios = []scenario.Scenario{
	scenario.UpgradeValues{},
	scenario.ChartUpgrade{},
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitError)
	}
	switch os.Args[1] {
	case "test":
		os.Exit(test(os.Args[2:]))
	case "list-scenarios":
		for _, s := range scenarios {
			fmt.Printf("%-24s %s\n", s.Name(), s.Description())
		}
	default:
		usage()
		os.Exit(exitError)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage:
  helmbulance test <chart> [-f values.yaml]... [--set key=value]... [flags]
  helmbulance list-scenarios
`)
}

func test(args []string) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var files, sets, only multiFlag
	fs.Var(&files, "f", "values file (repeatable)")
	fs.Var(&sets, "set", "install-time value, key=value (repeatable). Upgrades test what happens when it is left out.")
	fs.Var(&only, "scenario", "run only this scenario (repeatable)")
	cluster := fs.String("cluster", "", "reuse a sandbox cluster created earlier by helmbulance")
	keep := fs.Bool("keep-cluster", false, "keep the sandbox cluster after the run")
	nodeImage := fs.String("node-image", "", "kind node image")
	timeout := fs.Duration("timeout", 10*time.Minute, "timeout for each install or upgrade")
	out := fs.String("report-dir", "helmbulance-report", "where to write report.md and report.json")
	from := fs.String("from", "", "chart to upgrade from in chart-upgrade (repo/chart, oci://… or a path); defaults to the chart under test")
	fromVersion := fs.String("from-version", "", "chart version to upgrade from; enables the chart-upgrade scenario")

	// Accept the chart path before or after the flags.
	var chart string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		chart, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if chart == "" && fs.NArg() > 0 {
		chart = fs.Arg(0)
	}
	if chart == "" {
		usage()
		return exitError
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	selected, err := pick(only)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitError
	}
	if *fromVersion == "" && len(only) == 0 {
		selected = without(selected, "chart-upgrade")
	}

	runner := &execx.Runner{Env: os.Environ()}
	started := time.Now()

	var c *sandbox.Cluster
	if *cluster != "" {
		c, err = sandbox.Open(*cluster)
	} else {
		fmt.Println("Creating sandbox cluster…")
		c, err = sandbox.Create(ctx, runner, *nodeImage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitError
	}
	if !*keep && *cluster == "" {
		defer func() {
			fmt.Println("Deleting sandbox cluster…")
			if err := c.Delete(context.Background(), runner); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}()
	} else {
		fmt.Printf("Sandbox cluster: %s (reuse with --cluster %s)\n", c.Name, c.Name)
	}

	hasher, err := oracle.NewHasher()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitError
	}
	h := &helm.Client{Runner: runner, Kubeconfig: c.Kubeconfig, Timeout: *timeout}
	env := &scenario.Env{
		Runner: runner,
		Helm:   h,
		Kube:   &kube.Client{Runner: runner, Kubeconfig: c.Kubeconfig},
		Hasher: hasher,
		Chart:  chart,
		Values: helm.Values{Files: files, Sets: sets},
		Log:    os.Stderr,

		From:        *from,
		FromVersion: *fromVersion,
	}

	rep := &report.Report{
		SchemaVersion: report.SchemaVersion,
		Chart:         chart,
		ValuesFiles:   files,
		SetKeys:       keysOf(sets),
		HelmVersion:   h.Version(ctx),
		Cluster:       c.Name,
		Started:       started,
	}
	for _, s := range selected {
		fmt.Printf("▶ %s\n", s.Name())
		res := s.Run(ctx, env)
		fmt.Printf("  %s: %s\n", res.Verdict, res.Summary)
		rep.Results = append(rep.Results, res)
	}
	rep.Duration = time.Since(started).Round(time.Second).String()
	rep.Commands = runner.Transcript()

	if err := rep.Write(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitError
	}
	fmt.Printf("Report: %s/report.md\n", *out)

	switch rep.Worst() {
	case scenario.Fail:
		return exitFindings
	case scenario.Inconclusive:
		return exitIncomplete
	}
	return exitClean
}

func pick(names []string) ([]scenario.Scenario, error) {
	if len(names) == 0 {
		return scenarios, nil
	}
	var out []scenario.Scenario
	for _, n := range names {
		found := false
		for _, s := range scenarios {
			if s.Name() == n {
				out = append(out, s)
				found = true
			}
		}
		if !found {
			return nil, errors.New("unknown scenario: " + n)
		}
	}
	return out, nil
}

func keysOf(sets []string) []string {
	var keys []string
	for _, s := range sets {
		for _, part := range strings.Split(s, ",") {
			k, _, _ := strings.Cut(part, "=")
			keys = append(keys, k)
		}
	}
	return keys
}

func without(list []scenario.Scenario, name string) []scenario.Scenario {
	var out []scenario.Scenario
	for _, s := range list {
		if s.Name() != name {
			out = append(out, s)
		}
	}
	return out
}
