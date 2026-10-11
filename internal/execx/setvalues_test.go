package execx

import (
	"slices"
	"testing"
)

func TestSetKeys(t *testing.T) {
	got := SetKeys([]string{`pw=hunter\,2secret`, "list={a,b},x=1", `dotted\.key=v`})
	want := []string{"pw", "list", "x", `dotted\.key`}
	if !slices.Equal(got, want) {
		t.Errorf("SetKeys = %q, want %q", got, want)
	}
}
