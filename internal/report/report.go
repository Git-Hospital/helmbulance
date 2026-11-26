// Package report writes the results of a run as JSON (stable, for machines), Markdown (for
// people) and SARIF (for code scanning). None ever contains a Secret value.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Git-Hospital/helmbulance/internal/execx"
	"github.com/Git-Hospital/helmbulance/internal/scenario"
)

// SchemaVersion of the JSON report. Bump it on incompatible changes.
const SchemaVersion = 1

// Report is everything a run produced.
type Report struct {
	SchemaVersion int `json:"schemaVersion"`
	// Verdict is Worst(), the worst result of the run, so a reader of report.json does not have to
	// work it out. test and matrix exit by it. scan exits by it only with --strict: without it,
	// a FAIL is a candidate to confirm and the exit code is 0. Write sets it.
	Verdict     string   `json:"verdict"`
	Chart       string   `json:"chart"`
	ValuesFiles []string `json:"valuesFiles"`
	SetKeys     []string `json:"setKeys"` // keys only, never values
	// ExpectedRotation is what --expected-rotation allowed, so a reader sees what was not reported.
	ExpectedRotation []string          `json:"expectedRotation,omitempty"`
	HelmVersion      string            `json:"helmVersion"`
	Cluster          string            `json:"cluster"`
	Started          time.Time         `json:"started"`
	Duration         string            `json:"duration"`
	Results          []scenario.Result `json:"results"`
	Commands         []execx.Command   `json:"commands"`
	// Complete is false in the report written after each step while the run goes on, and true in
	// the one written at its end. A run killed from outside, such as at a CI job limit, leaves the
	// last partial report (#164).
	Complete bool `json:"complete"`

	// Hide masks secret values, URL credentials and local paths. Write applies it to every field,
	// so nothing reaches report.json or report.md around the runner's masking. It is idempotent.
	Hide func(string) string `json:"-"`
}

// Worst returns the overall verdict: FAIL beats INCONCLUSIVE beats PASS. SKIPPED counts for
// nothing, so a run in which every scenario was skipped tested nothing and is INCONCLUSIVE.
func (r *Report) Worst() string {
	passed, inconclusive := false, false
	for _, res := range r.Results {
		switch res.Verdict {
		case scenario.Fail:
			return scenario.Fail
		case scenario.Inconclusive:
			inconclusive = true
		case scenario.Pass:
			passed = true
		}
	}
	if inconclusive || !passed {
		return scenario.Inconclusive
	}
	return scenario.Pass
}

// Write saves report.json, report.md and report.sarif in dir.
func (r *Report) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Report files are shared. Mask credentials, local paths and secret values in one place, so no
	// field reaches any of the files around the runner's masking.
	r.redact()
	r.Verdict = r.Worst()
	// An empty list is [], not null, so a reader can iterate every list without a check.
	for _, list := range []*[]string{&r.ValuesFiles, &r.SetKeys} {
		if *list == nil {
			*list = []string{}
		}
	}
	if r.Results == nil {
		r.Results = []scenario.Result{}
	}
	if r.Commands == nil {
		r.Commands = []execx.Command{}
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(dir, "report.json", append(data, '\n')); err != nil {
		return err
	}
	if err := writeFile(dir, "report.md", []byte(r.Markdown())); err != nil {
		return err
	}
	sarif, err := r.SARIF()
	if err != nil {
		return err
	}
	return writeFile(dir, "report.sarif", sarif)
}

