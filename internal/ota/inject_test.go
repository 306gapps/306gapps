package ota

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/stage"
)

// baseFiles is a miniature but structurally faithful target-files package.
var baseFiles = map[string]string{
	"META/misc_info.txt": strings.Join([]string{
		"ab_update=true",
		"product_fs_type=erofs",
		"product_size=1048576",
		"system_size=2097152",
		"", // trailing newline
	}, "\n"),
	"META/apkcerts.txt": strings.Join([]string{
		`name="Dialer.apk" certificate="build/target/product/security/platform.x509.pem" private_key="build/target/product/security/platform.pk8" partition="system_ext"`,
		`name="Settings.apk" certificate="build/target/product/security/platform.x509.pem" private_key="build/target/product/security/platform.pk8" partition="system"`,
		"",
	}, "\n"),
	"META/product_filesystem_config.txt": strings.Join([]string{
		"app 0 0 755 capabilities=0x0",
		"app/QuickSearchBox 0 0 755 capabilities=0x0",
		"app/QuickSearchBox/QuickSearchBox.apk 0 0 644 capabilities=0x0",
		"",
	}, "\n"),
	"META/system_ext_filesystem_config.txt": strings.Join([]string{
		"priv-app 0 0 755 capabilities=0x0",
		"priv-app/Dialer 0 0 755 capabilities=0x0",
		"priv-app/Dialer/Dialer.apk 0 0 644 capabilities=0x0",
		"",
	}, "\n"),
	"META/ab_partitions.txt":                        "product\nsystem\nsystem_ext\n",
	"PRODUCT/app/QuickSearchBox/QuickSearchBox.apk": "stock-search-box",
	"PRODUCT/etc/build.prop":                        "ro.product.name=test\n",
	"SYSTEM_EXT/priv-app/Dialer/Dialer.apk":         "stock-dialer",
	"SYSTEM/framework/framework.jar":                "framework",
	// Stale images that must not survive the merge.
	"IMAGES/product.img": "old-product-image",
	"IMAGES/system.img":  "old-system-image",
	"META/care_map.pb":   "old-care-map",
}

func writeBaseTargetFiles(t *testing.T, extra map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "base-target_files.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	all := map[string]string{}
	for k, v := range baseFiles {
		all[k] = v
	}
	for k, v := range extra {
		if v == "" {
			delete(all, k)
			continue
		}
		all[k] = v
	}
	for name, content := range all {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func testPlan(t *testing.T) *stage.Plan {
	t.Helper()
	dir := t.TempDir()
	payload := func(name, content string) (string, int64) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p, int64(len(content))
	}
	gms, gmsSize := payload("gms.apk", "gmscore-payload-bytes")
	perm, permSize := payload("perm.xml", "<permissions/>")
	dialer, dialerSize := payload("dialer.apk", "google-dialer-payload")

	return &stage.Plan{
		Release: manifest.Release{ID: "a17-cd1a.260905.001.b1"},
		Packages: []manifest.Package{
			{ID: "gmscore", Name: "Play services"},
			{ID: "dialer", Name: "Google Phone"},
		},
		Entries: []stage.Entry{
			{Path: "product/priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk",
				Local: gms, Mode: 0o644, Kind: manifest.KindAPK, Size: gmsSize},
			{Path: "product/etc/permissions/privapp-permissions-google-p.xml",
				Local: perm, Mode: 0o644, Kind: manifest.KindPermission, Size: permSize},
			{Path: "system_ext/priv-app/GoogleDialer/GoogleDialer.apk",
				Local: dialer, Mode: 0o644, Kind: manifest.KindAPK, Size: dialerSize},
		},
		Removes: []string{
			"product/app/QuickSearchBox",
			"system_ext/priv-app/Dialer",
		},
	}
}

func inject(t *testing.T, base string, opt InjectOptions) (*InjectResult, map[string]string) {
	t.Helper()
	opt.Base = base
	if opt.Out == "" {
		opt.Out = filepath.Join(t.TempDir(), "merged.zip")
	}
	res, err := Inject(testPlan(t), opt)
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	zr, err := zip.OpenReader(opt.Out)
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
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(b)
	}
	return res, got
}

func TestInjectPlacesPayloadsInPartitionTrees(t *testing.T) {
	_, files := inject(t, writeBaseTargetFiles(t, nil), InjectOptions{})

	if got := files["PRODUCT/priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk"]; got != "gmscore-payload-bytes" {
		t.Errorf("gmscore payload wrong: %q", got)
	}
	if got := files["SYSTEM_EXT/priv-app/GoogleDialer/GoogleDialer.apk"]; got != "google-dialer-payload" {
		t.Errorf("dialer payload wrong: %q", got)
	}
	if _, ok := files["PRODUCT/etc/permissions/privapp-permissions-google-p.xml"]; !ok {
		t.Error("permissions xml missing")
	}
}

