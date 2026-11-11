package oracle

import (
	"testing"
	"time"
)

func TestParseTraffic(t *testing.T) {
	log := `100 ok
100 ok
101 fail
101 fail
102 fail
103 ok
104 ok
not a line
105 fail
106 ok
`
	s := ParseTraffic(log)
	if s.Total != 9 || s.Failed != 4 {
		t.Fatalf("Total/Failed = %d/%d, want 9/4", s.Total, s.Failed)
	}
	if s.LongestOutage != 2*time.Second {
		t.Errorf("LongestOutage = %v, want 2s (101 to 103)", s.LongestOutage)
	}
}

func TestParseTrafficOutageAtEnd(t *testing.T) {
	s := ParseTraffic("10 ok\n11 fail\n12 fail\n13 fail\n")
	if s.LongestOutage != 3*time.Second {
		t.Errorf("LongestOutage = %v, want 3s (11 to 13, inclusive)", s.LongestOutage)
	}
}

func TestParseTrafficClean(t *testing.T) {
	s := ParseTraffic("1 ok\n1 ok\n2 ok\n")
	if s.Failed != 0 || s.LongestOutage != 0 || s.FailedPercent() != 0 {
		t.Errorf("clean log reported failures: %+v", s)
	}
}

func TestParseTrafficSince(t *testing.T) {
	s := ParseTrafficSince("100 fail\n101 ok\n105 ok\n106 fail\n107 ok\n", 105)
	if s.Total != 3 || s.Failed != 1 {
		t.Fatalf("Total/Failed = %d/%d, want 3/1 (the failure at 100 is before since)", s.Total, s.Failed)
	}
	if len(s.FailedAt) != 1 || s.FailedAt[0] != 1 {
		t.Errorf("FailedAt = %v, want [1]", s.FailedAt)
	}
}

// ParseTrafficBetween counts the requests sent from since up to, not including, until, so each
// workload of a one-at-a-time restart gets the failures of its own window (#10).
func TestParseTrafficBetween(t *testing.T) {
	log := "100 ok\n101 fail\n102 fail\n103 ok\n104 fail\n105 ok\n"
	if s := ParseTrafficBetween(log, 101, 104); s.Total != 3 || s.Failed != 2 || s.LongestOutage != 2*time.Second {
		t.Errorf("[101, 104) = %+v, want 2 of 3 failed and a 2s outage", s)
	}
	if s := ParseTrafficBetween(log, 104, 0); s.Total != 2 || s.Failed != 1 {
		t.Errorf("[104, end) = %+v, want 1 of 2 failed", s)
	}
}

// The traffic pod writes what wget printed after "fail" (#162). Only short reasons are kept, and a
// line in the old two-field format still counts.
func TestParseTrafficReasons(t *testing.T) {
	log := "10 ok\n" +
		"11 fail wget: server returned error: HTTP/1.1 404 Not Found\n" +
		"11 fail wget: server returned error: HTTP/1.1 404 Not Found\n" +
		"12 fail wget: can't connect to remote host (10.96.0.4): Connection refused\n" +
		"13 fail\n" +
		"14 fail wget: something new\n" +
		"15 ok trailing words\n"
	s := ParseTraffic(log)
	if s.Total != 6 || s.Failed != 5 {
		t.Fatalf("total %d failed %d, want 6 and 5 (an ok line with extra words is not a request)", s.Total, s.Failed)
	}
	if s.Reasons["HTTP 404 Not Found"] != 2 || s.Reasons["connection refused"] != 1 || len(s.Reasons) != 2 {
		t.Errorf("reasons %v, want two 404s and one refused", s.Reasons)
	}
	if r, n := s.MostCommonReason(); r != "HTTP 404 Not Found" || n != 2 {
		t.Errorf("MostCommonReason = %q, %d", r, n)
	}
	if r, n := ParseTraffic("1 fail\n").MostCommonReason(); r != "" || n != 0 {
		t.Errorf("no reasons: MostCommonReason = %q, %d, want nothing", r, n)
	}
}

// What the pinned busybox 1.36.1 wget prints for each case, captured by running it.
func TestFailureReason(t *testing.T) {
	for in, want := range map[string]string{
		"wget: server returned error: HTTP/1.1 404 Not Found":                "HTTP 404 Not Found",
		"wget: server returned error: HTTP/1.1 400 Bad Request":              "HTTP 400 Bad Request",
		"wget: can't connect to remote host (127.0.0.1): Connection refused": "connection refused",
		"wget: bad address 'no-such-host.invalid'":                           "name not resolved",
		"wget: download timed out":                                           "timed out",
		"wget: error getting response: Connection reset by peer":             "connection reset",
		"wget: server returned error: garbled":                               "an HTTP error",
		"":                                                                   "",
		"wget: something else":                                               "",
	} {
		if got := FailureReason(in); got != want {
			t.Errorf("FailureReason(%q) = %q, want %q", in, got, want)
		}
	}
}