// writeFile replaces dir/name through a temporary file and a rename, so a run killed while it
// writes leaves the previous file whole, never half of the new one.
func writeFile(dir, name string, data []byte) error {
	f, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// redact masks every text field of the report. With no Hide func it falls back to redacting the
// chart's URL, as before. Idempotent, so transcript text the runner already masked is unchanged.
func (r *Report) redact() {
	hide := r.Hide
	if hide == nil {
		r.Chart = execx.RedactURLs(r.Chart)
		return
	}
	r.Chart = hide(r.Chart)
	for i, v := range r.ValuesFiles {
		r.ValuesFiles[i] = hide(v)
	}
	for i := range r.Commands {
		r.Commands[i].Line = hide(r.Commands[i].Line)
	}
	for i := range r.Results {
		res := &r.Results[i]
		res.Summary = hide(res.Summary)
		if res.PodSecurity != nil {
			for k, v := range res.PodSecurity.Violations {
				res.PodSecurity.Violations[k] = hide(v)
			}
		}
		for j := range res.Findings {
			f := &res.Findings[j]
			f.Title, f.Impact, f.Fix = hide(f.Title), hide(f.Impact), hide(f.Fix)
			for k, e := range f.Evidence {
				f.Evidence[k] = hide(e)
			}
			for k, c := range f.Reproduce {
				f.Reproduce[k] = hide(c)
			}
		}
	}
}

// Markdown renders the report for people.
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Helmbulance report: %s\n\n", r.Worst())
	if !r.Complete {
		b.WriteString("This report was written before the run finished. Steps marked \"not run yet\" had not started, and the run may have been stopped from outside.\n\n")
	}
	fmt.Fprintf(&b, "- Chart: `%s`\n", r.Chart)
	if len(r.ValuesFiles) > 0 {
		fmt.Fprintf(&b, "- Values files: `%s`\n", strings.Join(r.ValuesFiles, "`, `"))
	}
	if len(r.SetKeys) > 0 {
		fmt.Fprintf(&b, "- Install-time `--set` keys: `%s`\n", strings.Join(r.SetKeys, "`, `"))
	}
	if len(r.ExpectedRotation) > 0 {
		fmt.Fprintf(&b, "- Expected rotations (not reported): `%s`\n", strings.Join(r.ExpectedRotation, "`, `"))
	}
	fmt.Fprintf(&b, "- Helm: %s\n- Sandbox: kind cluster `%s`\n- Duration: %s\n\n", r.HelmVersion, r.Cluster, r.Duration)

	b.WriteString("| Scenario | Verdict | Summary |\n|---|---|---|\n")
	for _, res := range r.Results {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", res.Scenario, res.Verdict, escape(res.Summary))
	}

	// The Pod Security level is information about the chart, not a finding (#115).
	var levels []scenario.Result
	for _, res := range r.Results {
		if res.PodSecurity != nil {
			levels = append(levels, res)
		}
	}
	if len(levels) > 0 {
		b.WriteString("\n## Pod Security (information, not a finding)\n\nThe level the release's pods reach, as the API server rates them; nothing was enforced.\n\n")
		for _, res := range levels {
			ps := res.PodSecurity
			fmt.Fprintf(&b, "- `%s`: **%s**", res.Scenario, ps.Level)
			if len(ps.Violations) > 0 {
				next := map[string]string{"baseline": "restricted", "privileged": "baseline"}[ps.Level]
				var lines []string
				for _, v := range ps.Violations {
					lines = append(lines, escape(v))
				}
				fmt.Fprintf(&b, ". Not %s: %s", next, strings.Join(lines, "; "))
			}
			b.WriteString("\n")
		}
	}

	for _, res := range r.Results {
		for _, f := range res.Findings {
			fmt.Fprintf(&b, "\n## %s: %s\n\n", f.ID, escape(f.Title))
			fmt.Fprintf(&b, "**Severity:** %s · **Scenario:** `%s`\n\n", f.Severity, res.Scenario)
			b.WriteString("**Evidence**\n\n")
			for _, e := range f.Evidence {
				fmt.Fprintf(&b, "- %s\n", escape(e))
			}
			fence := codeFence(f.Reproduce)
			fmt.Fprintf(&b, "\n**Impact.** %s\n\n**Fix.** %s\n\n**Reproduce**\n\n%ssh\n", inline(f.Impact), inline(f.Fix), fence)
			for _, c := range f.Reproduce {
				b.WriteString(commandLine(c) + "\n")
			}
			b.WriteString(fence + "\n")
		}
	}
	return b.String()
}

