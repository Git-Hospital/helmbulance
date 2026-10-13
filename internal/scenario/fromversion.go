package scenario

import (
	"context"
	"fmt"
	"regexp"
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

// exactVersion is a full MAJOR.MINOR.PATCH, with an optional v, pre-release and build part.
var exactVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// isExactVersion reports whether v names one version rather than a range. helm reads a partial
// version such as 1.2 as a range and installs the newest match (1.2.7), so it is not exact.
func isExactVersion(v string) bool {
	return exactVersion.MatchString(v)
}

// toVersion is " to <version>" for the version release runs after an upgrade, or "" if helm cannot
// say.
func toVersion(ctx context.Context, env *Env, release, ns string) string {
	v, err := env.Helm.ReleaseVersion(ctx, release, ns)
	if err != nil {
		fmt.Fprintf(env.Log, "could not read the upgraded version: %v\n", err)
		return ""
	}
	return " to " + v
}
