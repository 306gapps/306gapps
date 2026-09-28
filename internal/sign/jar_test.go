package sign

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleZip(t *testing.T, dir string, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(dir, "in.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func testKey(t *testing.T) *Key {
	t.Helper()
	// 4096-bit generation is slow; the format does not care about size.
	k, err := generateSized("test", 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func signed(t *testing.T, entries map[string]string) (string, *Key) {
	t.Helper()
	dir := t.TempDir()
	in := sampleZip(t, dir, entries)
	out := filepath.Join(dir, "out.zip")
	k := testKey(t)
	if err := Zip(in, out, k); err != nil {
		t.Fatal(err)
	}
	return out, k
}

func names(t *testing.T, path string) []string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var out []string
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out
}

func read(t *testing.T, path, entry string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != entry {
			continue
		}
		rc, _ := f.Open()
		b := make([]byte, f.UncompressedSize64)
		rc.Read(b)
		rc.Close()
		return string(b)
	}
	t.Fatalf("%s not in %s", entry, path)
	return ""
}

func TestSignAddsTheThreeSignatureFilesFirst(t *testing.T) {
	out, _ := signed(t, map[string]string{
		"META-INF/com/google/android/update-binary": "#!/sbin/sh\n",
		"files/product/app/A/A.apk":                 "payload",
	})
	got := names(t, out)
	want := []string{"META-INF/MANIFEST.MF", "META-INF/" + Alias + ".SF", "META-INF/" + Alias + ".RSA"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("entry %d is %q, want %q", i, got[i], w)
		}
	}
}

func TestSignKeepsEveryOriginalEntry(t *testing.T) {
	entries := map[string]string{
		"META-INF/com/google/android/update-binary": "#!/sbin/sh\n",
		"installer/installer.sh":                    "echo hi\n",
		"files/product/app/A/A.apk":                 "payload",
	}
	out, _ := signed(t, entries)
	got := map[string]bool{}
	for _, n := range names(t, out) {
		got[n] = true
	}
	for n := range entries {
		if !got[n] {
			t.Errorf("%s did not survive signing", n)
		}
	}
	if read(t, out, "files/product/app/A/A.apk") != "payload" {
		t.Error("payload altered by signing")
	}
}

func TestManifestDigestsEveryEntry(t *testing.T) {
	out, _ := signed(t, map[string]string{
		"a.txt": "one", "b.txt": "two",
	})
	mf := read(t, out, "META-INF/MANIFEST.MF")
	for _, n := range []string{"a.txt", "b.txt"} {
		if !strings.Contains(mf, "Name: "+n) {
			t.Errorf("manifest omits %s:\n%s", n, mf)
		}
	}
	if strings.Count(mf, "SHA-256-Digest:") != 2 {
		t.Errorf("want two digests:\n%s", mf)
	}
}

func TestVerifyAcceptsAnUntouchedZip(t *testing.T) {
	out, _ := signed(t, map[string]string{"a.txt": "one"})
	if err := Verify(out); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsAnAlteredZip(t *testing.T) {
	out, k := signed(t, map[string]string{"a.txt": "one", "b.txt": "two"})

	// Rebuild with one entry's content changed, keeping the signature files.
	tampered := filepath.Join(t.TempDir(), "bad.zip")
	zr, _ := zip.OpenReader(out)
	f, _ := os.Create(tampered)
	zw := zip.NewWriter(f)
	for _, e := range zr.File {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: e.Name, Method: zip.Deflate})
		rc, _ := e.Open()
		b := make([]byte, e.UncompressedSize64)
		rc.Read(b)
		rc.Close()
		if e.Name == "a.txt" {
			b = []byte("ONE")
		}
		w.Write(b)
	}
	zw.Close()
	f.Close()
	zr.Close()

	if err := Verify(tampered); err == nil {
		t.Fatal("an altered payload must not verify")
	}
	_ = k
}

func TestVerifyRejectsAnUnsignedZip(t *testing.T) {
	dir := t.TempDir()
	in := sampleZip(t, dir, map[string]string{"a.txt": "one"})
	if err := Verify(in); err == nil {
		t.Fatal("an unsigned zip must not verify")
	}
}

func TestSigningIsReproducible(t *testing.T) {
	dir := t.TempDir()
	in := sampleZip(t, dir, map[string]string{"a.txt": "one", "b.txt": "two"})
	k := testKey(t)

	digests := make([]string, 2)
	for i := range digests {
		out := filepath.Join(dir, "o.zip")
		if err := Zip(in, out, k); err != nil {
			t.Fatal(err)
		}
		digests[i] = read(t, out, "META-INF/MANIFEST.MF")
		os.Remove(out)
	}
	if digests[0] != digests[1] {
		t.Error("the same input and key must produce the same manifest")
	}
}

func TestReSigningDropsThePreviousSignature(t *testing.T) {
	out, k := signed(t, map[string]string{"a.txt": "one"})
	again := filepath.Join(t.TempDir(), "again.zip")
	if err := Zip(out, again, k); err != nil {
		t.Fatal(err)
	}
	var mf int
	for _, n := range names(t, again) {
		if n == "META-INF/MANIFEST.MF" {
			mf++
		}
	}
	if mf != 1 {
		t.Fatalf("want exactly one manifest, got %d", mf)
	}
	if err := Verify(again); err != nil {
		t.Fatal(err)
	}
}

func TestLongEntryNamesWrapAtTheFormatLimit(t *testing.T) {
	long := "files/product/priv-app/PrebuiltGmsCore/app_chimera/m/PrebuiltGmsCoreVic_MeasurementDynamite.apk"
	out, _ := signed(t, map[string]string{long: "x"})
	mf := read(t, out, "META-INF/MANIFEST.MF")
	for _, line := range strings.Split(mf, "\r\n") {
		if len(line) > manifestLineLimit {
			t.Errorf("line exceeds the %d byte limit (%d): %q", manifestLineLimit, len(line), line)
		}
	}
	if err := Verify(out); err != nil {
		t.Fatal(err)
	}
}
