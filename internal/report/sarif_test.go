package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/Git-Hospital/helmbulance/internal/scenario"
)

var update = flag.Bool("update", false, "rewrite testdata/report.sarif from the current output")

// One finding of each severity, and the same ID twice in one scenario, for a chart in a directory
// under the working directory.
func sarifReport() *Report {
	return &Report{
		Chart: "./charts/app/",
		Results: []scenario.Result{
			{Scenario: "upgrade-without-values", Verdict: scenario.Fail, Findings: []scenario.Finding{{
				ID: "HB-UV-001", Title: "A Secret changes on an upgrade with no values", Severity: "high",
				Evidence: []string{"app/password changed"}, Impact: "Clients lose their password.",
				Fix: "Use lookup.", Reproduce: []string{"helm upgrade app ./charts/app"},
			}}},
			{Scenario: "k8s-1.37", Verdict: scenario.Fail, Findings: []scenario.Finding{
				{ID: "HB-K8S-003", Title: "The chart uses APIs that are deprecated or removed on Kubernetes 1.37", Evidence: []string{"policy/v1beta1 PodDisruptionBudget"}, Severity: "medium"},
				{ID: "HB-K8S-003", Title: "The chart uses APIs that are deprecated or removed on Kubernetes 1.37", Evidence: []string{"batch/v1beta1 CronJob"}, Severity: "medium"},
				{ID: "HB-K8S-004", Title: "The API server warns about objects this chart creates on Kubernetes 1.37", Severity: "low"},
			}},
			{Scenario: "scan", Verdict: scenario.Fail, Findings: []scenario.Finding{
				{ID: "HB-SCAN-003", Title: "Candidate: Secret values are generated anew on every upgrade", Severity: "candidate"},
			}},
			{Scenario: "reinstall", Verdict: scenario.Pass},
		},
	}
}

// The SARIF file is what code scanning reads, so its shape is pinned whole. Run with -update after
// a deliberate change and read the diff.
func TestSARIFGolden(t *testing.T) {
	golden, err := filepath.Abs(filepath.Join("testdata", "report.sarif"))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join("charts", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("charts", "app", "Chart.yaml"), []byte("name: app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := sarifReport().SARIF()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("SARIF differs from %s (run go test -update and read the diff):\n%s", golden, got)
	}
}

func TestSARIFLevels(t *testing.T) {
	for severity, want := range map[string]string{"high": "error", "medium": "warning", "low": "note", "candidate": "note"} {
		if got := level(severity); got != want {
			t.Errorf("level(%q) = %q, want %q", severity, got, want)
		}
	}
}

// A fingerprint must not change between runs of the same chart, and must tell apart two findings
// that code scanning would otherwise merge into one alert.
func TestSARIFFingerprints(t *testing.T) {
	results := func(r *Report) []sarifResult {
		data, err := r.SARIF()
		if err != nil {
			t.Fatal(err)
		}
		var log sarifLog
		if err := json.Unmarshal(data, &log); err != nil {
			t.Fatal(err)
		}
		return log.Runs[0].Results
	}
	first, again := results(sarifReport()), results(sarifReport())
	// Evidence differs from run to run (pod names, counts); the fingerprint must not follow it.
	changed := sarifReport()
	changed.Results[0].Findings[0].Evidence = []string{"app/password changed in pod app-7d9f"}
	moved := results(changed)
	seen := map[string]bool{}
	for i, r := range first {
		fp := r.PartialFingerprints[fingerprintKey]
		if fp == "" || fp != again[i].PartialFingerprints[fingerprintKey] || fp != moved[i].PartialFingerprints[fingerprintKey] {
			t.Errorf("result %d (%s): fingerprint %q is not stable across runs", i, r.RuleID, fp)
		}
		if seen[fp] {
			t.Errorf("result %d (%s): fingerprint %q is shared with another result", i, r.RuleID, fp)
		}
		seen[fp] = true
	}
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join("charts", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("charts", "app", "Chart.yaml"), []byte("name: app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	local, same := sarifReport(), sarifReport()
	same.Chart = "charts/app"
	if results(local)[0].PartialFingerprints[fingerprintKey] != results(same)[0].PartialFingerprints[fingerprintKey] {
		t.Error("./charts/app/ and charts/app give the same finding different fingerprints")
	}
	other := sarifReport()
	other.Chart = "./charts/other"
	if results(other)[0].PartialFingerprints[fingerprintKey] == first[0].PartialFingerprints[fingerprintKey] {
		t.Error("two charts give the same finding the same fingerprint")
	}
}

// Code scanning resolves a location against the repository root, so only a chart directory under
// the working directory gets one.
func TestSARIFChartLocation(t *testing.T) {
	// A chart next to the working directory exists, so only the ".." rule keeps it out.
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "app", "Chart.yaml"), []byte("name: app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(base, "work"))
	if err := os.MkdirAll(filepath.Join("charts", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("charts", "app", "Chart.yaml"), []byte("name: app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("app-1.0.0.tgz", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(filepath.Join("charts", "app"))
	if err != nil {
		t.Fatal(err)
	}
	for chart, want := range map[string]string{
		"charts/app":              "charts/app/Chart.yaml",
		"./charts/app/":           "charts/app/Chart.yaml",
		"charts/missing":          "",
		"app-1.0.0.tgz":           "",
		abs:                       "",
		"~/charts/app":            "",
		"../app":                  "",
		"bitnami/postgresql":      "",
		"oci://ghcr.io/x/chart":   "",
		"https://e.example/c.tgz": "",
	} {
		if got := chartYAML(chart); got != want {
			t.Errorf("chartYAML(%q) = %q, want %q", chart, got, want)
		}
	}
}

// A run with no findings still writes a valid file with an empty results list: code scanning reads
// that as "nothing found" and closes the alerts an earlier run opened.
func TestSARIFNoFindings(t *testing.T) {
	data, err := (&Report{Chart: "bitnami/postgresql", Results: []scenario.Result{{Scenario: "reinstall", Verdict: scenario.Pass}}}).SARIF()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"results": []`)) || !bytes.Contains(data, []byte(`"rules": []`)) {
		t.Errorf("want empty rules and results lists, got:\n%s", data)
	}
}