// markdownText makes text inert in a Markdown table cell or list item: it cannot end the cell, start
// a line, or open HTML, and "](" and "][" become "]&#40;" and "]&#91;", so no inline or reference
// link or image ([text](url), ![](url), [text][ref]) can form, which the job summary would render
// and fetch. "]:" becomes "]&#58;": a link reference definition ([x]: url) is valid at the start of
// a list item, which every evidence line is, and would turn a shortcut ![x] anywhere in the report
// into an image. Brackets alone stay as they are: a field path such as containers[app].env[FOO] in a
// code span must read the same. A bare URL is still autolinked by GitHub; its text is the URL.
var markdownText = strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "\t", " ", "<", "&lt;", ">", "&gt;",
	"](", "]&#40;", "][", "]&#91;", "]:", "]&#58;")

// inline keeps text from the cluster from breaking a Markdown table or injecting markup. A lone
// carriage return is a line ending in CommonMark too, so it is turned into a space like a newline;
// otherwise a chart's `fail "x\r## Heading"` would start a heading in the report. Control
// characters and the bidirectional overrides that reorder what is shown are dropped: report.md is
// also read in a terminal. They are dropped before the replacer runs: a control between "]" and "("
// would otherwise hide the pair from it and join them again when dropped (review of #121). A
// leading "#" is escaped, since the text can start a list item, where "# x" is a heading.
func inline(s string) string {
	return leadingBlock(strings.TrimLeft(markdownText.Replace(dropControls(s)), " "))
}

// leadingBlock escapes what would start a block at the start of a list item or a cell: a heading
// (#), a list item (-, *, + or 1. with a space after it), a thematic break (---, ***, ___) or a
// code fence (``` or ~~~). Evidence is written as list items, where "- # x" is a heading in a
// nested list (#170). A code span at the start, as in our own evidence, stays as it is.
func leadingBlock(t string) string {
	if t == "" {
		return t
	}
	marker := func(i int) bool { return i == len(t) || t[i] == ' ' }
	switch c := t[0]; {
	case c == '#', strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
		return `\` + t
	case (c == '-' || c == '*' || c == '+') && marker(1):
		return `\` + t
	case (c == '-' || c == '*' || c == '_') && len(strings.ReplaceAll(t, " ", "")) >= 3 &&
		strings.Trim(t, " "+string(c)) == "":
		return `\` + t
	}
	i := 0
	for i < len(t) && i < 9 && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i > 0 && i < len(t) && (t[i] == '.' || t[i] == ')') && marker(i+1) {
		return t[:i] + `\` + t[i:]
	}
	return t
}

// commandLine keeps a reproduce command on one line of its code block: a values key the chart
// chose can hold a line break.
func commandLine(s string) string {
	return dropControls(s)
}

// dropControls turns a line break or tab into a space, so "x\n## heading" stays two words, and drops
// the other control characters, the bidirectional marks and overrides, the zero-width characters
// and the Unicode line and paragraph separators.
func dropControls(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0),
			r >= 0x200b && r <= 0x200f, r == 0x2028 || r == 0x2029,
			r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0xfeff:
			return -1
		}
		return r
	}, s)
}

// codeFence is a fence longer than any run of backticks in lines, so none of them closes the block.
// It counts them as commandLine writes them: a control between two backticks is dropped there, and
// would otherwise hide a longer run from the count (#170).
func codeFence(lines []string) string {
	longest := 2
	for _, l := range lines {
		run := 0
		for _, r := range commandLine(l) {
			if r != '`' {
				run = 0
				continue
			}
			if run++; run > longest {
				longest = run
			}
		}
	}
	return strings.Repeat("`", longest+1)
}

// escape is inline with a length limit, for text that comes from the cluster.
func escape(s string) string {
	s = inline(s)
	if len(s) > 500 {
		s = s[:500]
		// Do not cut a multi-byte rune in half, which would leave invalid UTF-8.
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	return s
}
