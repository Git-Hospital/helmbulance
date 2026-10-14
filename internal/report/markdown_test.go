package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egezamb/helmbulance/internal/scenario"
)

// Evidence quotes helm and the cluster, so a chart controls part of it. It must not reach
// report.md as raw HTML, or split one evidence item over several lines.
func TestMarkdownEscapesEvidence(t *testing.T) {
	r := &Report{Results: []scenario.Result{{
		Scenario: "chart-upgrade",
		Verdict:  scenario.Fail,
		Findings: []scenario.Finding{{
			ID:       "HB-CU-001",
			Title:    "Upgrading fails",
			Evidence: []string{"Error: <img src=x onerror=alert(1)>\n## Not a heading"},
		}},
	}}}
	md := r.Markdown()
	if strings.Contains(md, "<img") {
		t.Errorf("raw HTML from evidence in report.md:\n%s", md)
	}
	if strings.Contains(md, "\n## Not a heading") {
		t.Errorf("evidence started a new Markdown line:\n%s", md)
	}
	if !strings.Contains(md, "- Error: &lt;img src=x onerror=alert(1)&gt; ## Not a heading\n") {
		t.Errorf("escaped evidence missing:\n%s", md)
	}
}

// A chart URL can carry credentials; report files are shared, the terminal is not.
func TestWriteHidesChartURLCredentials(t *testing.T) {
	dir := t.TempDir()
	r := &Report{Chart: "https://u:hunter2@charts.example.com/c-1.0.0.tgz"}
	if err := r.Write(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"report.json", "report.md"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "hunter2") {
			t.Errorf("%s contains the chart URL's password:\n%s", name, b)
		}
	}
}
