package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egezamb/helmbulance/internal/execx"
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

// A lone carriage return is a line ending in CommonMark, so a chart's fail message must not use it
// to start a heading in the report (review item 2).
func TestMarkdownEscapesCarriageReturn(t *testing.T) {
	r := &Report{Results: []scenario.Result{{
		Scenario: "chart-upgrade", Verdict: scenario.Fail,
		Findings: []scenario.Finding{{ID: "HB-CU-001", Title: "x", Evidence: []string{"bad value\r## Injected heading"}}},
	}}}
	md := r.Markdown()
	if strings.Contains(md, "\r") {
		t.Errorf("carriage return reached report.md:\n%q", md)
	}
	if strings.Contains(md, "## Injected heading") && !strings.Contains(md, "value ## Injected heading") {
		t.Errorf("carriage return started a new block:\n%s", md)
	}
}

// Every field Write persists goes through Hide, so no field leaks a secret value, URL credential or
// local path around the runner's masking (review item 1).
func TestWriteRedactsEveryField(t *testing.T) {
	runner := &execx.Runner{Secrets: []string{"setval"}, Paths: map[string]string{"/home/zztester": "~"}}
	r := &Report{
		Hide:        runner.Hide,
		Chart:       "https://u:cpw@charts.example.com/c.tgz",
		ValuesFiles: []string{"https://u:vpw@values.example.com/v.yaml", "/home/zztester/vals.yaml"},
		Commands:    []execx.Command{{Line: "helm install --set token=setval /home/zztester/chart"}},
		Results: []scenario.Result{{
			Scenario: "chart-upgrade", Verdict: scenario.Fail, Summary: "failed at /home/zztester/chart",
			Findings: []scenario.Finding{{
				ID: "HB-CU-001", Title: "x",
				Evidence:  []string{"fetch https://u:epw@e.example/a.tgz failed; chart /home/zztester/chart"},
				Reproduce: []string{"helm pull https://u:rpw@r.example/c.tgz"},
			}},
		}},
	}
	dir := t.TempDir()
	if err := r.Write(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"report.json", "report.md"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{"cpw", "vpw", "epw", "rpw", "setval", "zztester"} {
			if strings.Contains(string(b), leak) {
				t.Errorf("%s leaks %q:\n%s", name, leak, b)
			}
		}
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

// A chart's fail message reaches the evidence, and the job summary renders report.md: a Markdown
// link or image in it must stay text, and control characters must not reach report.md at all.
// Impact and Fix are escaped too, without the evidence's length cut.
func TestMarkdownNeutralisesLinksAndControls(t *testing.T) {
	long := strings.Repeat("x", 600)
	r := &Report{Results: []scenario.Result{{
		Scenario: "chart-upgrade",
		Verdict:  scenario.Fail,
		Findings: []scenario.Finding{{
			ID:    "HB-CU-001",
			Title: "Upgrading fails",
			Evidence: []string{
				"Error: [Re-run with your token](https://evil.example/login) ![p](https://evil.example/pixel.png)",
				`Error: \[escaped\](https://evil.example/2) [ref][r]`,
				"`Deployment/x spec.template.spec.containers[app].env[FOO].value`",
				"Error: a" + "\x1b]0;title" + "\x07" + "b " + "\u202egnp.exe" + "\u202c" + " c" + "\u009b" + "d",
			},
			Impact: "<img src=x>" + "\n## Not a heading",
			Fix:    "Set it. " + long,
		}},
	}}}
	md := r.Markdown()
	for _, bad := range []string{"![p](", "](https://evil.example/2)", "][r]", "\x1b", "\x07", "\u202e", "\u009b", "<img", "\n## Not a heading"} {
		if strings.Contains(md, bad) {
			t.Errorf("report.md contains %q:\n%s", bad, md)
		}
	}
	if !strings.Contains(md, "[Re-run with your token]&#40;https://evil.example/login)") {
		t.Errorf("the link text is not kept as text:\n%s", md)
	}
	if !strings.Contains(md, "`Deployment/x spec.template.spec.containers[app].env[FOO].value`") {
		t.Errorf("a field path in a code span was changed:\n%s", md)
	}
	if !strings.Contains(md, long) {
		t.Error("Fix was cut: only evidence has a length limit")
	}
}

// A reproduce line can carry a values key from the chart. A line break and a fence in it must not
// close the code block and let the rest render as Markdown.
func TestMarkdownKeepsReproduceInItsBlock(t *testing.T) {
	r := &Report{Results: []scenario.Result{{
		Scenario: "scan",
		Verdict:  scenario.Fail,
		Findings: []scenario.Finding{{
			ID:        "HB-SCAN-004",
			Title:     "x",
			Reproduce: []string{"helm template c --set-json 'a\n```\n![i](https://evil.example/r.png)=1'"},
		}},
	}}}
	md := r.Markdown()
	fences := 0
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "```") {
			fences++
		}
		if strings.HasPrefix(line, "![i]") {
			t.Errorf("the reproduce line left its code block:\n%s", md)
		}
	}
	if fences != 2 {
		t.Errorf("got %d fence lines, want the block's own 2:\n%s", fences, md)
	}
	if !strings.Contains(md, "````sh\n") {
		t.Errorf("the fence is not longer than the backticks inside:\n%s", md)
	}
}
