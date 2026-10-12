package scenario

import (
	"context"
	"strings"
)

// notTheChart lists error texts that come from the run, not from the chart: helm's --wait timing
// out (Helm 3 and 4 word it differently), a killed process, an image that could not be pulled, a
// registry rate limit, or a cluster that did not answer.
var notTheChart = []string{
	"context deadline exceeded",
	"timed out waiting for the condition",
	"signal: killed",
	"errimagepull",
	"imagepullbackoff",
	"toomanyrequests",
	"connection refused",
	"i/o timeout",
	"tls handshake timeout",
}

// IsEnvironmentError reports whether err says more about the run than about the chart: the run was
// cancelled or timed out, or the error is one of notTheChart. Such an error makes a result
// INCONCLUSIVE, never a finding.
func IsEnvironmentError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range notTheChart {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
