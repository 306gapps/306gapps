// Package source fetches release indexes, manifests and payloads through a content-addressed cache.
package source

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Cache stores payloads by digest, so a blob is downloaded at most once.
type Cache struct{ Dir string }

// DefaultCacheDir is ~/.cache/306gapps (or the XDG override).
func DefaultCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "306gapps")
	}
	return filepath.Join(os.TempDir(), "306gapps-cache")
}

func NewCache(dir string) *Cache {
	if dir == "" {
		dir = DefaultCacheDir()
	}
	return &Cache{Dir: dir}
}

// ErrCorrupt is returned when stored or fetched bytes do not match the digest.
var ErrCorrupt = errors.New("digest mismatch")

func (c *Cache) path(digest string) string {
	return filepath.Join(c.Dir, "blobs", digest[:2], digest)
}

// Has reports whether a verified copy of digest is already stored.
func (c *Cache) Has(digest string) bool {
	st, err := os.Stat(c.path(digest))
	return err == nil && st.Mode().IsRegular()
}

// Get returns the path to the cached blob.
func (c *Cache) Get(digest string) (string, error) {
	p := c.path(digest)
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

// Put streams r into the cache, verifying the digest and renaming into place only on success.
func (c *Cache) Put(digest string, r io.Reader) (string, error) {
	if len(digest) != 64 {
		return "", fmt.Errorf("malformed digest %q", digest)
	}
	final := c.path(digest)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(final), ".partial-*")
	if err != nil {
		return "", err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), r); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != digest {
		return "", fmt.Errorf("%w: want %s, got %s", ErrCorrupt, digest, got)
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", err
	}
	return final, nil
}

// Verify rehashes a stored blob and evicts it if it no longer matches.
func (c *Cache) Verify(digest string) error {
	p := c.path(digest)
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		os.Remove(p)
		return fmt.Errorf("%w for %s", ErrCorrupt, digest)
	}
	return nil
}

// Size reports the total bytes stored.
func (c *Cache) Size() (int64, error) {
	var n int64
	err := filepath.WalkDir(filepath.Join(c.Dir, "blobs"), func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		n += info.Size()
		return nil
	})
	return n, err
}

// Clear removes every stored blob.
func (c *Cache) Clear() error {
	err := os.RemoveAll(filepath.Join(c.Dir, "blobs"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
