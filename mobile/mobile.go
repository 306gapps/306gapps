// Package mobile is the surface gomobile binds for the Android app. Anything
// richer than strings and numbers crosses as JSON, since gobind cannot carry
// slices or maps.
package mobile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/306gapps/306gapps/internal/build"
	"github.com/306gapps/306gapps/internal/catalog"
	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/sign"
	"github.com/306gapps/306gapps/internal/source"
	"github.com/306gapps/306gapps/internal/stage"
)

// Progress receives build status; frac is 0..1, or negative to leave the bar alone.
type Progress interface {
	Update(line string, frac float64)
}

// Session holds one picker's state. Every method is safe to call from any thread.
type Session struct {
	src       *source.Source
	configDir string

	mu       sync.Mutex
	index    *source.Index
	cat      *catalog.Catalog
	selected map[string]bool
	res      *catalog.Resolution
	resErr   error
	cancel   context.CancelFunc
}

// NewSession uses the published assets repo when root is empty.
func NewSession(root, cacheDir, configDir string) *Session {
	if root == "" {
		root = source.DefaultRoot
	}
	return &Session{
		src:       source.New(root, source.NewCache(cacheDir)),
		configDir: configDir,
		selected:  map[string]bool{},
	}
}

type releaseJSON struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	API     int    `json:"api"`
	Device  string `json:"device"`
	Build   string `json:"build"`
}

// Releases returns the newest build of each Android version, newest version first.
func (s *Session) Releases() (string, error) {
	idx, err := s.src.Index(context.Background())
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.index = idx
	s.mu.Unlock()

	out := []releaseJSON{}
	for _, api := range idx.APIs() {
		if r, ok := idx.Latest(api); ok {
			out = append(out, releaseJSON{r.ID, r.Android.Version, r.Android.API, r.Device, r.Build})
		}
	}
	return marshal(out)
}

type catalogJSON struct {
	Release  releaseJSON   `json:"release"`
	Groups   []groupJSON   `json:"groups"`
	Variants []variantJSON `json:"variants"`
}

type groupJSON struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Summary  string        `json:"summary"`
	Size     int64         `json:"size"`
	Packages []packageJSON `json:"packages"`
}

type packageJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Summary  string `json:"summary"`
	Size     int64  `json:"size"`
	Required bool   `json:"required"`
}

type variantJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// Load fetches a release's manifest and resets the selection to its defaults.
// Releases must have been called first.
func (s *Session) Load(releaseID string) (string, error) {
	s.mu.Lock()
	idx := s.index
	s.mu.Unlock()
	if idx == nil {
		return "", errors.New("no release index loaded")
	}
	var ref source.ReleaseRef
	found := false
	for _, r := range idx.Releases {
		if r.ID == releaseID {
			ref, found = r, true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("no release %q", releaseID)
	}

	m, err := s.src.Manifest(context.Background(), ref)
	if err != nil {
		return "", err
	}
	cat := catalog.New(m)

	s.mu.Lock()
	s.cat = cat
	s.selected = map[string]bool{}
	for _, id := range cat.Defaults() {
		s.selected[id] = true
	}
	s.resolve()
	s.mu.Unlock()

	out := catalogJSON{
		Release:  releaseJSON{ref.ID, ref.Android.Version, ref.Android.API, ref.Device, ref.Build},
		Groups:   []groupJSON{},
		Variants: []variantJSON{},
	}
	byGroup := cat.ByGroup()
	for _, g := range cat.Groups() {
		gj := groupJSON{ID: g.ID, Name: g.Name, Summary: g.Summary, Packages: []packageJSON{}}
		for _, p := range byGroup[g.ID] {
			gj.Packages = append(gj.Packages, packageJSON{p.ID, p.Name, p.Summary, p.Size(), p.Required})
			gj.Size += p.Size()
		}
		out.Groups = append(out.Groups, gj)
	}
	for _, v := range cat.Variants() {
		out.Variants = append(out.Variants, variantJSON{v.ID, v.Name, v.Summary})
	}
	return marshal(out)
}

type stateJSON struct {
	// Selected is what will be installed, dependencies included.
	Selected []string `json:"selected"`
	// Implied maps a package nobody ticked to the ids that pulled it in.
	Implied map[string][]string `json:"implied"`
	Count   int                 `json:"count"`
	Size    int64               `json:"size"`
	// Variant is the preset the selection matches exactly, or empty.
	Variant string `json:"variant"`
	Error   string `json:"error"`
}

// State reports the current resolution.
func (s *Session) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state()
}

