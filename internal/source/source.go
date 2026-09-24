package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/306gapps/306gapps/internal/manifest"
)

// IndexSchema is the index version this build understands.
const IndexSchema = 1

// Index is the assets repo's catalogue of published releases.
type Index struct {
	Schema   int          `json:"schema"`
	Repo     string       `json:"repo"`
	Updated  time.Time    `json:"updated"`
	Releases []ReleaseRef `json:"releases"`
	// Tools are helper binaries, versioned with the assets repo so a new busybox
	// does not need a new release of the builder.
	Tools Tools `json:"tools,omitempty"`
}

// Tools lists helper binaries, keyed by device architecture.
type Tools struct {
	Busybox map[string]Payload `json:"busybox,omitempty"`
}

// Payload is a single downloadable, verified blob.
type Payload struct {
	Asset  string `json:"asset"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// Base is the URL prefix Asset resolves against, when it is not the source root.
	Base string `json:"base,omitempty"`
	// Version is informational, e.g. "1.36.1.1 (Magisk v30.7)".
	Version string `json:"version,omitempty"`
}

// ReleaseRef points at one published Pixel dump.
type ReleaseRef struct {
	ID      string           `json:"id"`
	Android manifest.Android `json:"android"`
	Device  string           `json:"device"`
	Build   string           `json:"build"`
	Branch  string           `json:"branch"`
	Created time.Time        `json:"created"`
	// Manifest and AssetBase are resolved relative to the source root.
	Manifest  string `json:"manifest"`
	AssetBase string `json:"asset_base"`
}

func (r ReleaseRef) String() string {
	return fmt.Sprintf("%s (Android %s, %s %s)", r.ID, r.Android.Version, r.Device, r.Build)
}

// Latest returns the newest release for an API level, or the newest overall
// when api is zero.
func (i *Index) Latest(api int) (ReleaseRef, bool) {
	var best ReleaseRef
	var found bool
	for _, r := range i.Releases {
		if api != 0 && r.Android.API != api {
			continue
		}
		if !found || r.Created.After(best.Created) {
			best, found = r, true
		}
	}
	return best, found
}

// APIs returns the distinct API levels present, newest first.
func (i *Index) APIs() []int {
	seen := map[int]bool{}
	var out []int
	for _, r := range i.Releases {
		if !seen[r.Android.API] {
			seen[r.Android.API] = true
			out = append(out, r.Android.API)
		}
	}
	for a := 0; a < len(out); a++ {
		for b := a + 1; b < len(out); b++ {
			if out[b] > out[a] {
				out[a], out[b] = out[b], out[a]
			}
		}
	}
	return out
}

// Progress reports download advancement for a single file.
type Progress func(file manifest.File, done, total int64)

// Source resolves a release index, manifests and payloads from an http(s) URL or a local directory.
type Source struct {
	Root   string
	Cache  *Cache
	Client *http.Client
}

func New(root string, cache *Cache) *Source {
	return &Source{
		Root:   strings.TrimSuffix(root, "/"),
		Cache:  cache,
		Client: &http.Client{Timeout: 0},
	}
}

// resolve joins a possibly-relative reference onto the source root.
func (s *Source) resolve(ref string) string {
	if ref == "" {
		return s.Root
	}
	if strings.Contains(ref, "://") {
		return ref
	}
	return s.Root + "/" + strings.TrimPrefix(ref, "/")
}

// open returns a reader for a resolved reference, local or remote.
func (s *Source) open(ctx context.Context, ref string) (io.ReadCloser, int64, error) {
	loc := s.resolve(ref)
	if u, err := url.Parse(loc); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("User-Agent", "306gapps")
		resp, err := s.Client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, 0, fmt.Errorf("fetch %s: %s", loc, resp.Status)
		}
		return resp.Body, resp.ContentLength, nil
	}
	loc = strings.TrimPrefix(loc, "file://")
	f, err := os.Open(loc)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// Index fetches and validates the release catalogue.
func (s *Source) Index(ctx context.Context) (*Index, error) {
	rc, _, err := s.open(ctx, "index.json")
	if err != nil {
		return nil, fmt.Errorf("read index: %w", err)
	}
	defer rc.Close()

	var idx Index
	if err := json.NewDecoder(rc).Decode(&idx); err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}
	if idx.Schema != IndexSchema {
		return nil, fmt.Errorf("index schema %d unsupported (want %d)", idx.Schema, IndexSchema)
	}
	// A fresh assets repo legitimately has no releases yet; the caller decides if that matters.
	return &idx, nil
}

// Manifest fetches and validates one release's manifest.
func (s *Source) Manifest(ctx context.Context, ref ReleaseRef) (*manifest.Manifest, error) {
	loc := ref.Manifest
	if loc == "" {
		loc = filepath.ToSlash(filepath.Join("releases", ref.ID, "manifest.json"))
	}
	rc, _, err := s.open(ctx, loc)
	if err != nil {
		return nil, fmt.Errorf("read manifest for %s: %w", ref.ID, err)
	}
	defer rc.Close()

	m, err := manifest.Load(rc)
	if err != nil {
		return nil, fmt.Errorf("manifest %s: %w", ref.ID, err)
	}
	if m.Release.AssetBase == "" {
		m.Release.AssetBase = ref.AssetBase
	}
	return m, nil
}

// Fetch returns a local path to a verified copy of f, downloading only on a cache miss.
func (s *Source) Fetch(ctx context.Context, m *manifest.Manifest, f manifest.File, p Progress) (string, error) {
	if s.Cache.Has(f.SHA256) {
		if p != nil {
			p(f, f.Size, f.Size)
		}
		return s.Cache.Get(f.SHA256)
	}

	ref := f.Asset
	if base := m.Release.AssetBase; base != "" && !strings.Contains(ref, "://") {
		ref = strings.TrimSuffix(base, "/") + "/" + ref
	}
	rc, total, err := s.open(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", f.Path, err)
	}
	defer rc.Close()

	if total <= 0 {
		total = f.Size
	}
	var r io.Reader = rc
	if p != nil {
		r = &progressReader{r: rc, total: total, file: f, cb: p}
	}
	path, err := s.Cache.Put(f.SHA256, r)
	if err != nil {
		return "", fmt.Errorf("cache %s: %w", f.Path, err)
	}
	return path, nil
}

// FetchPayload downloads and verifies a helper binary into the same cache as release payloads.
func (s *Source) FetchPayload(ctx context.Context, p Payload) (string, error) {
	if p.Asset == "" || p.SHA256 == "" {
		return "", fmt.Errorf("payload is incomplete")
	}
	if s.Cache.Has(p.SHA256) {
		return s.Cache.Get(p.SHA256)
	}
	ref := p.Asset
	if p.Base != "" && !strings.Contains(ref, "://") {
		ref = strings.TrimSuffix(p.Base, "/") + "/" + ref
	}
	rc, _, err := s.open(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", p.Asset, err)
	}
	defer rc.Close()

	path, err := s.Cache.Put(p.SHA256, rc)
	if err != nil {
		return "", fmt.Errorf("cache %s: %w", p.Asset, err)
	}
	return path, nil
}

// BusyboxFor returns the busybox payload for an architecture, if published.
func (i *Index) BusyboxFor(arch string) (Payload, bool) {
	p, ok := i.Tools.Busybox[arch]
	return p, ok
}

type progressReader struct {
	r     io.Reader
	done  int64
	total int64
	file  manifest.File
	cb    Progress
}

func (pr *progressReader) Read(b []byte) (int, error) {
	n, err := pr.r.Read(b)
	if n > 0 {
		pr.done += int64(n)
		pr.cb(pr.file, pr.done, pr.total)
	}
	return n, err
}