func TestInjectPreservesUntouchedFiles(t *testing.T) {
	_, files := inject(t, writeBaseTargetFiles(t, nil), InjectOptions{})
	if files["SYSTEM/framework/framework.jar"] != "framework" {
		t.Error("untouched ROM file did not survive the merge")
	}
	if files["PRODUCT/etc/build.prop"] != "ro.product.name=test\n" {
		t.Error("build.prop did not survive the merge")
	}
	if files["META/ab_partitions.txt"] == "" {
		t.Error("ab_partitions.txt did not survive the merge")
	}
}

func TestInjectDropsStaleImages(t *testing.T) {
	_, files := inject(t, writeBaseTargetFiles(t, nil), InjectOptions{})
	for name := range files {
		if strings.HasPrefix(name, "IMAGES/") {
			t.Errorf("stale image survived: %s", name)
		}
	}
	if _, ok := files["META/care_map.pb"]; ok {
		t.Error("stale care_map survived; it describes the old images")
	}
}

func TestInjectRemovesSupersededApps(t *testing.T) {
	res, files := inject(t, writeBaseTargetFiles(t, nil), InjectOptions{})

	if _, ok := files["PRODUCT/app/QuickSearchBox/QuickSearchBox.apk"]; ok {
		t.Error("superseded QuickSearchBox survived")
	}
	if _, ok := files["SYSTEM_EXT/priv-app/Dialer/Dialer.apk"]; ok {
		t.Error("superseded AOSP Dialer survived")
	}
	if res.Removed != 2 {
		t.Errorf("want 2 removals, got %d", res.Removed)
	}

	// Their ownership records must go too, or the image builder trips on
	// entries for files that no longer exist.
	fsc := files["META/product_filesystem_config.txt"]
	if strings.Contains(fsc, "QuickSearchBox") {
		t.Errorf("removed app still recorded in filesystem_config:\n%s", fsc)
	}
	if strings.Contains(files["META/apkcerts.txt"], "QuickSearchBox.apk") {
		t.Error("removed app still recorded in apkcerts")
	}
	if strings.Contains(files["META/apkcerts.txt"], `name="Dialer.apk"`) {
		t.Error("superseded AOSP dialer still recorded in apkcerts")
	}
}

func TestInjectRecordsOwnershipForNewFiles(t *testing.T) {
	_, files := inject(t, writeBaseTargetFiles(t, nil), InjectOptions{})
	fsc := files["META/product_filesystem_config.txt"]

	for _, want := range []string{
		"priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk 0 0 644 capabilities=0x0",
		"priv-app/PrebuiltGmsCore 0 0 755 capabilities=0x0",
		"priv-app 0 0 755 capabilities=0x0",
		"etc/permissions 0 0 755 capabilities=0x0",
	} {
		if !strings.Contains(fsc, want) {
			t.Errorf("filesystem_config missing %q:\n%s", want, fsc)
		}
	}
}

func TestInjectMarksGoogleApksPresigned(t *testing.T) {
	_, files := inject(t, writeBaseTargetFiles(t, nil), InjectOptions{})
	certs := files["META/apkcerts.txt"]

	for _, want := range []string{
		`name="PrebuiltGmsCore.apk" certificate="PRESIGNED" private_key="" partition="product"`,
		`name="GoogleDialer.apk" certificate="PRESIGNED" private_key="" partition="system_ext"`,
	} {
		if !strings.Contains(certs, want) {
			t.Errorf("apkcerts missing %q:\n%s", want, certs)
		}
	}
	// Non-apk payloads are not apks and must not appear.
	if strings.Contains(certs, "privapp-permissions") {
		t.Error("a non-apk payload was recorded in apkcerts")
	}
	// The ROM's own entries stay signable.
	if !strings.Contains(certs, `name="Settings.apk"`) {
		t.Error("the ROM's own apkcerts entries were lost")
	}
}

func TestInjectMatchesThePrefixConventionOfTheBase(t *testing.T) {
	// Some ROMs record filesystem_config paths prefixed with the partition.
	prefixed := writeBaseTargetFiles(t, map[string]string{
		"META/product_filesystem_config.txt": strings.Join([]string{
			"product 0 0 755 capabilities=0x0",
			"product/app 0 0 755 capabilities=0x0",
			"product/app/QuickSearchBox 0 0 755 capabilities=0x0",
			"product/app/QuickSearchBox/QuickSearchBox.apk 0 0 644 capabilities=0x0",
			"",
		}, "\n"),
	})
	_, files := inject(t, prefixed, InjectOptions{})
	fsc := files["META/product_filesystem_config.txt"]

	if !strings.Contains(fsc, "product/priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk 0 0 644") {
		t.Errorf("did not follow the base's prefixed convention:\n%s", fsc)
	}
	if strings.Contains(fsc, "\npriv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk") {
		t.Errorf("wrote a bare path into a prefixed file:\n%s", fsc)
	}
	if strings.Contains(fsc, "QuickSearchBox") {
		t.Errorf("prefixed removal did not take:\n%s", fsc)
	}
}

