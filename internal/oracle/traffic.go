package oracle

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TrafficScript is the shell loop the traffic pod runs: one HTTP request every 200 ms, one line per
// request: "<unix seconds> ok", or "<unix seconds> fail <what wget printed>". A response with status
// 4xx or 5xx counts as a failure (busybox wget exits non-zero). The reason is the first line of
// wget's error, without tabs or carriage returns (busybox tr has no character classes), so that a
// report can say how the requests failed (#162). $URL is set by the caller.
const TrafficScript = `while true; do t=$(date +%s); if r=$(wget -q -T 2 -O /dev/null "$URL" 2>&1); then echo "$t ok"; else echo "$t fail $(printf '%s' "$r" | head -n 1 | tr -d '\r\t' | cut -c1-120)"; fi; sleep 0.2; done`

// TrafficStats summarises a traffic log.
type TrafficStats struct {
	Total, Failed int
	// LongestOutage is the longest stretch of consecutive failures, from the first failed request
	// to the next successful one, at one-second resolution.
	LongestOutage time.Duration
	// FailedAt is the second of each failure, counted from the first request, so a report can say
	// whether the failures came at the start or later.
	FailedAt []int64
	// Reasons counts the failures by FailureReason; a line with no reason (an older traffic pod)
	// is not counted here.
	Reasons map[string]int
}

// FailureReason turns what busybox wget printed for a failed request into a short reason:
// "HTTP 404 Not Found" (the code with Go's name for it, not the server's phrase), "connection refused", "name not resolved", "timed out", "connection
// reset", or "" when it says nothing known. Only these words reach a report, never wget's text,
// which can name a host.
func FailureReason(wget string) string {
	switch {
	case strings.Contains(wget, "server returned error: "):
		status := wget[strings.Index(wget, "server returned error: ")+len("server returned error: "):]
		// Only the status code is kept: the reason phrase is the app's own text, which can be
		// missing ("HTTP/1.1 400 ") or carry anything, such as a URL.
		if f := strings.Fields(status); len(f) >= 2 && strings.HasPrefix(f[0], "HTTP/") {
			if code, err := strconv.Atoi(f[1]); err == nil && code >= 100 && code <= 999 {
				return strings.TrimSpace("HTTP " + f[1] + " " + http.StatusText(code))
			}
		}
		return "an HTTP error"
	case strings.Contains(wget, "Connection refused"):
		return "connection refused"
	case strings.Contains(wget, "bad address"):
		return "name not resolved"
	case strings.Contains(wget, "timed out"):
		return "timed out"
	case strings.Contains(wget, "Connection reset"):
		return "connection reset"
	}
	return ""
}

// MostCommonReason is the reason most failures had and how many had it; "" and 0 when no failure
// carried a known reason. A tie goes to the alphabetically first, so reports do not flip.
func (s TrafficStats) MostCommonReason() (string, int) {
	best, n := "", 0
	for r, c := range s.Reasons {
		if c > n || c == n && r < best {
			best, n = r, c
		}
	}
	return best, n
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

// TrafficSecond returns the unix second of a traffic log line, in the traffic pod's own clock.
func TrafficSecond(line string) (int64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, false
	}
	t, err := strconv.ParseInt(fields[0], 10, 64)
	return t, err == nil
}

// ParseTrafficSince is ParseTraffic counting only requests sent at or after since (unix seconds).
func ParseTrafficSince(log string, since int64) TrafficStats {
	return ParseTrafficBetween(log, since, 0)
}

// ParseTrafficBetween is ParseTrafficSince counting only requests sent before until as well; an
// until of 0 has no end. A request in the second until belongs to the next window.
func ParseTrafficBetween(log string, since, until int64) TrafficStats {
	var s TrafficStats
	var first int64 = -1
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
		if len(fields) < 2 || fields[1] == "ok" && len(fields) != 2 {
			continue
		}
		t, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || t < since || (until > 0 && t >= until) {
			continue
		}
		if first < 0 {
			first = t
		}
		switch fields[1] {
		case "ok":
			s.Total++
			closeOutage(t)
		case "fail":
			s.Total++
			s.Failed++
			s.FailedAt = append(s.FailedAt, t-first)
			if r := FailureReason(strings.Join(fields[2:], " ")); r != "" {
				if s.Reasons == nil {
					s.Reasons = map[string]int{}
				}
				s.Reasons[r]++
			}
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
