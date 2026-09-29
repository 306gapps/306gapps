//go:build gui

package gui

import "testing"

func TestFirstSentence(t *testing.T) {
	cases := []struct {
		in   string
		want string
		more bool
	}{
		{"Replaces the ROM's first-boot flow. Needed to sign in during setup; " +
			"without it you sign in from Settings afterwards.",
			"Replaces the ROM's first-boot flow.", true},
		{"Google Calendar.", "Google Calendar.", false},
		{"No trailing period", "No trailing period", false},
		{"", "", false},
		{"Keyboard, including the offline speech and CJK models. Those live " +
			"outside the app directory.",
			"Keyboard, including the offline speech and CJK models.", true},
		// Folded yaml arrives with newlines and runs of spaces.
		{"Scans apps as they install.\n  This is also the component\n  Google uses.",
			"Scans apps as they install.", true},
		// An abbreviation is not a sentence end.
		{"Works offline, e.g. on a plane. Downloads on demand otherwise.",
			"Works offline, e.g. on a plane.", true},
		{"One. Two. Three.", "One.", true},
	}
	for _, c := range cases {
		got, more := firstSentence(c.in)
		if got != c.want || more != c.more {
			t.Errorf("firstSentence(%q) = (%q, %v), want (%q, %v)",
				c.in, got, more, c.want, c.more)
		}
	}
}
