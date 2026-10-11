package helm

import "os"

// IsLocal reports whether ref is a chart on disk (a directory or a packaged .tgz). helm itself
// prefers a local path over a repository name, so this matches what helm will install.
func IsLocal(ref string) bool {
	_, err := os.Stat(ref)
	return err == nil
}
