package scan

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// chartFiles returns the templates of a chart and of its subcharts, keyed by their path from the
// chart's root as helm names them in "# Source:" lines: templates/x.yaml, charts/sub/templates/y.yaml.
// The chart may be a directory or a packaged .tgz, and subcharts vendored as charts/*.tgz are read
// from their archives, which is the normal layout after helm dependency build (#51). nil means the
// chart could not be read, such as a repository or OCI reference, or that it is over the limits
// below.
//
// The chart is untrusted: only regular files are read, so a FIFO or a link to a device cannot block
// or fill memory, and files helm never parses as templates are not read at all.
func chartFiles(chart string) map[string]string {
	if p, err := filepath.EvalSymlinks(chart); err == nil {
		chart = p
	}
	fi, err := os.Stat(chart)
	if err != nil {
		return nil
	}
	r := &chartReader{files: map[string]string{}, left: maxChartBytes, skip: maxSkippedBytes, inflate: maxChartBytes + maxSkippedBytes}
	if fi.IsDir() {
		_ = filepath.WalkDir(chart, func(p string, d os.DirEntry, err error) error {
			if r.over {
				return filepath.SkipAll
			}
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(chart, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel != "." && !templatePath.MatchString(rel+"/") && !chartsDir.MatchString(rel) {
					return filepath.SkipDir
				}
				return nil
			}
			tmpl, archive := templatePath.MatchString(rel), subchartArchive.MatchString(rel)
			if !tmpl && !archive {
				return nil
			}
			limit := int64(maxChartFile)
			if archive {
				limit = maxChartBytes
			}
			b, ok := r.readFile(p, limit)
			if !ok {
				return nil
			}
			if archive {
				r.addArchive(path.Dir(rel)+"/", b, 0)
				return nil
			}
			r.files[rel] = string(b)
			return nil
		})
	} else if b, ok := r.readFile(chart, maxChartBytes); ok {
		r.addArchive("", b, 0)
	}
	if r.over {
		return nil
	}
	return r.files
}

// maxChartFile caps one template, maxChartBytes all that is read from a chart and its archives,
// maxSkippedBytes the archive members passed over unread, which still have to be inflated, and
// maxArchiveDepth the nesting of subchart archives, so a hostile chart cannot fill memory or keep
// scan busy. A chart over them counts as unreadable, and then no Secret is left out.
const (
	maxChartFile    = 4 << 20
	maxChartBytes   = 64 << 20
	maxSkippedBytes = 1 << 30
	maxArchiveDepth = 3
)

var (
	// templatePath is a file helm parses as a template: under templates/ of the chart or of a
	// subchart, at any depth of charts/.
	templatePath = regexp.MustCompile(`^(charts/[^/]+/)*templates/`)
	// subchartArchive is a subchart vendored as an archive.
	subchartArchive = regexp.MustCompile(`^(charts/[^/]+/)*charts/[^/]+\.tgz$`)
	// chartsDir is a directory on the way to a subchart's templates.
	chartsDir = regexp.MustCompile(`^(charts/[^/]+/)*charts(/[^/]+)?$`)
)

type chartReader struct {
	files   map[string]string
	left    int64
	skip    int64
	inflate int64
	over    bool
}

// take counts n bytes read against what is left, and marks the chart over its limits when they
// do not fit.
func (r *chartReader) take(n, limit int64) bool {
	if n < 0 || n > limit || n > r.left {
		r.over = true
		return false
	}
	r.left -= n
	return true
}

// pass counts n bytes passed over in an archive.
func (r *chartReader) pass(n int64) bool {
	if n < 0 || n > r.skip {
		r.over = true
		return false
	}
	r.skip -= n
	return true
}

// inflated counts what is read from an archive's inflated stream against what may be inflated in
// all, read or passed over, and stops the read when it runs out.
type inflated struct {
	src io.Reader
	r   *chartReader
}

func (in *inflated) Read(p []byte) (int, error) {
	n, err := in.src.Read(p)
	in.r.inflate -= int64(n)
	if in.r.inflate < 0 {
		in.r.over = true
		return n, errTooLarge
	}
	return n, err
}

var errTooLarge = errors.New("chart archive inflates past the limit")

// readFile reads p if it is a regular file, following a link, and no larger than limit.
func (r *chartReader) readFile(p string, limit int64) ([]byte, bool) {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() || !r.take(fi.Size(), limit) {
		return nil, false
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, fi.Size()+1))
	if err != nil || int64(len(b)) > fi.Size() {
		r.over = true
		return nil, false
	}
	return b, true
}

// addArchive adds the templates of a chart archive under prefix. A chart archive holds one top
// directory, the chart's name, which helm uses in "# Source:" paths, so a subchart lands under
// prefix + name; for the chart itself (prefix "") the top directory is dropped. Members that are
// passed over count too, since reading past them inflates them.
func (r *chartReader) addArchive(prefix string, data []byte, depth int) {
	if depth > maxArchiveDepth {
		return
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return
	}
	// Every inflated byte counts, headers and long member names included, not only what the
	// headers say the members hold.
	tr := tar.NewReader(&inflated{gz, r})
	for !r.over {
		h, err := tr.Next()
		if err != nil {
			return
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		top, rest, ok := strings.Cut(name, "/")
		tmpl, archive := templatePath.MatchString(rest), subchartArchive.MatchString(rest)
		if h.Typeflag != tar.TypeReg || !ok || strings.HasPrefix(name, "../") || path.IsAbs(name) || !tmpl && !archive {
			r.pass(h.Size)
			continue
		}
		limit := int64(maxChartFile)
		if archive {
			limit = maxChartBytes
		}
		key := rest
		if prefix != "" {
			key = prefix + top + "/" + rest
		}
		if !r.take(h.Size, limit) || !r.take(int64(len(key)), maxChartFile) {
			return
		}
		b, err := io.ReadAll(io.LimitReader(tr, h.Size))
		if err != nil {
			return
		}
		if archive {
			r.addArchive(path.Dir(key)+"/", b, depth+1)
			continue
		}
		r.files[key] = string(b)
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
// no longer cuts the body short (#51). Template comments are dropped first, so an action inside one
// is not counted. One pass over the actions: Go templates allow define only at the top level, so
// the bodies do not overlap.
func defines(text string) map[string]string {
	text = withoutComments(text)
	out := map[string]string{}
	depth, start := 0, -1
	var name string
	for _, loc := range action.FindAllStringSubmatchIndex(text, -1) {
		body := text[loc[2]:loc[3]]
		switch keyword(body) {
		case "define":
			// A define is only valid at the top level, so one opener counted by mistake before it
			// does not hide it.
			depth, start = 1, -1
			if n, ok := defineName(body); ok {
				name, start = n, loc[1]
			}
		case "if", "range", "with", "block":
			depth++
		case "end":
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				out[name] = text[start:loc[0]]
				start = -1
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

// keyword is the word an action starts with, so {{if(.x)}} is an if too; "" for a comment or a
// variable.
func keyword(body string) string {
	if strings.HasPrefix(body, "/*") {
		return ""
	}
	return body[:len(body)-len(strings.TrimLeft(body, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"))]
}

var comment = regexp.MustCompile(`(?s)\{\{-?\s*/\*.*?\*/\s*-?\}\}`)

func withoutComments(text string) string { return comment.ReplaceAllString(text, "") }
