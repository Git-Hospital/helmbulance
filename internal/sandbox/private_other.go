//go:build !unix

package sandbox

// checkPrivate has nothing to check where there are no Unix owners and modes: the state directory
// must then be one that only its user can write (#155).
func checkPrivate(state, clusters, dir string) error { return nil }
