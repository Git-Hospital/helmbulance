package scenario

import (
	"context"
	"fmt"
	"time"
)

// Settle timings. Variables, not constants, so tests can shorten them.
var (
	settleTimeout = 90 * time.Second // how long problems may take to clear
	settleWindow  = 30 * time.Second // how long it must then stay clean
	settlePoll    = 10 * time.Second
)

// Settle calls check until it has returned no problems for settleWindow, and returns nil, or
// until settleTimeout has passed with problems, and returns them. helm --wait returns as soon as
// pods are ready, so a pod that crashes seconds later is only seen by watching a while longer.
//
// check returns an error when it could not read the cluster. Such a read is not clean, and if it is
// still failing at the deadline Settle returns the error: an unreadable cluster says nothing about
// the chart. A cancelled ctx returns an error too, never the last read's problems or a clean
// result, since the window was not watched to the end (#49).
func Settle(ctx context.Context, check func() ([]string, error)) ([]string, error) {
	deadline := time.Now().Add(settleTimeout)
	var cleanSince time.Time
	for {
		problems, err := check()
		if ctx.Err() != nil {
			return nil, interrupted(ctx)
		}
		now := time.Now()
		switch {
		case err != nil || len(problems) > 0:
			cleanSince = time.Time{}
			if now.After(deadline) && err != nil {
				return nil, err
			}
			if now.After(deadline) {
				return problems, nil
			}
		case cleanSince.IsZero():
			cleanSince = now
		case now.Sub(cleanSince) >= settleWindow:
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, interrupted(ctx)
		case <-time.After(settlePoll):
		}
	}
}

func interrupted(ctx context.Context) error {
	return fmt.Errorf("interrupted while the pods settled: %w", ctx.Err())
}
