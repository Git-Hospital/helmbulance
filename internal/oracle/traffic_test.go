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
