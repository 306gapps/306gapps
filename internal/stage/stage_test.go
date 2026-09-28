package stage

import (
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
