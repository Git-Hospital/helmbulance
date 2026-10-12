package oracle

import (
	"strconv"
	"strings"
	"time"
)

// TrafficScript is the shell loop the traffic pod runs: one HTTP request every 200 ms, one line per
// request: "<unix seconds> ok" or "<unix seconds> fail". A response with status 4xx or 5xx counts
// as a failure (busybox wget exits non-zero). $URL is set by the caller.
const TrafficScript = `while true; do t=$(date +%s); if wget -q -T 2 -O /dev/null "$URL"; then echo "$t ok"; else echo "$t fail"; fi; sleep 0.2; done`

// TrafficStats summarises a traffic log.
type TrafficStats struct {
	Total, Failed int
	// LongestOutage is the longest stretch of consecutive failures, from the first failed request
	// to the next successful one, at one-second resolution.
	LongestOutage time.Duration
}

// FailedPercent is the share of failed requests.
func (s TrafficStats) FailedPercent() float64 {
	if s.Total == 0 {
		return 0
	}
	return 100 * float64(s.Failed) / float64(s.Total)
}

// ParseTraffic reads the traffic pod's log. Lines it does not recognise are ignored.
func ParseTraffic(log string) TrafficStats {
	return ParseTrafficSince(log, 0)
}

// ParseTrafficSince is ParseTraffic counting only requests sent at or after since (unix seconds).
func ParseTrafficSince(log string, since int64) TrafficStats {
	var s TrafficStats
	var outageStart int64 = -1
	var lastFail int64
	closeOutage := func(end int64) {
		if outageStart < 0 {
			return
		}
		if d := time.Duration(end-outageStart) * time.Second; d > s.LongestOutage {
			s.LongestOutage = d
		}
		outageStart = -1
	}
	for _, line := range strings.Split(log, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		t, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || t < since {
			continue
		}
		switch fields[1] {
		case "ok":
			s.Total++
			closeOutage(t)
		case "fail":
			s.Total++
			s.Failed++
			lastFail = t
			if outageStart < 0 {
				outageStart = t
			}
		}
	}
	// An outage still open at the end lasts at least until the last failure, plus that second.
	closeOutage(lastFail + 1)
	return s
}
