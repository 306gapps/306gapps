package presets_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/306gapps/306gapps/internal/catalog"
	"github.com/306gapps/306gapps/internal/presets"
	"github.com/306gapps/306gapps/internal/source"
)

// TestLivePresetsResolve checks the shipped presets against the real releases,
// which is the only way to catch a preset naming a package no release has.
// Needs the network, so it is opt-in.
func TestLivePresetsResolve(t *testing.T) {
	if os.Getenv("LIVE") == "" {
		t.Skip("set LIVE=1")
	}
	ctx := context.Background()
	s := source.New(source.DefaultRoot, source.NewCache(t.TempDir()))
	idx, err := s.Index(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range idx.Releases {
		m, err := s.Manifest(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		c := catalog.New(m)
		presets.Apply(ctx, s, c)
		for _, v := range c.Variants() {
			if len(v.Packages) == 0 {
				t.Errorf("%s: preset %s resolved to nothing", ref.ID, v.ID)
			}
			if strings.HasPrefix(v.ID, "crdroid") {
				for _, p := range v.Packages {
					if p == "pixellauncher" {
						t.Errorf("%s: %s includes pixellauncher", ref.ID, v.ID)
					}
				}
			}
			t.Logf("%-24s %-13s %2d packages", ref.ID, v.ID, len(v.Packages))
		}
	}
}
