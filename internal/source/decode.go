package source

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"

	"github.com/306gapps/306gapps/internal/manifest"
)

// decode undoes a download's transport compression and returns a check to run once
// the stream is drained. It digests the artifact as downloaded; the cache digests
// the decoded bytes separately.
func decode(r io.Reader, f manifest.File) (io.Reader, func() error, error) {
	if !f.Compressed() {
		return r, func() error { return nil }, nil
	}
	if f.Encoding != manifest.EncodingGzip {
		return nil, nil, fmt.Errorf("%s: unknown encoding %q", f.Path, f.Encoding)
	}

	hr := &hashReader{r: r, h: sha256.New()}
	zr, err := gzip.NewReader(hr)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", f.Path, err)
	}
	check := func() error {
		if err := zr.Close(); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		// Drain the rest so the digest covers the whole artifact, not just what gzip read.
		if _, err := io.Copy(io.Discard, hr); err != nil {
			return err
		}
		if got := hex.EncodeToString(hr.h.Sum(nil)); got != f.AssetSHA256 {
			return fmt.Errorf("%w: %s downloaded corrupt (artifact digest %s, want %s)",
				ErrCorrupt, f.Path, got, f.AssetSHA256)
		}
		if hr.n != f.AssetSize {
			return fmt.Errorf("%w: %s is %d bytes, the manifest says %d",
				ErrCorrupt, f.Path, hr.n, f.AssetSize)
		}
		return nil
	}
	return zr, check, nil
}

type hashReader struct {
	r io.Reader
	h hash.Hash
	n int64
}

func (hr *hashReader) Read(b []byte) (int, error) {
	n, err := hr.r.Read(b)
	if n > 0 {
		hr.h.Write(b[:n])
		hr.n += int64(n)
	}
	return n, err
}
