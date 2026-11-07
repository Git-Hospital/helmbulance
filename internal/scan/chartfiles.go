package scan

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// chartFiles returns the text files of a chart, keyed by their path from the chart's root as helm
// names them in "# Source:" lines: templates/x.yaml, charts/sub/templates/y.yaml. The chart may be
// a directory or a packaged .tgz, and subcharts vendored as charts/*.tgz are read from their
// archives, which is the normal layout after helm dependency build (#51). nil means the chart could
// not be read, such as a repository or OCI reference.
func chartFiles(chart string) map[string]string {
	fi, err := os.Stat(chart)
	if err != nil {
		return nil
	}
	files := map[string]string{}
	if fi.IsDir() {
		_ = filepath.WalkDir(chart, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(chart, p)
			rel = filepath.ToSlash(rel)
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			if strings.HasSuffix(rel, ".tgz") && strings.HasPrefix(rel, "charts/") {
				addArchive(files, path.Dir(rel)+"/", b, 0)
				return nil
			}
			files[rel] = string(b)
			return nil
		})
		return files
	}
	b, err := os.ReadFile(chart)
	if err != nil {
		return nil
	}
	addArchive(files, "", b, 0)
	return files
}

// maxArchiveFile caps one file read from a chart archive, and maxArchiveDepth the nesting of
// subchart archives, so a hostile archive cannot fill memory.
const (
	maxArchiveFile  = 4 << 20
	maxArchiveDepth = 3
)

// addArchive adds the files of a chart archive under prefix. A chart archive holds one top
// directory, the chart's name, which helm uses in "# Source:" paths, so a subchart lands under
// prefix + name; for the chart itself (prefix "") the top directory is dropped.
func addArchive(files map[string]string, prefix string, data []byte, depth int) {
	if depth > maxArchiveDepth {
		return
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return
		}
		if h.Typeflag != tar.TypeReg || h.Size > maxArchiveFile {
			continue
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if strings.HasPrefix(name, "../") || path.IsAbs(name) {
			continue
		}
		top, rest, ok := strings.Cut(name, "/")
		if !ok {
			continue
		}
		key := rest
		if prefix != "" {
			key = prefix + top + "/" + rest
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxArchiveFile))
		if err != nil {
			return
		}
		if strings.HasSuffix(rest, ".tgz") && strings.HasPrefix(rest, "charts/") {
			sub := path.Dir(key) + "/"
			addArchive(files, sub, b, depth+1)
			continue
		}
		files[key] = string(b)
	}
}

var (
	// action is one template action, {{ … }}, with its trim markers.
	action = regexp.MustCompile(`(?s)\{\{-?\s*(.*?)\s*-?\}\}`)
	// lookupCall is lookup used as a function: followed by an argument, not part of a word such
	// as nslookup.
	lookupCall = regexp.MustCompile(`(^|[^\w.])lookup\s+["(\$.]`)
)

// defines returns the body of every {{ define "name" }} in text up to its own {{ end }}, counting
// the if, range, with, block and define actions inside it, so an {{ if }}…{{ end }} before a lookup
// no longer cuts the body short (#51). Template comments are dropped.
func defines(text string) map[string]string {
	out := map[string]string{}
	locs := action.FindAllStringSubmatchIndex(text, -1)
	for i, loc := range locs {
		body := text[loc[2]:loc[3]]
		name, ok := defineName(body)
		if !ok {
			continue
		}
		depth := 1
		for _, l := range locs[i+1:] {
			switch keyword(text[l[2]:l[3]]) {
			case "if", "range", "with", "block", "define":
				depth++
			case "end":
				depth--
			}
			if depth == 0 {
				out[name] = withoutComments(text[loc[1]:l[0]])
				break
			}
		}
	}
	return out
}

func defineName(body string) (string, bool) {
	if keyword(body) != "define" {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(body, "define"))
	if len(rest) < 2 || rest[0] != '"' {
		return "", false
	}
	end := strings.IndexByte(rest[1:], '"')
	if end < 0 {
		return "", false
	}
	return rest[1 : end+1], true
}

// keyword is the first word of an action, "" for a comment.
func keyword(body string) string {
	if strings.HasPrefix(body, "/*") {
		return ""
	}
	f := strings.Fields(body)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

var comment = regexp.MustCompile(`(?s)\{\{-?\s*/\*.*?\*/\s*-?\}\}`)

func withoutComments(text string) string { return comment.ReplaceAllString(text, "") }
