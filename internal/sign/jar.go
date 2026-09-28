package sign

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/smallstep/pkcs7"
)

// Alias is the base name of the signature files inside META-INF; recovery accepts any name.
const Alias = "306GAPPS"

// The JAR format wraps at 72 bytes and spec-following readers reject longer lines.
const manifestLineLimit = 72

// Zip writes a copy of in at out, signed in the JAR form recoveries understand.
func Zip(in, out string, key *Key) error {
	zr, err := zip.OpenReader(in)
	if err != nil {
		return err
	}
	defer zr.Close()

	manifest, sections, err := buildManifest(zr)
	if err != nil {
		return err
	}
	sf := buildSignatureFile(manifest, sections)
	rsa, err := signDetached(sf, key)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)

	// The signature files come first, as a JAR reader expects.
	for _, e := range []struct {
		name string
		body []byte
	}{
		{"META-INF/MANIFEST.MF", manifest},
		{"META-INF/" + Alias + ".SF", sf},
		{"META-INF/" + Alias + ".RSA", rsa},
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name: e.name, Method: zip.Deflate, Modified: epoch,
		})
		if err != nil {
			return err
		}
		if _, err := w.Write(e.body); err != nil {
			return err
		}
	}

	for _, entry := range zr.File {
		if isSignatureFile(entry.Name) {
			continue
		}
		if err := zw.Copy(entry); err != nil {
			return fmt.Errorf("copy %s: %w", entry.Name, err)
		}
	}

	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

// epoch keeps a signed zip reproducible, matching the builder's own timestamp.
var epoch = notBefore

// isSignatureFile reports entries from a previous signature, which must not be carried over.
func isSignatureFile(name string) bool {
	if !strings.HasPrefix(name, "META-INF/") {
		return false
	}
	upper := strings.ToUpper(name)
	return strings.HasSuffix(upper, ".SF") ||
		strings.HasSuffix(upper, ".RSA") ||
		strings.HasSuffix(upper, ".DSA") ||
		strings.HasSuffix(upper, ".EC") ||
		upper == "META-INF/MANIFEST.MF"
}

// buildManifest digests every entry, returning the manifest and the per-entry
// sections the signature file digests in turn.
func buildManifest(zr *zip.ReadCloser) ([]byte, map[string][]byte, error) {
	var main bytes.Buffer
	main.WriteString("Manifest-Version: 1.0\r\n")
	main.WriteString("Created-By: 306gapps\r\n")
	main.WriteString("\r\n")

	names := make([]string, 0, len(zr.File))
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || isSignatureFile(f.Name) {
			continue
		}
		names = append(names, f.Name)
		byName[f.Name] = f
	}
	sort.Strings(names)

	sections := make(map[string][]byte, len(names))
	body := &bytes.Buffer{}
	for _, name := range names {
		rc, err := byName[name].Open()
		if err != nil {
			return nil, nil, err
		}
		h := sha256.New()
		if _, err := io.Copy(h, rc); err != nil {
			rc.Close()
			return nil, nil, err
		}
		rc.Close()

		var sec bytes.Buffer
		writeHeader(&sec, "Name", name)
		writeHeader(&sec, "SHA-256-Digest", base64.StdEncoding.EncodeToString(h.Sum(nil)))
		sec.WriteString("\r\n")

		sections[name] = sec.Bytes()
		body.Write(sec.Bytes())
	}

	return append(main.Bytes(), body.Bytes()...), sections, nil
}

// buildSignatureFile digests the manifest as a whole and each of its sections.
func buildSignatureFile(manifest []byte, sections map[string][]byte) []byte {
	var b bytes.Buffer
	b.WriteString("Signature-Version: 1.0\r\n")
	b.WriteString("Created-By: 306gapps\r\n")
	sum := sha256.Sum256(manifest)
	writeHeader(&b, "SHA-256-Digest-Manifest", base64.StdEncoding.EncodeToString(sum[:]))
	b.WriteString("\r\n")

	names := make([]string, 0, len(sections))
	for n := range sections {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		h := sha256.Sum256(sections[name])
		writeHeader(&b, "Name", name)
		writeHeader(&b, "SHA-256-Digest", base64.StdEncoding.EncodeToString(h[:]))
		b.WriteString("\r\n")
	}
	return b.Bytes()
}

// writeHeader emits one header, wrapped at the line limit with a leading space on continuations.
func writeHeader(b *bytes.Buffer, key, value string) {
	line := key + ": " + value
	for len(line) > manifestLineLimit {
		b.WriteString(line[:manifestLineLimit])
		b.WriteString("\r\n ")
		line = line[manifestLineLimit:]
		// A continuation line's leading space counts toward the limit.
		if len(line) > manifestLineLimit-1 {
			b.WriteString(line[:manifestLineLimit-1])
			b.WriteString("\r\n ")
			line = line[manifestLineLimit-1:]
		}
	}
	b.WriteString(line)
	b.WriteString("\r\n")
}

// signDetached produces the PKCS#7 block over the signature file.
func signDetached(sf []byte, key *Key) ([]byte, error) {
	sd, err := pkcs7.NewSignedData(sf)
	if err != nil {
		return nil, err
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSignerChain(key.Cert, key.Private, nil, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, err
	}
	// Detached: the content is the .SF file already in the archive.
	sd.Detach()
	return sd.Finish()
}

// Verify re-derives the manifest from a signed zip and checks it against the recorded one.
func Verify(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()

	var recorded []byte
	for _, f := range zr.File {
		if f.Name != "META-INF/MANIFEST.MF" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		recorded, err = io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	if recorded == nil {
		return fmt.Errorf("%s carries no signature", path)
	}

	rebuilt, _, err := buildManifest(zr)
	if err != nil {
		return err
	}
	if !bytes.Equal(recorded, rebuilt) {
		return fmt.Errorf("%s has been altered since it was signed", path)
	}
	return nil
}

var _ = crypto.SHA256
