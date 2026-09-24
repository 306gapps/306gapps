package ota

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// apkCerts is META/apkcerts.txt, which sign_target_files_apks reads when re-signing.
// Google's prebuilts must be marked PRESIGNED or GMS and Play Integrity break.
type apkCerts struct {
	lines     []string
	names     map[string]bool
	hasPart   bool
	certField string
}

var (
	nameField = regexp.MustCompile(`name="([^"]*)"`)
	partField = regexp.MustCompile(`partition="([^"]*)"`)
)

func parseAPKCerts(r io.Reader) (*apkCerts, error) {
	a := &apkCerts{names: map[string]bool{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		a.lines = append(a.lines, line)
		if m := nameField.FindStringSubmatch(line); m != nil {
			a.names[m[1]] = true
		}
		if partField.MatchString(line) {
			a.hasPart = true
		}
	}
	return a, sc.Err()
}

// addPresigned records an apk that must be left exactly as shipped.
func (a *apkCerts) addPresigned(apk, partition string) {
	if a.names[apk] {
		return
	}
	line := fmt.Sprintf(`name="%s" certificate="PRESIGNED" private_key=""`, apk)
	if a.hasPart && partition != "" {
		line += fmt.Sprintf(` partition="%s"`, partition)
	}
	a.lines = append(a.lines, line)
	a.names[apk] = true
}

// remove drops an apk's record, for files the selection supersedes.
func (a *apkCerts) remove(apk string) {
	if !a.names[apk] {
		return
	}
	kept := a.lines[:0]
	for _, l := range a.lines {
		if m := nameField.FindStringSubmatch(l); m != nil && m[1] == apk {
			continue
		}
		kept = append(kept, l)
	}
	a.lines = kept
	delete(a.names, apk)
}

func (a *apkCerts) render() []byte {
	sorted := append([]string(nil), a.lines...)
	sort.Strings(sorted)
	return []byte(strings.Join(sorted, "\n") + "\n")
}
