//go:build gui

package gui

import "strings"

// firstSentence is what a collapsed row shows.
//
// The rest is a click away, so the row wants a whole sentence rather than
// however much text happens to fit before the label clips. Returns the
// sentence and whether anything was left behind.
func firstSentence(s string) (string, bool) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "", false
	}
	for i := 0; i < len(s)-1; i++ {
		if s[i] != '.' || s[i+1] != ' ' {
			continue
		}
		// "e.g. " and friends are not sentence ends.
		if i >= 1 && isAbbrev(s[:i+1]) {
			continue
		}
		return s[:i+1], true
	}
	return s, false
}

func isAbbrev(upTo string) bool {
	for _, a := range []string{"e.g.", "i.e.", "etc.", "vs.", "Inc.", "Ltd."} {
		if strings.HasSuffix(upTo, a) {
			return true
		}
	}
	return false
}
