package main

import (
	"testing"

	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/source"
)

// Release ids are spelled "a17-cd1a...", so "a17" is what people type.
func TestResolveReleaseAcceptsThePrefixedVersion(t *testing.T) {
	idx := &source.Index{Releases: []source.ReleaseRef{
		{ID: "a17-cd1a.260905.001.b1", Android: manifest.Android{API: 37, Version: "17"}},
		{ID: "a16-cp1a.260505.005", Android: manifest.Android{API: 36, Version: "16"}},
	}}
	for _, spec := range []string{"17", "a17", "A17", "a17-cd1a.260905.001.b1"} {
		ref, err := resolveRelease(idx, spec)
		if err != nil {
			t.Errorf("%q: %v", spec, err)
			continue
		}
		if ref.ID != "a17-cd1a.260905.001.b1" {
			t.Errorf("%q resolved to %s", spec, ref.ID)
		}
	}
	if _, err := resolveRelease(idx, "a99"); err == nil {
		t.Error("an unknown version should still be refused")
	}
}

func TestOrFallsBack(t *testing.T) {
	if or("", "latest") != "latest" {
		t.Error("empty should fall back")
	}
	if or("17", "latest") != "17" {
		t.Error("a value should win")
	}
}
