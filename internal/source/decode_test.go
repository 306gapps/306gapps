package source

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/306gapps/306gapps/internal/manifest"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func gzipped(b []byte) []byte {
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	w.Write(b)
	w.Close()
	return out.Bytes()
}

// A compressed payload must come back byte-identical to what was compressed.
func TestDecodeRoundTrip(t *testing.T) {
	plain := bytes.Repeat([]byte("PK\x03\x04not really an apk"), 500)
	packed := gzipped(plain)
	f := manifest.File{
		Path: "product/app/X/X.apk", SHA256: sum(plain), Size: int64(len(plain)),
		Encoding: manifest.EncodingGzip, AssetSHA256: sum(packed),
		AssetSize: int64(len(packed)),
	}
	r, check, err := decode(bytes.NewReader(packed), f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := check(); err != nil {
		t.Fatalf("check: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Error("decoded bytes differ from the original")
	}
}

// An uncompressed file passes straight through, so the common path is unchanged.
func TestDecodeIdentity(t *testing.T) {
	f := manifest.File{Path: "x", SHA256: sum([]byte("hello"))}
	r, check, err := decode(strings.NewReader("hello"), f)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	if string(got) != "hello" || check() != nil {
		t.Errorf("identity path altered the stream: %q", got)
	}
}

// A corrupted download must be named as such, not surface as a gzip error or,
// worse, be accepted because the decompressor happened to cope.
func TestDecodeCatchesACorruptArtifact(t *testing.T) {
	plain := bytes.Repeat([]byte("payload"), 1000)
	packed := gzipped(plain)
	f := manifest.File{
		Path: "product/app/X/X.apk", SHA256: sum(plain), Size: int64(len(plain)),
		Encoding: manifest.EncodingGzip,
		// The artifact digest belongs to something else entirely.
		AssetSHA256: sum([]byte("a different artifact")),
		AssetSize:   int64(len(packed)),
	}
	r, check, err := decode(bytes.NewReader(packed), f)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, r)
	err = check()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	if !strings.Contains(err.Error(), "product/app/X/X.apk") {
		t.Errorf("the error should name the file: %v", err)
	}
}

func TestDecodeCatchesAWrongSize(t *testing.T) {
	plain := []byte("payload")
	packed := gzipped(plain)
	f := manifest.File{
		Path: "x", SHA256: sum(plain), Size: int64(len(plain)),
		Encoding: manifest.EncodingGzip, AssetSHA256: sum(packed),
		AssetSize: int64(len(packed)) + 99,
	}
	r, check, _ := decode(bytes.NewReader(packed), f)
	io.Copy(io.Discard, r)
	if err := check(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt for a size mismatch, got %v", err)
	}
}

func TestDecodeRefusesAnUnknownEncoding(t *testing.T) {
	f := manifest.File{Path: "x", Encoding: "brotli"}
	if _, _, err := decode(strings.NewReader("x"), f); err == nil {
		t.Error("an encoding we cannot undo should be refused up front")
	}
}

// Truncated mid-stream: gzip's own trailer check has to fire.
func TestDecodeCatchesATruncatedArtifact(t *testing.T) {
	plain := bytes.Repeat([]byte("payload"), 1000)
	packed := gzipped(plain)
	cut := packed[:len(packed)-20]
	f := manifest.File{
		Path: "x", SHA256: sum(plain), Size: int64(len(plain)),
		Encoding: manifest.EncodingGzip, AssetSHA256: sum(cut),
		AssetSize: int64(len(cut)),
	}
	r, check, err := decode(bytes.NewReader(cut), f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, r); err == nil {
		if err := check(); err == nil {
			t.Error("a truncated artifact should not verify")
		}
	}
}

func TestDownloadReportsTheWireSize(t *testing.T) {
	plain := manifest.File{Size: 100}
	if plain.Download() != 100 {
		t.Error("an uncompressed file downloads its own size")
	}
	packed := manifest.File{Size: 100, Encoding: manifest.EncodingGzip, AssetSize: 40}
	if packed.Download() != 40 {
		t.Errorf("a compressed file downloads its artifact size, got %d", packed.Download())
	}
}
