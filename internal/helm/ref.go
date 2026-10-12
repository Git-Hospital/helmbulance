package helm

import (
	"os"
	"path/filepath"
	"strings"
)

// IsLocal reports whether ref is a chart on disk (a directory or a packaged .tgz). helm itself
// trims the reference and prefers a local path over a repository name, so this matches what helm
// will install.
func IsLocal(ref string) bool {
	_, err := os.Stat(strings.TrimSpace(ref))
	return err == nil
}

// IsPath reports whether helm reads ref only as a path, never as a repository, OCI or URL
// reference: it is absolute, starts with '.' or '~', or has neither a '/' nor a scheme. A ref like
// this that is not on disk is a typo, not a remote chart.
func IsPath(ref string) bool {
	ref = strings.TrimSpace(ref)
	return filepath.IsAbs(ref) || strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "~") ||
		!strings.Contains(ref, "/")
}