// Toggle ticks or unticks one package. Taking one side of a conflict drops the other.
func (s *Session) Toggle(id string, on bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cat == nil {
		return s.state()
	}
	if p, ok := s.cat.Package(id); !ok || p.Required {
		return s.state()
	}
	s.selected[id] = on
	if on {
		for _, other := range s.cat.ConflictsWith(id) {
			s.selected[other] = false
		}
	}
	s.resolve()
	return s.state()
}

// SetGroup ticks or unticks a whole family. It cannot take both sides of a
// conflict, so the first in manifest order wins.
func (s *Session) SetGroup(group string, on bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cat == nil {
		return s.state()
	}
	taken := map[string]bool{}
	for _, p := range s.cat.ByGroup()[group] {
		if p.Required {
			continue
		}
		if on && s.conflictsWithAny(p.ID, taken) {
			s.selected[p.ID] = false
			continue
		}
		s.selected[p.ID] = on
		taken[p.ID] = on
	}
	s.resolve()
	return s.state()
}

// ApplyVariant replaces the selection with a preset.
func (s *Session) ApplyVariant(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cat == nil {
		return s.state()
	}
	if v, ok := s.cat.Variant(id); ok {
		s.selected = map[string]bool{}
		for _, pid := range s.cat.Prune(v.Packages) {
			s.selected[pid] = true
		}
		s.resolve()
	}
	return s.state()
}

type resultJSON struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Files  int    `json:"files"`
	SHA256 string `json:"sha256"`
}

// ZipName is what Build calls the zip when not given a name.
func (s *Session) ZipName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cat == nil {
		return ""
	}
	return defaultName(s.cat.Manifest().Release.ID)
}

func defaultName(releaseID string) string {
	return fmt.Sprintf("306gapps-%s-%s.zip", releaseID, build.TargetRecovery)
}

// zipName tidies a user-typed name; blank means the default.
func zipName(name, releaseID string) string {
	n := strings.TrimSpace(filepath.Base(strings.TrimSpace(name)))
	if n == "" || n == "." || n == ".." || n == "/" || n == ".zip" {
		return defaultName(releaseID)
	}
	if !strings.HasSuffix(strings.ToLower(n), ".zip") {
		n += ".zip"
	}
	return n
}

// Build downloads what the selection needs and writes a signed recovery zip
// called name into outDir. Cancel aborts it.
func (s *Session) Build(outDir, name string, p Progress) (string, error) {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return "", errors.New("a build is already running")
	}
	cat, res, resErr, idx := s.cat, s.res, s.resErr, s.index
	if cat == nil || res == nil {
		s.mu.Unlock()
		if resErr != nil {
			return "", resErr
		}
		return "", errors.New("no release loaded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		cancel()
	}()

	r, err := s.build(ctx, cat.Manifest(), res, idx, outDir, name, p.Update)
	if err != nil {
		if ctx.Err() != nil {
			return "", context.Canceled
		}
		return "", err
	}
	return marshal(r)
}

// Cancel stops a running build. It is a no-op when none is running.
func (s *Session) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// CacheSize is the bytes held in the download cache.
func (s *Session) CacheSize() int64 {
	n, _ := s.src.Cache.Size()
	return n
}

func (s *Session) ClearCache() error { return s.src.Cache.Clear() }

