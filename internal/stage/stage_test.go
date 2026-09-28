package stage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/306gapps/306gapps/internal/catalog"
	"github.com/306gapps/306gapps/internal/source"
	"os"
	"path/filepath"
	"testing"

	"github.com/306gapps/306gapps/internal/manifest"
)

// Expert mode lets someone keep the ROM's own app beside the Google one. That
// means not running the removals the package would otherwise do.
func TestKeepStockSkipsOnlyThatPackagesRemovals(t *testing.T) {
	pkgs := []manifest.Package{
		{ID: "chrome", Removes: []string{"Browser", "Jelly"}},
		{ID: "dialer", Removes: []string{"Dialer"}},
	}
	all := mergeRemoves(pkgs, nil)
	if len(all) != 3 {
		t.Fatalf("without keep-stock every removal applies, got %v", all)
	}
	kept := mergeRemoves(pkgs, []string{"chrome"})
	if len(kept) != 1 || kept[0] != "Dialer" {
		t.Errorf("keeping chrome should leave only the dialer removal, got %v", kept)
	}
	none := mergeRemoves(pkgs, []string{"chrome", "dialer"})
	if len(none) != 0 {
		t.Errorf("keeping both should remove nothing, got %v", none)
	}
	// An id that is not in the selection is simply ignored.
	if got := mergeRemoves(pkgs, []string{"nonsense"}); len(got) != 3 {
		t.Errorf("an unknown id should change nothing, got %v", got)
	}
}

// The download workers all call Progress. A caller that keeps a map of what it
// has seen -- both pickers do -- would otherwise hit "concurrent map writes",
// which is a hard crash, not an error. Run with -race.
func TestProgressIsNotCalledConcurrently(t *testing.T) {
	dir := t.TempDir()
	var files []manifest.File
	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("f%02d.apk", i)
		body := bytes.Repeat([]byte{byte(i)}, 4096)
		sum := sha256.Sum256(body)
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, manifest.File{
			Path: "product/app/X" + name, Asset: name,
			SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)),
			Mode: "0644", Kind: manifest.KindAPK,
		})
	}
	m := &manifest.Manifest{
		Schema:  manifest.Schema,
		Release: manifest.Release{ID: "t", Android: manifest.Android{API: 36}},
	}
	res := &catalog.Resolution{Files: files, Owner: map[string]string{}}
	src := source.New(dir, source.NewCache(t.TempDir()))

	// Exactly the shape both pickers use, unguarded on purpose.
	seen := map[string]bool{}
	n := 0
	_, err := Build(context.Background(), src, m, res, Options{
		Workers: 8,
		Progress: func(f manifest.File, got, want int64) {
			if got < want || seen[f.Path] {
				return
			}
			seen[f.Path] = true
			n++
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("progress was never reported")
	}
}
