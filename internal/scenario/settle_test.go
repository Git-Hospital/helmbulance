package scenario

import (
	"context"
	"errors"
	"testing"
	"time"
)

// shortSettle shrinks the settle timings for a test.
func shortSettle(t *testing.T, timeout, window time.Duration) {
	t.Helper()
	to, w, p := settleTimeout, settleWindow, settlePoll
	t.Cleanup(func() { settleTimeout, settleWindow, settlePoll = to, w, p })
	settleTimeout, settleWindow, settlePoll = timeout, window, time.Millisecond
}

// reads returns a check that gives the next answer on each call, then repeats the last.
func reads(answers ...[]string) func() ([]string, error) {
	i := 0
	return func() ([]string, error) {
		a := answers[min(i, len(answers)-1)]
		i++
		return a, nil
	}
}

// failing returns a check that cannot read the cluster on the calls in bad (0-based), and reads
// a clean namespace on the others; the last call's answer repeats.
func failing(n int, bad ...int) func() ([]string, error) {
	i := 0
	return func() ([]string, error) {
		call := min(i, n-1)
		i++
		for _, b := range bad {
			if b == call {
				return nil, errors.New("could not read pods: connection refused")
			}
		}
		return nil, nil
	}
}

// A pod that crashes after the first clean read is seen.
func TestSettleSeesAProblemAfterAFirstCleanRead(t *testing.T) {
	shortSettle(t, 20*time.Millisecond, time.Second)
	got, err := Settle(t.Context(), reads(nil, nil, []string{"pod `web` restarted"}))
	if len(got) != 1 || err != nil {
		t.Errorf("Settle = %q, want the restart", got)
	}
}

func TestSettleCleanForTheWindow(t *testing.T) {
	shortSettle(t, time.Second, 10*time.Millisecond)
	if got, err := Settle(t.Context(), reads([]string{"pod `web` is not ready"}, nil)); got != nil || err != nil {
		t.Errorf("Settle = %q, want nil once ready and clean for the window", got)
	}
}

// #49 (1): a run interrupted before the pods settled has proven neither health nor a problem, so
// Settle returns an error, never the last read's problems or a clean result.
func TestSettleCancelled(t *testing.T) {
	shortSettle(t, time.Hour, time.Hour)
	for name, check := range map[string]func() ([]string, error){
		"after a clean read":   reads(nil),
		"after a problem":      reads([]string{"pod `web` restarted"}),
		"during a failed read": failing(1, 0),
	} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		got, err := Settle(ctx, check)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("%s: Settle after cancel = %q, %v; want no problems and context.Canceled", name, got, err)
		}
	}
}

// Cancelled while waiting between reads, after the first clean one.
func TestSettleCancelledInTheWindow(t *testing.T) {
	shortSettle(t, time.Hour, time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	got, err := Settle(ctx, func() ([]string, error) {
		if calls++; calls == 2 {
			cancel()
		}
		return nil, nil
	})
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("Settle = %q, %v; want no problems and context.Canceled", got, err)
	}
}

// #49 (1): a cluster that cannot be read says nothing about the chart. At the deadline the read
// error is returned as an error, not as a problem with the workloads; a read that recovers counts
// like any other.
func TestSettleReadErrors(t *testing.T) {
	shortSettle(t, 20*time.Millisecond, 5*time.Millisecond)
	if got, err := Settle(t.Context(), failing(1, 0)); got != nil || err == nil {
		t.Errorf("never readable: Settle = %q, %v; want no problems and the read error", got, err)
	}
	if got, err := Settle(t.Context(), failing(3, 0, 1)); got != nil || err != nil {
		t.Errorf("readable again: Settle = %q, %v; want a clean result", got, err)
	}
}

// Interrupted during the read that reaches the deadline: kubectl was killed, or the pods it read
// were mid-shutdown. Neither is a problem of the chart's.
func TestSettleCancelledAtTheDeadline(t *testing.T) {
	shortSettle(t, 0, time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	got, err := Settle(ctx, func() ([]string, error) {
		cancel()
		return []string{"pod `web` is not ready"}, nil
	})
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("Settle = %q, %v; want no problems and context.Canceled", got, err)
	}
}