func (s *Session) build(ctx context.Context, m *manifest.Manifest, res *catalog.Resolution,
	idx *source.Index, outDir, name string, report func(string, float64)) (*resultJSON, error) {

	report("Fetching payloads…", 0)
	var (
		mu         sync.Mutex
		done       int
		got, total int64
		seen       = map[string]bool{}
		progress   = map[string]int64{}
	)
	for _, f := range res.Files {
		total += f.Download()
	}
	plan, err := stage.Build(ctx, s.src, m, res, stage.Options{
		Workers: 4,
		Progress: func(f manifest.File, n, want int64) {
			mu.Lock()
			defer mu.Unlock()
			got += n - progress[f.Path]
			progress[f.Path] = n
			if n >= want && !seen[f.Path] {
				seen[f.Path] = true
				done++
			}
			line := fmt.Sprintf("Downloading %d/%d · %s of %s",
				done, len(res.Files), humanSize(got), humanSize(total))
			report(line, float64(got)/float64(max(total, 1))*0.6)
		},
	})
	if err != nil {
		return nil, err
	}

	report("Verifying archives…", 0.62)
	if err := plan.Verify(4); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// A bundled busybox gives the installer one known toolset; missing is fine.
	busybox := ""
	if idx != nil {
		if pl, ok := idx.BusyboxFor(plan.Release.Architecture()); ok {
			if local, err := s.src.FetchPayload(ctx, pl); err == nil {
				busybox = local
			}
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	name = zipName(name, plan.Release.ID)
	out := filepath.Join(outDir, name)

	report("Packing…", 0.7)
	result, err := build.Build(plan, build.Options{
		Target:  build.TargetRecovery,
		Out:     out,
		Busybox: busybox,
		Progress: func(n, of int) {
			report(fmt.Sprintf("Packing %d/%d", n, of), 0.7+float64(n)/float64(max(of, 1))*0.25)
		},
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		os.Remove(out)
		return nil, err
	}

	report("Signing…", 0.96)
	key, _, err := sign.LoadOrGenerate(s.configDir, "306gapps")
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	tmp := result.Path + ".signed"
	if err := sign.Zip(result.Path, tmp, key); err != nil {
		os.Remove(tmp)
		return nil, fmt.Errorf("sign: %w", err)
	}
	if err := os.Rename(tmp, result.Path); err != nil {
		return nil, err
	}
	if err := sign.Verify(result.Path); err != nil {
		return nil, fmt.Errorf("the signature did not verify: %w", err)
	}

	size, sum, err := digest(result.Path)
	if err != nil {
		return nil, err
	}
	report("Done", 1)
	return &resultJSON{Path: result.Path, Name: name, Size: size, Files: result.Files, SHA256: sum}, nil
}

// resolve must be called with mu held.
func (s *Session) resolve() {
	s.res, s.resErr = s.cat.Resolve(s.selection())
}

func (s *Session) selection() []string {
	out := []string{}
	for id, on := range s.selected {
		if on {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Session) conflictsWithAny(id string, taken map[string]bool) bool {
	for _, other := range s.cat.ConflictsWith(id) {
		if taken[other] {
			return true
		}
	}
	return false
}

// state must be called with mu held.
func (s *Session) state() string {
	st := stateJSON{Selected: []string{}, Implied: map[string][]string{}}
	switch {
	case s.resErr != nil:
		st.Error = s.resErr.Error()
		// Still show the ticks, or a conflict would blank every checkbox.
		st.Selected = s.selection()
	case s.res != nil:
		for _, p := range s.res.Packages {
			st.Selected = append(st.Selected, p.ID)
		}
		for id, by := range s.res.Implied {
			st.Implied[id] = by
		}
		st.Count = len(s.res.Packages)
		st.Size = s.res.Size
		st.Variant = s.matchingVariant()
	}
	b, _ := json.Marshal(st)
	return string(b)
}

// matchingVariant compares resolutions, not ticks: a preset that omits a
// dependency still installs it.
func (s *Session) matchingVariant() string {
	have := map[string]bool{}
	for _, p := range s.res.Packages {
		have[p.ID] = true
	}
	for _, v := range s.cat.Variants() {
		r, err := s.cat.Resolve(s.cat.Prune(v.Packages))
		if err != nil || len(r.Packages) != len(have) {
			continue
		}
		same := true
		for _, p := range r.Packages {
			if !have[p.ID] {
				same = false
				break
			}
		}
		if same {
			return v.ID
		}
	}
	return ""
}

func digest(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
