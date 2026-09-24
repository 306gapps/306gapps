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
		Packages: []Package{{
			ID: "gmscore", Name: "Google Play services", Category: "core", Required: true,
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
		ID: "alt", Name: "Alt", Category: "core", Required: true,
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
