package mobile

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/source"
)

const setupReleaseID = "a17-cd1a.260905.001.b1"

func setupSession(t *testing.T) *Session {
	t.Helper()
	root := t.TempDir()
	assets := filepath.Join(root, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := func(id string) manifest.Package {
		content := apk(t, id)
		if err := os.WriteFile(filepath.Join(assets, id+".apk"), content, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		return manifest.Package{ID: id, Name: id, Group: "setup",
			Files: []manifest.File{{Path: "product/priv-app/" + id + "/" + id + ".apk",
				Asset: id + ".apk", SHA256: hex.EncodeToString(sum[:]),
				Size: int64(len(content)), Mode: "0644", Kind: manifest.KindAPK}}}
	}
	gms := pkg("gmscore")
	gms.Group = "core"
	gms.Required = true
	suw := pkg("setupwizard")
	suw.Default = true
	suw.Removes = []string{"LineageSetupWizard", "Provision", "SetupWizard"}
	shim := pkg("localeshim")
	shim.Default = true

	m := manifest.Manifest{
		Schema: manifest.Schema,
		Release: manifest.Release{ID: setupReleaseID,
			Android: manifest.Android{API: 37, Version: "17"},
			Created: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), AssetBase: "assets"},
		Groups:   []manifest.Group{{ID: "core", Name: "Core"}, {ID: "setup", Name: "Setup"}},
		Packages: []manifest.Package{gms, suw, shim},
	}
	writeJSON(t, filepath.Join(root, "manifest.json"), m)
	writeJSON(t, filepath.Join(root, "index.json"), source.Index{
		Schema: source.IndexSchema,
		Releases: []source.ReleaseRef{{ID: setupReleaseID, Android: m.Release.Android,
			Created: m.Release.Created, Manifest: "manifest.json", AssetBase: "assets"}},
	})
	s := NewSession(root, filepath.Join(root, "cache"), filepath.Join(root, "config"))
	if _, err := s.Releases(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(setupReleaseID); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSetKeepStockSetupWizard(t *testing.T) {
	s := setupSession(t)

	st := decode[stateJSON](t, s.State())
	if !slices.Contains(st.Selected, "localeshim") {
		t.Fatal("locale shim should be selected by default")
	}
	if len(st.KeepStock) != 0 {
		t.Fatalf("keepStock should start empty: %v", st.KeepStock)
	}

	st = decode[stateJSON](t, s.SetKeepStock("setupwizard", true))
	if !slices.Contains(st.KeepStock, "setupwizard") {
		t.Error("AOSP should keep the setup wizard")
	}
	if slices.Contains(st.Selected, "localeshim") {
		t.Error("AOSP should deselect the locale shim")
	}

	st = decode[stateJSON](t, s.SetKeepStock("setupwizard", false))
	if slices.Contains(st.KeepStock, "setupwizard") {
		t.Error("Pixel should not keep the setup wizard")
	}
	if !slices.Contains(st.Selected, "localeshim") {
		t.Error("Pixel should reselect the locale shim")
	}
}