func TestInjectReportsSizeGrowth(t *testing.T) {
	res, _ := inject(t, writeBaseTargetFiles(t, nil), InjectOptions{})
	if res.Delta["product"] <= 0 {
		t.Errorf("product should have grown, got %d", res.Delta["product"])
	}
	if res.Added != 3 {
		t.Errorf("want 3 files added, got %d", res.Added)
	}
	if len(res.Partitions) != 2 {
		t.Errorf("want product and system_ext, got %v", res.Partitions)
	}
}

func TestInjectWarnsWhenAPartitionWouldOverflow(t *testing.T) {
	tight := writeBaseTargetFiles(t, map[string]string{
		"META/misc_info.txt": "ab_update=true\nproduct_size=1024\n",
	})
	res, files := inject(t, tight, InjectOptions{})

	var warned bool
	for _, w := range res.Warnings {
		if strings.Contains(w, "/product grows by") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no size warning raised: %v", res.Warnings)
	}
	// Without -ota-grow the budget is left alone.
	if !strings.Contains(files["META/misc_info.txt"], "product_size=1024") {
		t.Error("budget was changed without --grow")
	}
}

func TestInjectGrowsThePartitionWhenAsked(t *testing.T) {
	tight := writeBaseTargetFiles(t, map[string]string{
		"META/misc_info.txt": "ab_update=true\nproduct_size=1024\n",
	})
	_, files := inject(t, tight, InjectOptions{Grow: true})
	if strings.Contains(files["META/misc_info.txt"], "product_size=1024\n") {
		t.Errorf("budget was not raised:\n%s", files["META/misc_info.txt"])
	}
	if !strings.Contains(files["META/misc_info.txt"], "ab_update=true") {
		t.Error("unrelated misc_info keys were lost")
	}
}

func TestInjectRejectsSomethingThatIsNotTargetFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "factory.zip")
	f, _ := os.Create(path)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("product.img")
	io.WriteString(w, "not a target-files package")
	zw.Close()
	f.Close()

	_, err := Inject(testPlan(t), InjectOptions{Base: path, Out: filepath.Join(t.TempDir(), "o.zip")})
	if err == nil || !strings.Contains(err.Error(), "target-files") {
		t.Fatalf("want a clear rejection, got %v", err)
	}
}

func TestInjectReplacesRatherThanDuplicating(t *testing.T) {
	// A base that already ships the file we are about to install.
	dup := writeBaseTargetFiles(t, map[string]string{
		"PRODUCT/priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk": "an-older-gmscore",
	})
	res, files := inject(t, dup, InjectOptions{})

	if files["PRODUCT/priv-app/PrebuiltGmsCore/PrebuiltGmsCore.apk"] != "gmscore-payload-bytes" {
		t.Error("the stale copy won")
	}
	if res.Replaced != 1 {
		t.Errorf("want 1 replacement, got %d", res.Replaced)
	}
}

func TestInjectRemovesByBareName(t *testing.T) {
	// A bare name has to reach the app wherever the ROM put it.
	base := writeBaseTargetFiles(t, nil)
	plan := testPlan(t)
	plan.Removes = []string{"QuickSearchBox", "Dialer"}

	out := filepath.Join(t.TempDir(), "merged.zip")
	res, err := Inject(plan, InjectOptions{Base: base, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if strings.Contains(f.Name, "QuickSearchBox") || strings.Contains(f.Name, "SYSTEM_EXT/priv-app/Dialer/") {
			t.Errorf("%s should have been removed by bare name", f.Name)
		}
	}
	if res.Removed != 2 {
		t.Errorf("want 2 removals, got %d", res.Removed)
	}
}

func TestRemovalPathsExpansion(t *testing.T) {
	got := removalPaths([]string{"Dialer"})
	want := map[string]bool{
		"system/app/Dialer": true, "system/priv-app/Dialer": true,
		"system_ext/app/Dialer": true, "system_ext/priv-app/Dialer": true,
		"product/app/Dialer": true, "product/priv-app/Dialer": true,
		"vendor/app/Dialer": true, "vendor/priv-app/Dialer": true,
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected expansion %q", g)
		}
		delete(want, g)
	}
	if len(want) > 0 {
		t.Errorf("missing expansions: %v", want)
	}
}

func TestRemovalPathsLeavesExactPathsAlone(t *testing.T) {
	got := removalPaths([]string{"product/app/messaging"})
	if len(got) != 1 || got[0] != "product/app/messaging" {
		t.Fatalf("got %v", got)
	}
}
