package build

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/stage"
)

func testPlan(t *testing.T) *stage.Plan {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	gms := write("gms.apk", "gmscore-payload")
	perm := write("perm.xml", "<permissions/>")

	return &stage.Plan{
		Release: manifest.Release{
			ID:      "a16-bp41.250901.001",
			Android: manifest.Android{API: 36, Version: "16", Codename: "Baklava"},
			Source:  manifest.Source{Device: "comet", Build: "BP41.250901.001"},
			Created: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
		},
		Packages: []manifest.Package{{ID: "gmscore", Name: "Play services"}},
		Entries: []stage.Entry{
			{Path: "product/priv-app/GmsCore/GmsCore.apk", Local: gms, Mode: 0o644,
				Context: "u:object_r:system_file:s0", Kind: manifest.KindAPK, Size: 15},
			{Path: "product/etc/permissions/gms.xml", Local: perm, Mode: 0o644,
				Kind: manifest.KindPermission, Size: 14},
			{Path: "system/priv-app/Setup/Setup.apk", Local: gms, Mode: 0o644,
				Kind: manifest.KindAPK, Size: 15},
		},
		Removes: []string{"product/app/QuickSearchBox"},
		Props:   map[string]string{"ro.com.google.gmsversion": "16_202509"},
		Size:    44,
	}
}

// TestOTARequiresABase documents that the ota target is useless without the
// ROM's own package -- there is nothing generic to fall back on.
func TestOTARequiresABase(t *testing.T) {
	_, err := Build(testPlan(t), Options{Target: TargetOTA, Out: filepath.Join(t.TempDir(), "o.zip")})
	if err == nil || !strings.Contains(err.Error(), "target-files") {
		t.Fatalf("want a target-files error, got %v", err)
	}
}

func buildTo(t *testing.T, target Target) (string, map[string]string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.zip")
	if _, err := Build(testPlan(t), Options{Target: target, Out: out}); err != nil {
		t.Fatalf("%s build: %v", target, err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b := make([]byte, f.UncompressedSize64)
		_, _ = rc.Read(b)
		rc.Close()
		got[f.Name] = string(b)
	}
	return out, got
}

func TestRecoveryLayout(t *testing.T) {
	_, files := buildTo(t, TargetRecovery)
	for _, want := range []string{
		"META-INF/com/google/android/update-binary",
		"META-INF/com/google/android/updater-script",
		"installer/installer.sh",
		"installer/util.sh",
		"installer/addon.d.sh",
		"installer/files.list",
		"installer/release.txt",
		"files/product/priv-app/GmsCore/GmsCore.apk",
		"files/system/priv-app/Setup/Setup.apk",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	list := files["installer/files.list"]
	if !strings.Contains(list, "product/priv-app/GmsCore/GmsCore.apk\t0644\tu:object_r:system_file:s0\t15") {
		t.Errorf("files.list record wrong:\n%s", list)
	}
	// A file with no recorded context gets the partition default.
	if !strings.Contains(list, "product/etc/permissions/gms.xml\t0644\tu:object_r:system_file:s0\t14") {
		t.Errorf("default context not applied:\n%s", list)
	}
	if !strings.Contains(files["installer/release.txt"], "api=36") {
		t.Error("release.txt missing api level")
	}
	if files["installer/removals.txt"] != "product/app/QuickSearchBox\n" {
		t.Errorf("removals wrong: %q", files["installer/removals.txt"])
	}
	if files["installer/props.txt"] != "ro.com.google.gmsversion=16_202509\n" {
		t.Errorf("props wrong: %q", files["installer/props.txt"])
	}
}

func TestModuleLayoutAndPathMapping(t *testing.T) {
	_, files := buildTo(t, TargetModule)
	for _, want := range []string{
		"module.prop", "customize.sh", "service.sh",
		"META-INF/com/google/android/update-binary",
		// /product is reached through /system/product in a module overlay.
		"system/product/priv-app/GmsCore/GmsCore.apk",
		// /system entries must not gain a second "system" component.
		"system/priv-app/Setup/Setup.apk",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	if _, bad := files["system/system/priv-app/Setup/Setup.apk"]; bad {
		t.Error("system partition double-prefixed")
	}
	prop := files["module.prop"]
	if !strings.Contains(prop, "id=306gapps") || !strings.Contains(prop, "versionCode=20260905") {
		t.Errorf("module.prop wrong:\n%s", prop)
	}
}

func TestModulePathMapping(t *testing.T) {
	cases := map[string]string{
		"system/priv-app/A/A.apk":     "system/priv-app/A/A.apk",
		"product/app/B/B.apk":         "system/product/app/B/B.apk",
		"system_ext/priv-app/C/C.apk": "system/system_ext/priv-app/C/C.apk",
		"vendor/etc/d.xml":            "system/vendor/etc/d.xml",
	}
	for in, want := range cases {
		if got := modulePath(in); got != want {
			t.Errorf("modulePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	plan := testPlan(t)
	dir := t.TempDir()
	digests := make([]string, 2)
	for i := range digests {
		out := filepath.Join(dir, "out.zip")
		res, err := Build(plan, Options{Target: TargetRecovery, Out: out})
		if err != nil {
			t.Fatal(err)
		}
		digests[i] = res.SHA256
		os.Remove(out)
		time.Sleep(10 * time.Millisecond)
	}
	if digests[0] != digests[1] {
		t.Fatalf("builds differ: %s vs %s", digests[0], digests[1])
	}
}

func TestBuildRejectsEmptyPlan(t *testing.T) {
	_, err := Build(&stage.Plan{}, Options{Target: TargetRecovery, Out: filepath.Join(t.TempDir(), "x.zip")})
	if err == nil || !strings.Contains(err.Error(), "nothing to build") {
		t.Fatalf("got %v", err)
	}
}

func TestAPKsAreStoredNotDeflated(t *testing.T) {
	out, _ := buildTo(t, TargetRecovery)
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		switch {
		case strings.HasSuffix(f.Name, ".apk") && f.Method != zip.Store:
			t.Errorf("%s should be stored, got method %d", f.Name, f.Method)
		case strings.HasSuffix(f.Name, ".sh") && f.Method != zip.Deflate:
			t.Errorf("%s should be deflated, got method %d", f.Name, f.Method)
		}
	}
}

func TestParseTarget(t *testing.T) {
	if _, err := ParseTarget("recovery"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseTarget("nope"); err == nil {
		t.Fatal("expected error")
	}
}
