package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// SARIF 2.1.0, only the parts Helmbulance fills in. Code scanning reads this file; nothing uploads
// it yet (#99).
type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID                   string       `json:"id"`
	ShortDescription     sarifMessage `json:"shortDescription"`
	DefaultConfiguration sarifConfig  `json:"defaultConfiguration"`
}

type sarifConfig struct {
	Level string `json:"level"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	RuleIndex           int               `json:"ruleIndex"`
	Level               string            `json:"level"`
	Message             sarifMessage      `json:"message"`
	Locations           []sarifLocation   `json:"locations,omitempty"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Properties          sarifProperties   `json:"properties"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
}

type sarifArtifact struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId"`
}

// sarifProperties keeps what a code scanning alert has no field for, so the SARIF file says as
// much as report.json about each finding.
type sarifProperties struct {
	Scenario  string   `json:"scenario"`
	Severity  string   `json:"severity"`
	Evidence  []string `json:"evidence"`
	Fix       string   `json:"fix,omitempty"`
	Reproduce []string `json:"reproduce"`
}

// fingerprintKey names the partialFingerprints entry. Bump the version if what goes into the hash
// changes, so code scanning does not match old alerts to new ones by accident.
const fingerprintKey = "helmbulance/v1"

// SARIF renders the report's findings as SARIF 2.1.0: one rule per finding ID, one result per
// finding. Write calls it on the redacted report, so no field carries a secret value.
//
// A finding has no template line or values key yet (§3.6 asks for them), so a result points at
// the chart's Chart.yaml when the chart is a directory under the working directory, and has no
// location otherwise.
func (r *Report) SARIF() ([]byte, error) {
	run := sarifRun{
		Tool: sarifTool{Driver: sarifDriver{
			Name:           "Helmbulance",
			InformationURI: "https://github.com/Git-Hospital/helmbulance",
			Rules:          []sarifRule{},
		}},
		Results: []sarifResult{},
	}
	var locations []sarifLocation
	// "./charts/app/" and "charts/app" are one chart, so a local chart is named by its cleaned path.
	chart := r.Chart
	if uri := chartYAML(r.Chart); uri != "" {
		locations = []sarifLocation{{PhysicalLocation: sarifPhysical{
			ArtifactLocation: sarifArtifact{URI: uri, URIBaseID: "%SRCROOT%"},
		}}}
		chart = path.Dir(uri)
	}
	rules := map[string]int{}
	seen := map[string]int{}
	for _, res := range r.Results {
		for _, f := range res.Findings {
			idx, ok := rules[f.ID]
			if !ok {
				idx = len(run.Tool.Driver.Rules)
				rules[f.ID] = idx
				run.Tool.Driver.Rules = append(run.Tool.Driver.Rules, sarifRule{
					ID:                   f.ID,
					ShortDescription:     sarifMessage{Text: f.Title},
					DefaultConfiguration: sarifConfig{Level: level(f.Severity)},
				})
			}
			// The same ID can come up more than once in one scenario (one HB-K8S-003 per removed
			// API): the count keeps their fingerprints apart, and runs report them in the same order.
			n := seen[res.Scenario+"\x00"+f.ID]
			seen[res.Scenario+"\x00"+f.ID]++
			text := f.Title
			if f.Impact != "" {
				text += "\n\n" + f.Impact
			}
			run.Results = append(run.Results, sarifResult{
				RuleID:              f.ID,
				RuleIndex:           idx,
				Level:               level(f.Severity),
				Message:             sarifMessage{Text: text},
				Locations:           locations,
				PartialFingerprints: map[string]string{fingerprintKey: fingerprint(res.Scenario, f.ID, chart, n)},
				Properties: sarifProperties{
					Scenario:  res.Scenario,
					Severity:  f.Severity,
					Evidence:  nonNil(f.Evidence),
					Fix:       f.Fix,
					Reproduce: nonNil(f.Reproduce),
				},
			})
		}
	}
	data, err := json.MarshalIndent(sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs:    []sarifRun{run},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// level maps a finding's severity to a SARIF level. A candidate is a hypothesis that no cluster
// reproduced yet, so it is a note, like a low finding.
func level(severity string) string {
	switch severity {
	case "high":
		return "error"
	case "medium":
		return "warning"
	default:
		return "note"
	}
}

// fingerprint identifies a finding across runs of the same chart: the scenario, the finding ID,
// the chart (already redacted; a local one by its cleaned path) and which occurrence of that ID in the scenario it is.
// Nothing from the evidence goes in, since names and counts in it change from run to run.
func fingerprint(scen, id, chart string, n int) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{scen, id, chart, strconv.Itoa(n)}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// chartYAML returns the chart's Chart.yaml as a forward-slash path relative to the working
// directory, or "" when the chart is not a directory there: a repository, OCI or URL reference, a
// packaged .tgz, an absolute path or one outside the working directory. Code scanning resolves a
// location against the repository root, which is the working directory in the Action.
func chartYAML(chart string) string {
	if chart == "" || filepath.IsAbs(chart) || strings.HasPrefix(chart, "~") {
		return ""
	}
	clean := filepath.Clean(chart)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ""
	}
	if fi, err := os.Stat(filepath.Join(clean, "Chart.yaml")); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return path.Join(filepath.ToSlash(clean), "Chart.yaml")
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
