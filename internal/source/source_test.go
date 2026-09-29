package source

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/306gapps/306gapps/internal/manifest"
)

func ref(id string, api int, created string) ReleaseRef {
	t, err := time.Parse(time.RFC3339, created)
	if err != nil {
		panic(err)
	}
	return ReleaseRef{ID: id, Android: manifest.Android{API: api}, Created: t}
}

func TestLatestPrefersTheNewestAndroidVersion(t *testing.T) {
	// Re-dumping an older version republishes it with a fresh timestamp, so
	// publication date alone would pick Android 14 here.
	idx := &Index{Releases: []ReleaseRef{
		ref("a17", 37, "2026-09-24T10:00:00Z"),
		ref("a14", 34, "2026-09-24T18:00:00Z"),
		ref("a16", 36, "2026-09-24T12:00:00Z"),
	}}
	got, ok := idx.Latest(0)
	if !ok || got.ID != "a17" {
		t.Fatalf("got %q, want a17", got.ID)
	}
}

func TestLatestBreaksTiesByPublicationDate(t *testing.T) {
	idx := &Index{Releases: []ReleaseRef{
		ref("a17-old", 37, "2026-08-01T00:00:00Z"),
		ref("a17-new", 37, "2026-09-24T00:00:00Z"),
	}}
	got, _ := idx.Latest(0)
	if got.ID != "a17-new" {
		t.Fatalf("got %q, want a17-new", got.ID)
	}
}

func TestLatestForAnExplicitAPI(t *testing.T) {
	idx := &Index{Releases: []ReleaseRef{
		ref("a17", 37, "2026-09-24T10:00:00Z"),
		ref("a14-old", 34, "2026-01-01T00:00:00Z"),
		ref("a14-new", 34, "2026-09-24T18:00:00Z"),
	}}
	got, ok := idx.Latest(34)
	if !ok || got.ID != "a14-new" {
		t.Fatalf("got %q, want a14-new", got.ID)
	}
}

func TestLatestOnAnEmptyIndex(t *testing.T) {
	if _, ok := (&Index{}).Latest(0); ok {
		t.Fatal("empty index should report nothing")
	}
	if _, ok := (&Index{Releases: []ReleaseRef{ref("a17", 37, "2026-09-24T00:00:00Z")}}).Latest(33); ok {
		t.Fatal("an API with no releases should report nothing")
	}
}

func TestAPIsAreNewestFirst(t *testing.T) {
	idx := &Index{Releases: []ReleaseRef{
		ref("a14", 34, "2026-09-24T00:00:00Z"),
		ref("a17", 37, "2026-09-24T00:00:00Z"),
		ref("a16", 36, "2026-09-24T00:00:00Z"),
	}}
	got := idx.APIs()
	if len(got) != 3 || got[0] != 37 || got[2] != 34 {
		t.Fatalf("got %v, want [37 36 34]", got)
	}
}

// A 500 from GitHub's asset host used to end a build: one bad response out of
// a hundred files and nothing downloaded.
func TestTransientFailureIsRetried(t *testing.T) {
	old := backoff
	backoff = []time.Duration{0, 0, 0}
	defer func() { backoff = old }()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		io.WriteString(w, "payload")
	}))
	defer srv.Close()

	s := New(srv.URL, nil)
	rc, _, err := s.open(context.Background(), "thing.gz")
	if err != nil {
		t.Fatalf("gave up: %v", err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "payload" {
		t.Errorf("got %q", b)
	}
	if hits != 3 {
		t.Errorf("made %d attempts, want 3", hits)
	}
}

func TestNotFoundIsNotRetried(t *testing.T) {
	old := backoff
	backoff = []time.Duration{0, 0, 0}
	defer func() { backoff = old }()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, _, err := New(srv.URL, nil).open(context.Background(), "gone"); err == nil {
		t.Fatal("want an error")
	}
	if hits != 1 {
		t.Errorf("retried a 404 %d times", hits-1)
	}
}

func TestRetryGivesUpAndSaysHowOften(t *testing.T) {
	old := backoff
	backoff = []time.Duration{0, 0, 0}
	defer func() { backoff = old }()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, _, err := New(srv.URL, nil).open(context.Background(), "down")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "4 attempts") {
		t.Errorf("error does not say how many tries: %v", err)
	}
	if hits != 4 {
		t.Errorf("made %d attempts, want 4", hits)
	}
}

func TestCancellationStopsRetrying(t *testing.T) {
	old := backoff
	backoff = []time.Duration{time.Hour}
	defer func() { backoff = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := New(srv.URL, nil).open(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}
