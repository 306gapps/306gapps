package source

import (
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
