// Package update reports whether a newer release has been published.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Release is a published version.
type Release struct {
	Tag string `json:"tag_name"`
	URL string `json:"html_url"`
}

// Check returns the newest release when it is newer than current.
//
// A build that is not exactly a released tag (a dev build, or anything Go
// stamped as a pseudo-version) reports nothing: it is already ahead of the
// last tag, so offering it as an update would be wrong.
func Check(ctx context.Context, repo, current string) (Release, bool, error) {
	if !clean(current) {
		return Release{}, false, nil
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return Release{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, false, fmt.Errorf("release check: %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Release{}, false, err
	}
	return r, Newer(r.Tag, current), nil
}

// Newer reports whether tag a is a later release than b. Anything that is not
// a plain vN.N.N is not comparable and answers false.
func Newer(a, b string) bool {
	x, ok1 := parse(a)
	y, ok2 := parse(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func clean(v string) bool {
	_, ok := parse(v)
	return ok
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	// A pre-release or pseudo-version suffix means this is not a release.
	if strings.ContainsAny(v, "-+ ") {
		return out, false
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
