package scenario

import (
	"context"
	"fmt"
	"strings"
)

// installedFrom returns the chart version release runs, and an error when --from-version is an
// exact version and the release runs another one. A constraint such as ~1.2 is resolved by helm,
// so any version it picked is accepted.
func installedFrom(ctx context.Context, env *Env, release, ns string) (string, error) {
	v, err := env.Helm.ReleaseVersion(ctx, release, ns)
	if err != nil {
		return "", err
	}
	want := env.FromVersion
	if isExactVersion(want) && strings.TrimPrefix(v, "v") != strings.TrimPrefix(want, "v") {
		return v, fmt.Errorf("the release runs %s, not --from-version %s", v, want)
	}
	return v, nil
}

// isExactVersion reports whether v names one version rather than a range.
func isExactVersion(v string) bool {
	return v != "" && !strings.ContainsAny(v, "^~<>=*xX ,|")
}
