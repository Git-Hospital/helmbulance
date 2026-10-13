package scenario

import "testing"

func TestNewSentinel(t *testing.T) {
	a, err := newSentinel()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newSentinel()
	if len(a) != 32 || a == b {
		t.Errorf("sentinels %q and %q: want two different 32-character values", a, b)
	}
}

func TestSentinelState(t *testing.T) {
	for out, want := range map[string]string{
		"abc\n": "kept", "missing\n": "missing", "": "missing", "xyz": "replaced",
	} {
		if got := sentinelState(out, "abc"); got != want {
			t.Errorf("sentinelState(%q) = %s, want %s", out, got, want)
		}
	}
}
