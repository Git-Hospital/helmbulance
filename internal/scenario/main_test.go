package scenario

import (
	"os"
	"testing"
	"time"
)

// The production waits are for real clusters: Settle's 90 s / 30 s / 10 s, the Secret snapshot's
// 3 s reads and the zero-downtime traffic phases. The tests run against fake helm and kubectl, so
// they shorten all of them here; a test that checks a timing sets its own and restores these (#60).
func TestMain(m *testing.M) {
	settleTimeout, settleWindow, settlePoll = 200*time.Millisecond, 20*time.Millisecond, time.Millisecond
	settleInterval = time.Millisecond
	controlDuration, trafficLead, trafficTail = time.Millisecond, time.Millisecond, time.Millisecond
	os.Exit(m.Run())
}
