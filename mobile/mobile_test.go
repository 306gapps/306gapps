package mobile

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/sign"
	"github.com/306gapps/306gapps/internal/source"
)

const releaseID = "a16-bp41.250901.001"

func fixture(t *testing.T) *Session {
	t.Helper()
	root := t.TempDir()
	assets := filepath.Join(root, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := func(id, group string) manifest.Package {
		content := apk(t, id)
		if err := os.WriteFile(filepath.Join(assets, id+".apk"), content, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		return manifest.Package{ID: id, Name: strings.ToUpper(id), Group: group,
			Files: []manifest.File{{
				Path: "product/priv-app/" + id + "/" + id + ".apk", Asset: id + ".apk",
				SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content)), Mode: "0644",
				Kind: manifest.KindAPK, Context: "u:object_r:system_file:s0",
			}}}
	}

	gms := pkg("gmscore", "core")
	gms.Required = true
	vending := pkg("vending", "core")
	vending.Requires = []string{"gmscore"}
	vending.Default = true
	gsa := pkg("gsa", "apps")
	gsa.Requires = []string{"vending"}
	dialerG := pkg("dialer-google", "apps")
	dialerA := pkg("dialer-aosp", "apps")
	dialerA.Conflicts = []string{"dialer-google"}

	m := manifest.Manifest{
		Schema: manifest.Schema,
		Release: manifest.Release{
			ID:        releaseID,
			Android:   manifest.Android{API: 36, Version: "16"},
			Created:   time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
			AssetBase: "assets",
		},
		Groups:   []manifest.Group{{ID: "core", Name: "Core"}, {ID: "apps", Name: "Apps"}},
		Variants: []manifest.Variant{{ID: "core", Name: "Core", Packages: []string{"vending"}}},
		Packages: []manifest.Package{gms, vending, gsa, dialerG, dialerA},
	}
	writeJSON(t, filepath.Join(root, "manifest.json"), m)
	writeJSON(t, filepath.Join(root, "index.json"), source.Index{
		Schema: source.IndexSchema,
		Releases: []source.ReleaseRef{{
			ID: releaseID, Android: m.Release.Android, Created: m.Release.Created,
			Manifest: "manifest.json", AssetBase: "assets",
		}},
	})
	return NewSession(root, filepath.Join(root, "cache"), filepath.Join(root, "config"))
}

// apk is the smallest thing the verifier accepts: a readable zip.
func apk(t *testing.T, id string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.Create("AndroidManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(id))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("%v: %s", err, s)
	}
	return v
}

func load(t *testing.T) *Session {
	t.Helper()
	s := fixture(t)
	rel, err := s.Releases()
	if err != nil {
		t.Fatal(err)
	}
	if rs := decode[[]releaseJSON](t, rel); len(rs) != 1 || rs[0].ID != releaseID {
		t.Fatalf("releases: %s", rel)
	}
	cat, err := s.Load(releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if c := decode[catalogJSON](t, cat); len(c.Groups) != 2 || len(c.Groups[1].Packages) != 3 {
		t.Fatalf("catalog: %s", cat)
	}
	return s
}

func TestDefaultsAndVariant(t *testing.T) {
	s := load(t)
	st := decode[stateJSON](t, s.State())
	if !slices.Equal(st.Selected, []string{"gmscore", "vending"}) {
		t.Fatalf("defaults: %v", st.Selected)
	}
	if st.Variant != "core" {
		t.Fatalf("defaults should match the core preset, got %q", st.Variant)
	}
}

func TestToggleDependencyAndConflict(t *testing.T) {
	s := load(t)
	s.Toggle("vending", false)
	st := decode[stateJSON](t, s.Toggle("gsa", true))
	if !slices.Contains(st.Selected, "vending") || !slices.Equal(st.Implied["vending"], []string{"gsa"}) {
		t.Fatalf("gsa should pull in vending: %+v", st)
	}

	s.Toggle("dialer-google", true)
	st = decode[stateJSON](t, s.Toggle("dialer-aosp", true))
	if slices.Contains(st.Selected, "dialer-google") || st.Error != "" {
		t.Fatalf("taking one dialer should drop the other: %+v", st)
	}

	st = decode[stateJSON](t, s.Toggle("gmscore", false))
	if !slices.Contains(st.Selected, "gmscore") {
		t.Fatal("a required package came off")
	}
}

func TestSetGroupTakesOneSideOfAConflict(t *testing.T) {
	s := load(t)
	st := decode[stateJSON](t, s.SetGroup("apps", true))
	if st.Error != "" {
		t.Fatal(st.Error)
	}
	if !slices.Contains(st.Selected, "dialer-google") || slices.Contains(st.Selected, "dialer-aosp") {
		t.Fatalf("first in manifest order should win: %v", st.Selected)
	}
}

type progress struct{ last float64 }

func (p *progress) Update(_ string, frac float64) {
	if frac >= 0 {
		p.last = frac
	}
}

func TestBuildWritesSignedZip(t *testing.T) {
	s := load(t)
	s.Toggle("gsa", true)
	out := t.TempDir()
	p := &progress{}
	js, err := s.Build(out, "", p)
	if err != nil {
		t.Fatal(err)
	}
	r := decode[resultJSON](t, js)
	if p.last != 1 {
		t.Fatalf("progress ended at %v", p.last)
	}
	if filepath.Dir(r.Path) != out || r.Name != "306gapps-"+releaseID+"-recovery.zip" {
		t.Fatalf("result: %+v", r)
	}

	zr, err := zip.OpenReader(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	for _, want := range []string{"META-INF/" + sign.Alias + ".RSA", "META-INF/com/google/android/update-binary"} {
		if !slices.Contains(names, want) {
			t.Errorf("zip lacks %s", want)
		}
	}
	if !slices.ContainsFunc(names, func(n string) bool { return strings.HasSuffix(n, "gsa.apk") }) {
		t.Error("zip lacks the gsa payload")
	}
	if s.CacheSize() == 0 {
		t.Error("nothing was cached")
	}
}

func TestBuildBeforeLoad(t *testing.T) {
	if _, err := fixture(t).Build(t.TempDir(), "", &progress{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestZipName(t *testing.T) {
	def := "306gapps-" + releaseID + "-recovery.zip"
	for in, want := range map[string]string{
		"":                 def,
		"   ":              def,
		".zip":             def,
		"..":               def,
		"mine":             "mine.zip",
		" my gapps.ZIP ":   "my gapps.ZIP",
		"../../etc/x":      "x.zip",
		"/sdcard/out.zip":  "out.zip",
		"stock-17.zip.zip": "stock-17.zip.zip",
	} {
		if got := zipName(in, releaseID); got != want {
			t.Errorf("zipName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildUsesGivenName(t *testing.T) {
	s := load(t)
	if got := s.ZipName(); got != "306gapps-"+releaseID+"-recovery.zip" {
		t.Fatalf("ZipName = %q", got)
	}
	out := t.TempDir()
	js, err := s.Build(out, "my gapps", &progress{})
	if err != nil {
		t.Fatal(err)
	}
	r := decode[resultJSON](t, js)
	if r.Name != "my gapps.zip" || r.Path != filepath.Join(out, "my gapps.zip") {
		t.Fatalf("result: %+v", r)
	}
}
