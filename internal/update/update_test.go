package update

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.1", "v0.1.0", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"0.2.0", "v0.1.0", true},
		// A build that is not a release is never behind: it is ahead of the
		// last tag, so offering that tag as an update would be wrong.
		{"v0.1.0", "v0.1.1-0.20260929003323-cc5b0a1bedb6", false},
		{"v0.1.0", "devel-587d9f55fbd3", false},
		{"v0.1.0", "unknown", false},
		{"v0.1.0", "", false},
		// Rubbish on either side is not comparable.
		{"latest", "v0.1.0", false},
		{"v0.1", "v0.1.0", false},
		{"v0.1.0.1", "v0.1.0", false},
		{"v-1.0.0", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// Ordering is numeric, not lexical: v0.10.0 is after v0.9.0.
func TestNewerIsNumeric(t *testing.T) {
	if !Newer("v0.10.0", "v0.9.0") {
		t.Error("v0.10.0 should be newer than v0.9.0")
	}
	if Newer("v0.9.0", "v0.10.0") {
		t.Error("v0.9.0 is not newer than v0.10.0")
	}
}
