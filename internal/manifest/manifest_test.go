package manifest

import (
	"strings"
	"testing"
)

const digest = "0000000000000000000000000000000000000000000000000000000000000000"

func base() *Manifest {
	return &Manifest{
		Schema:  Schema,
		Release: Release{ID: "a16-test", Android: Android{API: 36, Version: "16"}},
		Groups:  []Group{{ID: "core", Name: "Core"}},
		Packages: []Package{{
			ID: "gmscore", Name: "Google Play services", Group: "core", Required: true,
			Files: []File{{Path: "product/priv-app/GmsCore/GmsCore.apk", Asset: "gmscore.apk",
				SHA256: digest, Size: 1, Mode: "0644", Kind: KindAPK}},
		}},
	}
}

func wantErr(t *testing.T, m *Manifest, substr string) {
	t.Helper()
	err := m.Validate()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("error %q does not contain %q", err, substr)
	}
}

func TestValidateAcceptsGoodManifest(t *testing.T) {
	if err := base().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsDuplicateID(t *testing.T) {
	m := base()
	m.Packages = append(m.Packages, m.Packages[0])
	wantErr(t, m, "duplicate package id")
}

func TestValidateRejectsUnknownPartition(t *testing.T) {
	m := base()
	m.Packages[0].Files[0].Path = "odm/app/Foo/Foo.apk"
	wantErr(t, m, "unknown partition")
}

func TestValidateRejectsAbsolutePath(t *testing.T) {
	m := base()
	m.Packages[0].Files[0].Path = "/product/app/Foo/Foo.apk"
	wantErr(t, m, "partition-relative")
}

func TestValidateRejectsUncleanPath(t *testing.T) {
	m := base()
	m.Packages[0].Files[0].Path = "product/app/../app/Foo.apk"
	wantErr(t, m, "not clean")
}

func TestValidateRejectsUnknownDependency(t *testing.T) {
	m := base()
	m.Packages[0].Requires = []string{"ghost"}
	wantErr(t, m, `requires unknown package "ghost"`)
}

func TestValidateRejectsPathCollisionAcrossPackages(t *testing.T) {
	m := base()
	dup := m.Packages[0]
	dup.ID = "other"
	dup.Required = false
	m.Packages = append(m.Packages, dup)
	wantErr(t, m, "also provided by")
}

func TestValidateRejectsBadDigest(t *testing.T) {
	m := base()
	m.Packages[0].Files[0].SHA256 = "abc"
	wantErr(t, m, "malformed sha256")
}

func TestValidateRejectsConflictingRequiredPackages(t *testing.T) {
	m := base()
	m.Packages = append(m.Packages, Package{
		ID: "alt", Name: "Alt", Group: "core", Required: true,
		Conflicts: []string{"gmscore"},
		Files:     []File{{Path: "product/app/Alt/Alt.apk", Asset: "alt.apk", SHA256: digest, Kind: KindAPK}},
	})
	wantErr(t, m, "both required but conflict")
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	_, err := Load(strings.NewReader(`{"schema":1,"bogus":true}`))
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("want unknown-field error, got %v", err)
	}
}

func TestFileModeAndPartition(t *testing.T) {
	f := File{Path: "system_ext/priv-app/X/X.apk", Mode: "0755"}
	if f.Partition() != "system_ext" {
		t.Fatalf("got %q", f.Partition())
	}
	if f.FileMode() != 0o755 {
		t.Fatalf("got %o", f.FileMode())
	}
	if (File{}).FileMode() != 0o644 {
		t.Fatal("want default 0644")
	}
}

