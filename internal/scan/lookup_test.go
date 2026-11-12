package scan

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tgz packs files under a top directory, as helm package does.
func tgz(t *testing.T, top string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, text := range files {
		if err := tw.WriteHeader(&tar.Header{Name: top + "/" + name, Mode: 0o644, Size: int64(len(text)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const guarded = `{{- $s := lookup "v1" "Secret" .Release.Namespace "db" }}`

// The cases of #51 where a lookup was missed, so a guarded Secret was a false candidate, and those
// where text that is not a lookup call exempted an unguarded one.
func TestLookupChecker(t *testing.T) {
	secret := "templates/secret.yaml"
	for name, c := range map[string]struct {
		files map[string]string
		want  bool
	}{
		"in the Secret's own file": {map[string]string{secret: guarded}, true},
		"in a library subchart's helper": {map[string]string{
			secret:                             `{{ include "common.secret" . }}`,
			"charts/common/templates/_lib.tpl": `{{- define "common.secret" -}}` + guarded + `{{- end -}}`,
		}, true},
		"through a helper that includes another": {map[string]string{
			secret:                   `{{ include "app.a" . }}`,
			"templates/_helpers.tpl": `{{- define "app.a" -}}{{ include "app.b" . }}{{- end -}}{{- define "app.b" -}}` + guarded + `{{- end -}}`,
		}, true},
		"after an inner end in the helper": {map[string]string{
			secret:                   `{{ include "app.a" . }}`,
			"templates/_helpers.tpl": `{{- define "app.a" -}}{{ if .Values.x }}x{{ end }}` + guarded + `{{- end -}}`,
		}, true},
		"only in a comment":    {map[string]string{secret: `{{/* lookup "v1" "Secret" */}}{{ randAlphaNum 8 }}`}, false},
		"nslookup in a script": {map[string]string{secret: `script: nslookup db.example`}, false},
		"no lookup":            {map[string]string{secret: `{{ randAlphaNum 8 }}`}, false},
	} {
		t.Run(name, func(t *testing.T) {
			chart := t.TempDir()
			writeFiles(t, chart, c.files)
			uses, readable := lookupChecker(chart)
			if !readable {
				t.Fatal("a chart directory was not readable")
			}
			if got := uses("app/" + secret); got != c.want {
				t.Errorf("uses lookup = %v, want %v", got, c.want)
			}
		})
	}
}

// A subchart vendored as charts/*.tgz, the layout after helm dependency build, and a chart scanned
// as a .tgz are read from their archives (#51).
func TestLookupCheckerReadsArchives(t *testing.T) {
	chart := t.TempDir()
	writeFiles(t, chart, map[string]string{"templates/secret.yaml": `{{ include "common.secret" . }}`})
	sub := tgz(t, "common", map[string]string{"templates/_lib.tpl": `{{- define "common.secret" -}}` + guarded + `{{- end -}}`})
	if err := os.MkdirAll(filepath.Join(chart, "charts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chart, "charts", "common-2.0.0.tgz"), sub, 0o644); err != nil {
		t.Fatal(err)
	}
	if uses, _ := lookupChecker(chart); !uses("app/templates/secret.yaml") {
		t.Error("a lookup in a vendored subchart archive was missed")
	}
	if files := chartFiles(chart); files["charts/common/templates/_lib.tpl"] == "" {
		t.Errorf("subchart files are not under charts/<name>/: %v", keys(files))
	}

	pkg := filepath.Join(t.TempDir(), "app-1.0.0.tgz")
	if err := os.WriteFile(pkg, tgz(t, "app", map[string]string{"templates/secret.yaml": guarded}), 0o644); err != nil {
		t.Fatal(err)
	}
	uses, readable := lookupChecker(pkg)
	if !readable || !uses("app/templates/secret.yaml") {
		t.Errorf("a chart scanned as a .tgz: readable %v, lookup found %v", readable, uses("app/templates/secret.yaml"))
	}
	if _, readable := lookupChecker("oci://registry.example/app"); readable {
		t.Error("a remote reference counts as readable")
	}
}

// An archive member that climbs out of the chart is skipped.
func TestChartFilesSkipsEscapingPaths(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"app/../../etc/passwd", "app/templates/ok.yaml"} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte("x\n"))
	}
	_ = tw.Close()
	_ = gz.Close()
	pkg := filepath.Join(t.TempDir(), "app.tgz")
	if err := os.WriteFile(pkg, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	files := chartFiles(pkg)
	if len(files) != 1 || files["templates/ok.yaml"] == "" {
		t.Errorf("files = %v, want only templates/ok.yaml", keys(files))
	}
}

// The chart is untrusted. Files helm never parses as templates are not read, and what is read stays
// within the limits: a chart over them is unreadable rather than half read.
func TestChartFilesReadsOnlyTemplatesWithinLimits(t *testing.T) {
	chart := t.TempDir()
	writeFiles(t, chart, map[string]string{
		"templates/secret.yaml": guarded,
		"files/doc.txt":         `{{ define "x" }}{{ end }}`,
		"README.md":             "readme",
	})
	if files := chartFiles(chart); len(files) != 1 || files["templates/secret.yaml"] == "" {
		t.Errorf("files = %v, want only templates/secret.yaml", keys(files))
	}

	big := t.TempDir()
	writeFiles(t, big, map[string]string{"templates/big.yaml": strings.Repeat("x", maxChartFile+1)})
	if files := chartFiles(big); files != nil {
		t.Errorf("a template over the file limit: files = %v, want the chart unreadable", keys(files))
	}

	// A file helm accepts but never parses is passed over, even over the template limit.
	pkg := filepath.Join(t.TempDir(), "app.tgz")
	blob := map[string]string{"templates/secret.yaml": guarded, "files/blob.bin": strings.Repeat("\x00", maxChartFile+1)}
	if err := os.WriteFile(pkg, tgz(t, "app", blob), 0o644); err != nil {
		t.Fatal(err)
	}
	if files := chartFiles(pkg); len(files) != 1 || files["templates/secret.yaml"] == "" {
		t.Errorf("an archive with a large file that is not a template: files = %v, want templates/secret.yaml", keys(files))
	}

	// Members passed over count too: one that claims more than the archive may inflate makes the
	// chart unreadable before anything is inflated.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "app/templates/secret.yaml", Mode: 0o644, Size: int64(len(guarded)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(guarded))
	_ = tw.WriteHeader(&tar.Header{Name: "app/files/bomb", Mode: 0o644, Size: maxSkippedBytes + 1, Typeflag: tar.TypeReg})
	_ = tw.Flush()
	_ = gz.Close()
	if err := os.WriteFile(pkg, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if files := chartFiles(pkg); files != nil {
		t.Errorf("an archive over the inflate limit: files = %v, want the chart unreadable", keys(files))
	}
}

// What the headers hold counts too: long member names cost memory as map keys when they are kept,
// and inflating when they are passed over, though each member says it holds nothing.
func TestChartFilesCountsMemberNames(t *testing.T) {
	long := func(dir string, n int) []byte {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for i := range n {
			name := fmt.Sprintf("app/%s/%d%s", dir, i, strings.Repeat("a", 512<<10))
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
		}
		_ = tw.Close()
		_ = gz.Close()
		return buf.Bytes()
	}
	pkg := filepath.Join(t.TempDir(), "app.tgz")
	if err := os.WriteFile(pkg, long("templates", maxChartBytes/(512<<10)+8), 0o644); err != nil {
		t.Fatal(err)
	}
	if files := chartFiles(pkg); files != nil {
		t.Errorf("templates whose names add up past the limit: %d files, want the chart unreadable", len(files))
	}
	r := &chartReader{files: map[string]string{}, left: maxChartBytes, skip: maxSkippedBytes, inflate: 1 << 20}
	r.addArchive("", long("files", 8), 0)
	if !r.over {
		t.Error("members passed over whose headers inflate past the limit: want the chart over its limits")
	}
}

// Following includes reads each helper once, on its own: a long chain of helpers that include the
// next one used to copy everything found so far for each step.
func TestLookupCheckerFollowsALongChainInLinearTime(t *testing.T) {
	var b strings.Builder
	const n = 40000
	for i := range n {
		fmt.Fprintf(&b, "{{- define \"h%d\" -}}{{ include \"h%d\" . }}{{- end -}}\n", i, i+1)
	}
	fmt.Fprintf(&b, "{{- define \"h%d\" -}}%s{{- end -}}\n", n, guarded)
	chart := t.TempDir()
	writeFiles(t, chart, map[string]string{"templates/_h.tpl": b.String(), "templates/secret.yaml": `{{ include "h0" . }}`})
	uses, _ := lookupChecker(chart)
	start := time.Now()
	if !uses("app/templates/secret.yaml") {
		t.Error("the lookup at the end of the chain was missed")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("following %d includes took %v", n, d)
	}
}

// A define's body is found in one pass. Text with many unterminated defines used to be scanned to
// its end once for each of them.
func TestDefinesIsLinear(t *testing.T) {
	text := strings.Repeat(`{{ define "x" }}{{ if .a }}`, 50000)
	start := time.Now()
	defines(text)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("defines took %v on %d bytes", d, len(text))
	}
}

// When several templates define the same name, the result is the same every time, and a lookup
// counts only when every definition has it: which one helm renders also depends on .helmignore and
// subchart conditions.
func TestLookupCheckerWithTheSameHelperInSeveralFiles(t *testing.T) {
	plain := `{{- define "x.pw" -}}{{ randAlphaNum 8 }}{{- end -}}`
	withLookup := `{{- define "x.pw" -}}` + guarded + `{{- end -}}`
	throughHelper := `{{- define "x.pw" -}}{{ include "x.get" . }}{{- end -}}{{- define "x.get" -}}` + guarded + `{{- end -}}`
	for name, c := range map[string]struct {
		files map[string]string
		want  bool
	}{
		// helm renders the chart's, but whichever it renders, one of them has no lookup.
		"the chart's and a subchart's disagree": {map[string]string{
			"templates/_a.tpl":            plain,
			"charts/sub/templates/_a.tpl": withLookup,
		}, false},
		// helm renders _a.tpl, unless .helmignore leaves it out, which is not read here.
		"two at the same depth disagree": {map[string]string{
			"templates/_a.tpl": withLookup,
			"templates/_b.tpl": plain,
		}, false},
		"a lookup through another helper in one of them only": {map[string]string{
			"templates/_a.tpl": throughHelper,
			"templates/_b.tpl": plain,
		}, false},
		"every definition calls lookup": {map[string]string{
			"templates/_a.tpl":            withLookup,
			"charts/sub/templates/_a.tpl": throughHelper,
		}, true},
		"a .gotmpl file is a template": {map[string]string{
			"templates/_0.gotmpl": withLookup,
		}, true},
		"a define outside templates/ is not": {map[string]string{
			"templates/_a.tpl": plain,
			"files/doc.txt":    withLookup,
		}, false},
		// text/template keeps the define it has when a later one is empty.
		"an empty define does not replace a subchart's": {map[string]string{
			"templates/_a.tpl":            `{{- define "x.pw" -}}{{/* nothing */}} {{- end -}}`,
			"charts/sub/templates/_a.tpl": withLookup,
		}, true},
		"an empty define does not replace one at the same depth": {map[string]string{
			"templates/_a.tpl": `{{- define "x.pw" -}}{{- end -}}`,
			"templates/_b.tpl": withLookup,
		}, true},
	} {
		t.Run(name, func(t *testing.T) {
			chart := t.TempDir()
			c.files["templates/secret.yaml"] = `{{ include "x.pw" . }}`
			writeFiles(t, chart, c.files)
			for range 20 {
				if uses, _ := lookupChecker(chart); uses("app/templates/secret.yaml") != c.want {
					t.Fatalf("uses lookup = %v, want %v", !c.want, c.want)
				}
			}
		})
	}
}

// An action inside a template comment does not count for the nesting.
func TestDefinesSkipsActionsInComments(t *testing.T) {
	text := "{{- define \"x\" -}}{{- /*\n{{ if .x }}\n{{ end }}\n*/ -}}" + guarded + "{{- end -}}"
	if body := defines(text)["x"]; !lookupCall.MatchString(body) {
		t.Errorf("body of x = %q, want it to run past the comment to the lookup", body)
	}
	if got := keyword("if(.x)"); got != "if" {
		t.Errorf("keyword(%q) = %q, want if", "if(.x)", got)
	}
	// An opener counted by mistake, from braces in a string, does not hide the next define.
	text = `{{ $s := "}}{{ if" }}{{- define "x.pw" -}}` + guarded + `{{- end -}}`
	if body := defines(text)["x.pw"]; !lookupCall.MatchString(body) {
		t.Errorf("body of x.pw = %q, want the define after the stray if found", body)
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
