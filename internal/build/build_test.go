package build

import (
	"archive/zip"
	"bytes"
	"github.com/ulikunitz/xz"
	"io"
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
				Context: "u:object_r:system_file:s0", Kind: manifest.KindAPK, Size: 15,
				Package: "Play services"},
			{Path: "product/etc/permissions/gms.xml", Local: perm, Mode: 0o644,
				Kind: manifest.KindPermission, Size: 14, Package: "Play services"},
			{Path: "system/priv-app/Setup/Setup.apk", Local: gms, Mode: 0o644,
				Kind: manifest.KindAPK, Size: 15, Package: "Setup Wizard"},
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
	if _, err := Build(testPlan(t), Options{Target: target, Out: out, ForceXZ: true}); err != nil {
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
		"files/product/priv-app/GmsCore/GmsCore.apk.xz",
		"files/system/priv-app/Setup/Setup.apk.xz",
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

// Apks and apexes look like they should be stored, being zips already. They
// should not: Google leaves many entries uncompressed inside them so they can
// be mapped, and deflating the container recovers that. Only formats that are
// wholly compressed streams are stored.
func TestOnlyWhollyCompressedFormatsAreStored(t *testing.T) {
	for _, ext := range []string{".gz", ".xz", ".zst"} {
		if !storeExts[ext] {
			t.Errorf("%s is a compressed stream and should be stored", ext)
		}
	}
	for _, ext := range []string{".apk", ".apex", ".jar", ".xml", ".sh"} {
		if storeExts[ext] {
			t.Errorf("%s compresses further and should be deflated", ext)
		}
	}
}

func TestPayloadsAreDeflated(t *testing.T) {
	out, _ := buildTo(t, TargetRecovery)
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".apk") && f.Method != zip.Deflate {
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
		if f.Name == "files/product/priv-app/GmsCore/lib/arm64/libjni.so" ||
			f.Name == "files/product/priv-app/GmsCore/lib/arm64/libjni.so.xz" {
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
	// Columns are path, mode, context, size, owning package, link target. The
	// owner is what lets the installer name what it is writing; the link
	// target stays last so the optional field still is.
	want := "product/priv-app/GmsCore/lib/arm64/libjni.so\t0777\tu:object_r:system_file:s0\t0\t\t/product/lib64/libjni.so"
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

// emptyPlan includes a zero-length file. Dumps contain these (ART profile
// placeholders); they have no asset because a release host rejects a
// zero-length upload.
func emptyPlan(t *testing.T) *stage.Plan {
	t.Helper()
	p := testPlan(t)
	p.Entries = append(p.Entries, stage.Entry{
		Path: "product/priv-app/GmsCore/GmsCore.apk.prof",
		Mode: 0o644, Kind: manifest.KindEtc, Size: 0,
	})
	return p
}

func TestEmptyFilesAreCarriedWithoutAnAsset(t *testing.T) {
	for _, target := range []Target{TargetRecovery, TargetModule} {
		out := filepath.Join(t.TempDir(), "o.zip")
		if _, err := Build(emptyPlan(t), Options{Target: target, Out: out}); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		zr, err := zip.OpenReader(out)
		if err != nil {
			t.Fatal(err)
		}
		want := "files/product/priv-app/GmsCore/GmsCore.apk.prof.xz"
		xzd := true
		if target == TargetModule {
			want = "system/product/priv-app/GmsCore/GmsCore.apk.prof"
			xzd = false
		}
		var found bool
		for _, f := range zr.File {
			if f.Name != want {
				continue
			}
			found = true
			if xzd {
				// xz of nothing is a small stream that decompresses to empty.
				rc, _ := f.Open()
				b, _ := io.ReadAll(rc)
				rc.Close()
				if n := decompressXZLen(t, b); n != 0 {
					t.Errorf("%s: %s should decompress to empty, got %d bytes", target, want, n)
				}
			} else if f.UncompressedSize64 != 0 {
				t.Errorf("%s: %s should be empty, got %d bytes", target, want, f.UncompressedSize64)
			}
		}
		zr.Close()
		if !found {
			t.Errorf("%s: %s missing", target, want)
		}
	}
}

func TestEmptyFilesGetNoDigestLine(t *testing.T) {
	out := filepath.Join(t.TempDir(), "o.zip")
	if _, err := Build(emptyPlan(t), Options{Target: TargetRecovery, Out: out}); err != nil {
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
		if strings.Contains(string(b), ".prof") {
			t.Errorf("an empty file has no digest to record:\n%s", b)
		}
	}
}

func readZipEntry(t *testing.T, path, name string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	t.Fatalf("%s not in %s", name, path)
	return ""
}

// The installer names each package as it writes it, which it can only do if
// files.list says which package every file came from.
func TestRecoveryFilesListNamesTheOwningPackage(t *testing.T) {
	out := filepath.Join(t.TempDir(), "r.zip")
	if _, err := Build(testPlan(t), Options{Target: TargetRecovery, Out: out}); err != nil {
		t.Fatal(err)
	}
	list := readZipEntry(t, out, "installer/files.list")
	for _, line := range strings.Split(strings.TrimSpace(list), "\n") {
		cols := strings.Split(line, "\t")
		if len(cols) < 5 {
			t.Fatalf("record has no owner column: %q", line)
		}
		if cols[4] == "" {
			t.Errorf("no owning package for %s", cols[0])
		}
	}
}

// Zips built on windows shipped scripts with crlf endings, which recovery
// cannot exec. Whatever the checkout did, no script may carry a \r.
func TestScriptsHaveUnixLineEndings(t *testing.T) {
	for _, target := range []Target{TargetRecovery, TargetModule} {
		_, files := buildTo(t, target)
		for name, body := range files {
			if strings.HasPrefix(name, "files/") {
				continue
			}
			if strings.Contains(body, "\r") {
				t.Errorf("%s: %s has a carriage return", target, name)
			}
		}
	}
}

func TestWipeFRPMarkerOnlyWhenAsked(t *testing.T) {
	read := func(opt Options) map[string]string {
		opt.Out = filepath.Join(t.TempDir(), "out.zip")
		opt.Target = TargetRecovery
		if _, err := Build(testPlan(t), opt); err != nil {
			t.Fatal(err)
		}
		zr, err := zip.OpenReader(opt.Out)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		out := map[string]string{}
		for _, f := range zr.File {
			out[f.Name] = ""
		}
		return out
	}
	if _, ok := read(Options{})["installer/wipe-frp"]; ok {
		t.Error("wipe-frp marker present without WipeFRP")
	}
	if _, ok := read(Options{WipeFRP: true})["installer/wipe-frp"]; !ok {
		t.Error("wipe-frp marker missing with WipeFRP")
	}
}

// decompressXZLen returns the decompressed length of an xz stream, for tests.
func decompressXZLen(t *testing.T, b []byte) int {
	t.Helper()
	r, err := xz.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("xz reader: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("xz read: %v", err)
	}
	return len(out)
}

// Without a system xz and without ForceXZ (the Windows default), payloads are
// stored with deflate and no compression marker; the installer's plain path
// then applies.
func TestRecoveryDeflateWhenNoXZ(t *testing.T) {
	orig := systemXZ
	systemXZ = func() bool { return false }
	defer func() { systemXZ = orig }()

	out := filepath.Join(t.TempDir(), "d.zip")
	if _, err := Build(testPlan(t), Options{Target: TargetRecovery, Out: out}); err != nil {
		t.Fatal(err)
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
	if names["installer/compression"] {
		t.Error("deflate build should not carry a compression marker")
	}
	if !names["files/product/priv-app/GmsCore/GmsCore.apk"] {
		t.Error("deflate build should store the plain payload path")
	}
	if names["files/product/priv-app/GmsCore/GmsCore.apk.xz"] {
		t.Error("deflate build should not xz the payload")
	}
}