func TestValidateAcceptsASymlink(t *testing.T) {
	m := base()
	m.Packages[0].Files = append(m.Packages[0].Files, File{
		Path: "product/priv-app/GmsCore/lib/arm64/libjni.so",
		Mode: "0777", Kind: KindSymlink, Target: "/product/lib64/libjni.so",
	})
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAcceptsARelativeSymlink(t *testing.T) {
	m := base()
	m.Packages[0].Files = append(m.Packages[0].Files, File{
		Path: "product/app/Foo/lib/arm64/libx.so",
		Mode: "0777", Kind: KindSymlink, Target: "../../../../lib64/libx.so",
	})
	if err := m.Validate(); err != nil {
		t.Fatalf("relative link targets are legitimate: %v", err)
	}
}

func TestValidateRejectsSymlinkWithoutTarget(t *testing.T) {
	m := base()
	m.Packages[0].Files = append(m.Packages[0].Files, File{
		Path: "product/app/Foo/x.so", Mode: "0777", Kind: KindSymlink,
	})
	wantErr(t, m, "has no target")
}

func TestValidateRejectsSymlinkCarryingAPayload(t *testing.T) {
	m := base()
	m.Packages[0].Files = append(m.Packages[0].Files, File{
		Path: "product/app/Foo/x.so", Mode: "0777", Kind: KindSymlink,
		Target: "/product/lib64/x.so", Asset: "x.so", SHA256: digest,
	})
	wantErr(t, m, "must not carry a payload")
}

func TestValidateRejectsSymlinkLeavingTheInstalledPartitions(t *testing.T) {
	m := base()
	m.Packages[0].Files = append(m.Packages[0].Files, File{
		Path: "product/app/Foo/x.so", Mode: "0777", Kind: KindSymlink,
		Target: "/data/local/tmp/evil.so",
	})
	wantErr(t, m, "outside the partitions")
}

func TestArchitectureDefaultsToArm64(t *testing.T) {
	if (Release{}).Architecture() != "arm64" {
		t.Error("want arm64 by default")
	}
	if (Release{Arch: "arm"}).Architecture() != "arm" {
		t.Error("explicit arch ignored")
	}
}

func TestValidateAcceptsAnEmptyFileWithoutAnAsset(t *testing.T) {
	m := base()
	m.Packages[0].Files = append(m.Packages[0].Files, File{
		Path: "product/priv-app/GmsCore/GmsCore.apk.prof", Mode: "0644",
		Kind: KindEtc, Size: 0,
	})
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsAnEmptyFileCarryingAnAsset(t *testing.T) {
	m := base()
	m.Packages[0].Files = append(m.Packages[0].Files, File{
		Path: "product/app/Foo/Foo.prof", Mode: "0644", Kind: KindEtc,
		Size: 0, Asset: "foo.prof", SHA256: digest,
	})
	wantErr(t, m, "must not carry a payload")
}

func TestValidateStillRequiresAnAssetForRealFiles(t *testing.T) {
	m := base()
	m.Packages[0].Files = append(m.Packages[0].Files, File{
		Path: "product/app/Foo/Foo.apk", Mode: "0644", Kind: KindAPK, Size: 10,
	})
	wantErr(t, m, "has no asset")
}

func TestValidateAcceptsRemovalByBareName(t *testing.T) {
	// A bare name covers every app location on every partition, which is how
	// one entry handles ROMs that disagree about where an app lives.
	m := base()
	m.Packages[0].Removes = []string{"Dialer", "product/app/messaging"}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsAbsoluteRemoval(t *testing.T) {
	m := base()
	m.Packages[0].Removes = []string{"/system/app/Foo"}
	wantErr(t, m, "bare name or partition-relative")
}

func TestValidateRejectsEscapingRemoval(t *testing.T) {
	m := base()
	m.Packages[0].Removes = []string{"product/../../etc"}
	wantErr(t, m, "escapes the partition")
}

func TestValidateRejectsRemovalOnAnUnknownPartition(t *testing.T) {
	m := base()
	m.Packages[0].Removes = []string{"odm/app/Foo"}
	wantErr(t, m, "unknown partition")
}

// A release published either side of a schema change is the common cause of a
// strict-decoding failure, and "unknown field" alone reads like corruption.
func TestLoadExplainsASchemaMismatch(t *testing.T) {
	_, err := Load(strings.NewReader(`{"schema":1,"packages":[{"category":"core"}]}`))
	if err == nil {
		t.Fatal("an unknown field should be refused")
	}
	for _, want := range []string{"different version of 306gapps", "category", "Update 306gapps"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q: %v", want, err)
		}
	}
}
