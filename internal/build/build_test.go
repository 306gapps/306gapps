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

// Already-compressed payloads must be stored. A 148 MiB GMS Core apex was
// being re-deflated for no gain because .apex was missing from the list.
func TestCompressedPayloadKindsAreStored(t *testing.T) {
	for _, ext := range []string{".apk", ".jar", ".so", ".apex", ".capex", ".dex"} {
		if !storeExts[ext] {
			t.Errorf("%s should be stored, not deflated", ext)
		}
	}
	for _, ext := range []string{".xml", ".sh", ".txt", ".prop", ".prof"} {
		if storeExts[ext] {
			t.Errorf("%s is compressible and should be deflated", ext)
		}
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

// symlinkPlan mirrors testPlan but includes a link, which carries a target and
// no payload.
func symlinkPlan(t *testing.T) *stage.Plan {
	t.Helper()
	p := testPlan(t)
	p.Entries = append(p.Entries, stage.Entry{
		Path:   "product/priv-app/GmsCore/lib/arm64/libjni.so",
		Mode:   0o777,
		Kind:   manifest.KindSymlink,
		Target: "/product/lib64/libjni.so",
	})
	return p
}

func TestRecoveryRecordsSymlinksInTheWorkList(t *testing.T) {
	out := filepath.Join(t.TempDir(), "r.zip")
	if _, err := Build(symlinkPlan(t), Options{Target: TargetRecovery, Out: out}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	var list string
	for _, f := range zr.File {
		if f.Name == "files/product/priv-app/GmsCore/lib/arm64/libjni.so" {
			t.Error("a symlink must not be carried as a payload")
		}
		if f.Name == "installer/files.list" {
			rc, _ := f.Open()
			b := make([]byte, f.UncompressedSize64)
			rc.Read(b)
			rc.Close()
			list = string(b)
		}
	}
	want := "product/priv-app/GmsCore/lib/arm64/libjni.so\t0777\tu:object_r:system_file:s0\t0\t/product/lib64/libjni.so"
	if !strings.Contains(list, want) {
		t.Errorf("files.list missing the link record:\n%s", list)
	}
}

func TestRecoveryDigestListSkipsSymlinks(t *testing.T) {
	out := filepath.Join(t.TempDir(), "r.zip")
	if _, err := Build(symlinkPlan(t), Options{Target: TargetRecovery, Out: out}); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.OpenReader(out)
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "installer/digests.txt" {
			continue
		}
		rc, _ := f.Open()
		b := make([]byte, f.UncompressedSize64)
		rc.Read(b)
		rc.Close()
		if strings.Contains(string(b), "libjni.so") {
			t.Errorf("a symlink has no digest to record:\n%s", b)
		}
		return
	}
	t.Fatal("digests.txt missing")
}

func TestModuleWritesARealSymlink(t *testing.T) {
	out := filepath.Join(t.TempDir(), "m.zip")
	if _, err := Build(symlinkPlan(t), Options{Target: TargetModule, Out: out}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name != "system/product/priv-app/GmsCore/lib/arm64/libjni.so" {
			continue
		}
		if f.Mode()&os.ModeSymlink == 0 {
			t.Errorf("entry is not marked as a symlink: mode %v", f.Mode())
		}
		rc, _ := f.Open()
		b := make([]byte, f.UncompressedSize64)
		rc.Read(b)
		rc.Close()
		if string(b) != "/product/lib64/libjni.so" {
			t.Errorf("link target wrong: %q", b)
		}
		return
	}
	t.Fatal("symlink entry missing from the module")
}

func TestBusyboxIsBundledWhenGiven(t *testing.T) {
	bb := filepath.Join(t.TempDir(), "busybox")
	if err := os.WriteFile(bb, []byte("\x7fELF-not-really"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "r.zip")
	if _, err := Build(testPlan(t), Options{Target: TargetRecovery, Out: out, Busybox: bb}); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.OpenReader(out)
	defer zr.Close()
	var hasBB, hasNotice bool
	for _, f := range zr.File {
		switch f.Name {
		case "installer/busybox":
			hasBB = true
			if f.Mode().Perm()&0o111 == 0 {
				t.Errorf("busybox is not executable: %v", f.Mode())
			}
		case "installer/NOTICE.busybox":
			hasNotice = true
		}
	}
	if !hasBB {
		t.Error("busybox not bundled")
	}
	// Shipping a GPLv2 binary obliges us to carry the source offer with it.
	if !hasNotice {
		t.Error("busybox bundled without its licence notice")
	}
}

func TestBusyboxIsOptional(t *testing.T) {
	out := filepath.Join(t.TempDir(), "r.zip")
	if _, err := Build(testPlan(t), Options{Target: TargetRecovery, Out: out}); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.OpenReader(out)
	defer zr.Close()
	for _, f := range zr.File {
		if strings.Contains(f.Name, "busybox") {
			t.Errorf("unexpected %s when no busybox was given", f.Name)
		}
	}
}
