package scenario

import (
	"os"
	"testing"
	"time"

	"github.com/Git-Hospital/helmbulance/internal/kube"
)

// The production waits are for real clusters: Settle's 90 s / 30 s / 10 s, the Secret snapshot's
// 3 s reads, the reads after a --wait timeout and the zero-downtime traffic phases. The tests run against fake helm and kubectl, so
// they shorten all of them here; a test that checks a timing sets its own and restores these (#60).
func TestMain(m *testing.M) {
	settleTimeout, settleWindow, settlePoll = 200*time.Millisecond, 20*time.Millisecond, time.Millisecond
	settleInterval = time.Millisecond
	crashReadGap = time.Millisecond
	controlDuration, trafficLead, trafficTail = time.Millisecond, time.Millisecond, time.Millisecond
	kube.OutsideRecheck = time.Millisecond
	os.Exit(m.Run())
}
