package stage

import (
	"archive/zip"
	"fmt"
	"path"
	"strings"
	"sync"
)

// Payloads that are zips underneath. A truncated one still hashes consistently,
// so only opening the archive catches it.
var containerExts = map[string]bool{
	".apk": true, ".apex": true, ".capex": true, ".jar": true,
}

// Corruption names a payload that is not the archive it claims to be.
type Corruption struct {
	Path   string
	Reason string
}

// CorruptError reports payloads that did not survive whatever produced them.
type CorruptError struct{ Found []Corruption }

func (e *CorruptError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d payload(s) are not readable archives:", len(e.Found))
	for _, c := range e.Found {
		fmt.Fprintf(&b, "\n  - %s: %s", c.Path, c.Reason)
	}
	b.WriteString("\n\nThe digests matched, so these bytes are exactly what the " +
		"source published;\nthe corruption happened before they were recorded. " +
		"Report it against the\nassets repo rather than retrying.")
	return b.String()
}

// Verify opens every archive-shaped payload in the plan and reports the ones that will not open.
// Digests agree on a file the dump itself truncated, which older erofs-utils did silently.
func (p *Plan) Verify(workers int) error {
	if workers <= 0 {
		workers = 4
	}

	var (
		mu    sync.Mutex
		found []Corruption
		wg    sync.WaitGroup
	)
	jobs := make(chan Entry)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				if reason := checkContainer(e); reason != "" {
					mu.Lock()
					found = append(found, Corruption{Path: e.Path, Reason: reason})
					mu.Unlock()
				}
			}
		}()
	}
	for _, e := range p.Entries {
		if e.IsSymlink() || e.IsEmpty() || !containerExts[strings.ToLower(path.Ext(e.Path))] {
			continue
		}
		jobs <- e
	}
	close(jobs)
	wg.Wait()

	if len(found) > 0 {
		return &CorruptError{Found: found}
	}
	return nil
}

func checkContainer(e Entry) string {
	zr, err := zip.OpenReader(e.Local)
	if err != nil {
		return err.Error()
	}
	defer zr.Close()
	if len(zr.File) == 0 {
		return "archive is empty"
	}
	return ""
}
