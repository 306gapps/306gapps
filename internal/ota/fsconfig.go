// Package ota merges a staged selection into a ROM's target-files and drives the AOSP tooling.
package ota

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// fsEntry is one line of a filesystem_config.txt: the ownership and mode the image builder applies.
type fsEntry struct {
	Path  string
	UID   int
	GID   int
	Mode  uint32
	Extra string
}

func (e fsEntry) String() string {
	s := fmt.Sprintf("%s %d %d %o", e.Path, e.UID, e.GID, e.Mode)
	if e.Extra != "" {
		s += " " + e.Extra
	}
	return s
}

// fsConfig is a parsed filesystem_config.txt. ROMs differ on whether paths carry
// the partition prefix, and a mismatch silently loses ownership for every file we add.
type fsConfig struct {
	entries  []fsEntry
	prefixed bool
	part     string
}

func parseFSConfig(r io.Reader, partition string) (*fsConfig, error) {
	c := &fsConfig{part: partition}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)

	prefix := partition + "/"
	var prefixed, bare int

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		uid, err1 := strconv.Atoi(fields[1])
		gid, err2 := strconv.Atoi(fields[2])
		mode, err3 := strconv.ParseUint(fields[3], 8, 32)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		e := fsEntry{
			Path: fields[0], UID: uid, GID: gid, Mode: uint32(mode),
			Extra: strings.Join(fields[4:], " "),
		}
		c.entries = append(c.entries, e)

		switch {
		case e.Path == partition || strings.HasPrefix(e.Path, prefix):
			prefixed++
		case e.Path != "/" && e.Path != "":
			bare++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	c.prefixed = prefixed > bare
	return c, nil
}

// key renders a partition-relative path in this file's own convention.
func (c *fsConfig) key(rel string) string {
	if c.prefixed {
		return c.part + "/" + rel
	}
	return rel
}

// removeUnder drops every entry at or below a partition-relative path.
func (c *fsConfig) removeUnder(rel string) int {
	target := c.key(rel)
	kept := c.entries[:0]
	n := 0
	for _, e := range c.entries {
		if e.Path == target || strings.HasPrefix(e.Path, target+"/") {
			n++
			continue
		}
		kept = append(kept, e)
	}
	c.entries = kept
	return n
}

// set adds or replaces the entry for a partition-relative path.
func (c *fsConfig) set(rel string, uid, gid int, mode uint32, extra string) {
	target := c.key(rel)
	for i := range c.entries {
		if c.entries[i].Path == target {
			c.entries[i] = fsEntry{target, uid, gid, mode, extra}
			return
		}
	}
	c.entries = append(c.entries, fsEntry{target, uid, gid, mode, extra})
}

// has reports whether a partition-relative path is already recorded.
func (c *fsConfig) has(rel string) bool {
	target := c.key(rel)
	for _, e := range c.entries {
		if e.Path == target {
			return true
		}
	}
	return false
}

// render writes the file back, sorted so the output is reproducible.
func (c *fsConfig) render() []byte {
	sorted := append([]fsEntry(nil), c.entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	var b strings.Builder
	for _, e := range sorted {
		b.WriteString(e.String())
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// epoch fixes archive timestamps so a merge is reproducible.
var epoch = time.Date(2009, time.January, 1, 0, 0, 0, 0, time.UTC)
