package stage

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/306gapps/306gapps/internal/manifest"
)

func writeAPK(t *testing.T, dir, name string, valid bool) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !valid {
		// A plausible-looking prefix with the central directory cut off, which
		// is exactly what a truncating extractor leaves behind.
		f.Write([]byte("PK\x03\x04" + strings.Repeat("\x00", 512)))
		return p
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("AndroidManifest.xml")
	w.Write([]byte("ok"))
	zw.Close()
	return p
}

func TestVerifyAcceptsGoodArchives(t *testing.T) {
	dir := t.TempDir()
	p := &Plan{Entries: []Entry{
		{Path: "product/app/A/A.apk", Local: writeAPK(t, dir, "a.apk", true), Size: 10},
		{Path: "product/apex/x.apex", Local: writeAPK(t, dir, "x.apex", true), Size: 10},
	}}
	if err := p.Verify(2); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCatchesATruncatedPayload(t *testing.T) {
	dir := t.TempDir()
	p := &Plan{Entries: []Entry{
		{Path: "product/app/Good/Good.apk", Local: writeAPK(t, dir, "g.apk", true), Size: 10},
		{Path: "product/apex/gms.apex", Local: writeAPK(t, dir, "bad.apex", false), Size: 10},
	}}
	err := p.Verify(2)
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("want CorruptError, got %v", err)
	}
	if len(ce.Found) != 1 || ce.Found[0].Path != "product/apex/gms.apex" {
		t.Fatalf("wrong finding: %+v", ce.Found)
	}
	// The message must not send someone chasing a retry.
	if !strings.Contains(err.Error(), "assets repo") {
		t.Errorf("message does not point at the real cause:\n%s", err)
	}
}

func TestVerifySkipsNonArchives(t *testing.T) {
	dir := t.TempDir()
	xml := filepath.Join(dir, "p.xml")
	os.WriteFile(xml, []byte("<permissions/>"), 0o644)
	p := &Plan{Entries: []Entry{
		{Path: "product/etc/permissions/p.xml", Local: xml, Size: 14},
	}}
	if err := p.Verify(2); err != nil {
		t.Fatalf("an xml is not an archive: %v", err)
	}
}

func TestVerifySkipsSymlinksAndEmptyFiles(t *testing.T) {
	p := &Plan{Entries: []Entry{
		{Path: "product/app/A/lib.so", Kind: manifest.KindSymlink, Target: "/product/lib64/lib.so"},
		{Path: "product/app/A/A.apk.prof", Kind: manifest.KindEtc, Size: 0},
	}}
	if err := p.Verify(2); err != nil {
		t.Fatalf("neither has a payload to open: %v", err)
	}
}

func TestVerifyReportsEveryBadPayload(t *testing.T) {
	dir := t.TempDir()
	p := &Plan{Entries: []Entry{
		{Path: "a/app/x.apk", Local: writeAPK(t, dir, "1.apk", false), Size: 1},
		{Path: "b/app/y.jar", Local: writeAPK(t, dir, "2.jar", false), Size: 1},
	}}
	var ce *CorruptError
	if !errors.As(p.Verify(2), &ce) || len(ce.Found) != 2 {
		t.Fatalf("want both reported, got %+v", ce)
	}
}
