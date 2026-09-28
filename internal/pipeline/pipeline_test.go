// Package pipeline_test exercises index -> manifest -> resolve -> fetch ->
// stage -> build against a local fixture source, with no network involved.
package pipeline_test

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/306gapps/306gapps/internal/build"
	"github.com/306gapps/306gapps/internal/catalog"
	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/source"
	"github.com/306gapps/306gapps/internal/stage"
)

type fixture struct {
	root  string
	cache string
}

// newFixture writes a miniature assets repo to disk.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	assets := filepath.Join(root, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}

	payload := func(name, content string) manifest.File {
		if err := os.WriteFile(filepath.Join(assets, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(content))
		return manifest.File{
			Asset: name, SHA256: hex.EncodeToString(sum[:]),
			Size: int64(len(content)), Mode: "0644",
		}
	}

	mk := func(id, category, part string, kind manifest.Kind) manifest.Package {
		f := payload(id+".apk", "payload-of-"+id)
		f.Path = fmt.Sprintf("%s/priv-app/%s/%s.apk", part, id, id)
		f.Kind = kind
		f.Context = "u:object_r:system_file:s0"
		return manifest.Package{
			ID: id, Name: strings.ToUpper(id), Group: category, Files: []manifest.File{f},
		}
	}

	gms := mk("gmscore", "core", "product", manifest.KindAPK)
	gms.Required = true
	gms.Props = map[string]string{"ro.com.google.gmsversion": "16_202509"}

	vending := mk("vending", "core", "product", manifest.KindAPK)
	vending.Requires = []string{"gmscore"}
	vending.Default = true

	gsa := mk("gsa", "apps", "product", manifest.KindAPK)
	gsa.Requires = []string{"gmscore"}
	gsa.Removes = []string{"product/app/QuickSearchBox"}

	dialerG := mk("dialer-google", "apps", "system_ext", manifest.KindAPK)
	dialerG.Requires = []string{"gmscore"}
	dialerA := mk("dialer-aosp", "apps", "system_ext", manifest.KindAPK)
	dialerA.Conflicts = []string{"dialer-google"}

	m := manifest.Manifest{
		Schema: manifest.Schema,
		Release: manifest.Release{
			ID:        "a16-bp41.250901.001",
			Android:   manifest.Android{API: 36, Version: "16", Codename: "Baklava"},
			Source:    manifest.Source{Device: "comet", Build: "BP41.250901.001"},
			Created:   time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
			AssetBase: "assets",
		},
		Groups: []manifest.Group{
			{ID: "core", Name: "Core"},
			{ID: "apps", Name: "Apps"},
		},
		Packages: []manifest.Package{gms, vending, gsa, dialerG, dialerA},
	}
	writeJSON(t, filepath.Join(root, "manifest.json"), m)

	idx := source.Index{
		Schema: source.IndexSchema,
		Repo:   "306gapps/306gapps-assets",
		Releases: []source.ReleaseRef{{
			ID: m.Release.ID, Android: m.Release.Android,
			Device: "comet", Build: "BP41.250901.001", Branch: "a16",
			Created: m.Release.Created, Manifest: "manifest.json", AssetBase: "assets",
		}},
	}
	writeJSON(t, filepath.Join(root, "index.json"), idx)

	return &fixture{root: root, cache: filepath.Join(root, "cache")}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) source() *source.Source {
	return source.New(f.root, source.NewCache(f.cache))
}

