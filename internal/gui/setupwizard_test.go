//go:build gui

package gui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/presets"
	"github.com/306gapps/306gapps/internal/source"
)

// setupFixture is a small assets repo with the setup-wizard packages the shared
// fixture leaves out.
func setupFixture(t *testing.T) (*source.Source, source.ReleaseRef) {
	t.Helper()
	dir := t.TempDir()
	digest := strings.Repeat("a", 64)
	file := func(path string) manifest.File {
		return manifest.File{Path: path, Asset: path, SHA256: digest,
			Size: 1000, Mode: "0644", Kind: manifest.KindAPK}
	}
	m := manifest.Manifest{
		Schema: manifest.Schema,
		Release: manifest.Release{
			ID: "a17-test", Android: manifest.Android{API: 37, Version: "17"},
			AssetBase: "assets",
		},
		Groups: []manifest.Group{{ID: "core", Name: "Core"}, {ID: "setup", Name: "Setup Wizard"}},
		Variants: []manifest.Variant{
			{ID: "core", Name: "Core", Packages: []string{"gmscore"}},
		},
		Packages: []manifest.Package{
			{ID: "gmscore", Name: "Play services", Group: "core", Required: true,
				Files: []manifest.File{file("product/priv-app/Gms/Gms.apk")}},
			{ID: "setupwizard", Name: "Google Setup Wizard", Group: "setup", Default: true,
				Removes: []string{"LineageSetupWizard", "Provision", "SetupWizard"},
				Files:   []manifest.File{file("product/priv-app/SUW/SUW.apk")}},
			{ID: "localeshim", Name: "Setup language picker", Group: "setup", Default: true,
				Files: []manifest.File{file("product/priv-app/Shim/Shim.apk")}},
		},
	}
	write := func(name string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", m)
	write("presets.json", map[string]any{
		"schema": presets.Schema,
		"variants": []map[string]any{
			{"id": "core", "name": "Core", "packages": []string{"gmscore"}},
		},
	})
	ref := source.ReleaseRef{ID: m.Release.ID, Android: m.Release.Android,
		Manifest: "manifest.json", AssetBase: "assets"}
	write("index.json", source.Index{Schema: source.IndexSchema,
		Releases: []source.ReleaseRef{ref}})
	return source.New(dir, source.NewCache(filepath.Join(dir, "cache"))), ref
}

func loadedSetup(t *testing.T) *window {
	t.Helper()
	test.NewApp()
	src, ref := setupFixture(t)
	s := newState(context.Background(), src, t.TempDir())
	u := &window{state: s, win: test.NewWindow(nil)}
	u.win.SetContent(u.build())
	if err := u.loadRelease(ref); err != nil {
		t.Fatal(err)
	}
	u.rebuildVariants()
	u.rebuildList()
	u.refreshSummary()
	return u
}

func TestAOSPHidesLocaleShim(t *testing.T) {
	u := loadedSetup(t)

	if _, ok := u.rows["localeshim"]; !ok {
		t.Fatal("locale picker should be listed in Pixel mode")
	}
	if !u.selected["localeshim"] {
		t.Fatal("locale picker should be selected by default")
	}

	u.rows["setupwizard"].setupSel.OnChanged("AOSP")

	if !u.state.keepStock["setupwizard"] {
		t.Error("AOSP should keep the ROM's setup wizard")
	}
	if u.selected["localeshim"] {
		t.Error("AOSP should deselect the locale picker")
	}
	if _, ok := u.rows["localeshim"]; ok {
		t.Error("AOSP should hide the locale picker row")
	}

	u.rows["setupwizard"].setupSel.OnChanged("Pixel")

	if u.state.keepStock["setupwizard"] {
		t.Error("Pixel should not keep the ROM's setup wizard")
	}
	if !u.selected["localeshim"] {
		t.Error("switching back to Pixel should reselect the locale picker")
	}
	if _, ok := u.rows["localeshim"]; !ok {
		t.Error("switching back to Pixel should show the locale picker row")
	}
}
