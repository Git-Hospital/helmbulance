package scan

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
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

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