func TestEndToEndRecoveryBuild(t *testing.T) {
	fx := newFixture(t)
	src := fx.source()
	ctx := context.Background()

	idx, err := src.Index(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := idx.Latest(36)
	if !ok {
		t.Fatal("no release for API 36")
	}
	m, err := src.Manifest(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}

	c := catalog.New(m)
	// User picks the search app; gmscore arrives as a dependency and because
	// it is marked required.
	res, err := c.Resolve([]string{"gsa"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Selected("gmscore") {
		t.Fatalf("gmscore not pulled in: %v", res.Packages)
	}

	plan, err := stage.Build(ctx, src, m, res, stage.Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 2 {
		t.Fatalf("want 2 staged files, got %d", len(plan.Entries))
	}
	if plan.Props["ro.com.google.gmsversion"] != "16_202509" {
		t.Errorf("props not merged: %v", plan.Props)
	}
	if len(plan.Removes) != 1 {
		t.Errorf("removes not merged: %v", plan.Removes)
	}

	out := filepath.Join(t.TempDir(), "306gapps.zip")
	result, err := build.Build(plan, build.Options{Target: build.TargetRecovery, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	if result.Size == 0 || result.SHA256 == "" {
		t.Fatalf("empty result: %+v", result)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{
		"files/product/priv-app/gmscore/gmscore.apk",
		"files/product/priv-app/gsa/gsa.apk",
		"installer/installer.sh",
	} {
		if !names[want] {
			t.Errorf("missing %s in built zip", want)
		}
	}
}

func TestCacheAvoidsRefetch(t *testing.T) {
	fx := newFixture(t)
	src := fx.source()
	ctx := context.Background()

	m, err := src.Manifest(ctx, source.ReleaseRef{ID: "x", Manifest: "manifest.json", AssetBase: "assets"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := catalog.New(m).Resolve([]string{"vending"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stage.Build(ctx, src, m, res, stage.Options{}); err != nil {
		t.Fatal(err)
	}

	// Remove the origin assets entirely: a second stage must succeed from cache.
	if err := os.RemoveAll(filepath.Join(fx.root, "assets")); err != nil {
		t.Fatal(err)
	}
	if _, err := stage.Build(ctx, src, m, res, stage.Options{}); err != nil {
		t.Fatalf("second stage should have been served from cache: %v", err)
	}
}

func TestCorruptPayloadIsRejected(t *testing.T) {
	fx := newFixture(t)
	src := fx.source()
	ctx := context.Background()

	m, err := src.Manifest(ctx, source.ReleaseRef{ID: "x", Manifest: "manifest.json", AssetBase: "assets"})
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with a payload after the manifest recorded its digest.
	if err := os.WriteFile(filepath.Join(fx.root, "assets", "gmscore.apk"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := catalog.New(m).Resolve([]string{"gmscore"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = stage.Build(ctx, src, m, res, stage.Options{})
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("want digest mismatch, got %v", err)
	}
	// And nothing corrupt was left behind in the cache.
	if entries, _ := filepath.Glob(filepath.Join(fx.cache, "blobs", "*", "*")); len(entries) != 0 {
		t.Fatalf("corrupt blob cached: %v", entries)
	}
}

func TestConflictSurfacesBeforeDownload(t *testing.T) {
	fx := newFixture(t)
	src := fx.source()
	m, err := src.Manifest(context.Background(), source.ReleaseRef{ID: "x", Manifest: "manifest.json", AssetBase: "assets"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.New(m).Resolve([]string{"dialer-aosp", "dialer-google"})
	if err == nil || !strings.Contains(err.Error(), "cannot be installed alongside") {
		t.Fatalf("got %v", err)
	}
}

func TestAllTargetsBuildFromSamePlan(t *testing.T) {
	fx := newFixture(t)
	src := fx.source()
	ctx := context.Background()

	m, err := src.Manifest(ctx, source.ReleaseRef{ID: "x", Manifest: "manifest.json", AssetBase: "assets"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := catalog.New(m).Resolve([]string{"gsa", "vending", "dialer-google"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := stage.Build(ctx, src, m, res, stage.Options{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// The ota target needs a ROM target-files package, so it is covered in
	// internal/ota rather than here.
	for _, target := range []build.Target{build.TargetRecovery, build.TargetModule} {
		out := filepath.Join(dir, string(target)+".zip")
		r, err := build.Build(plan, build.Options{Target: target, Out: out})
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if r.Files == 0 {
			t.Errorf("%s produced no entries", target)
		}
	}
}
