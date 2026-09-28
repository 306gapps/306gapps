//go:build gui

// Package gui is the desktop picker, a view over the same core the CLI uses.
package gui

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/306gapps/306gapps/internal/build"
	"github.com/306gapps/306gapps/internal/catalog"
	"github.com/306gapps/306gapps/internal/config"
	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/source"
)

// state holds what the window is showing; UI-goroutine only unless a mutex says otherwise.
type state struct {
	ctx    context.Context
	src    *source.Source
	outDir string

	index   *source.Index
	release source.ReleaseRef
	cat     *catalog.Catalog

	selected map[string]bool
	res      *catalog.Resolution
	resErr   error

	target build.Target

	// expert reveals the controls most people should not need.
	expert bool
	// keepStock names packages whose removals are skipped.
	keepStock map[string]bool
	// configs holds the user's own saved selections.
	configs *config.Store
	// keptLabel is the saved selection currently showing, if any.
	keptLabel string

	// The ota target needs the ROM and its signing keys; neither can be inferred.
	otaBase  string
	otaKeys  string
	otaTools string
	otaGrow  bool
	busybox  string

	mu       sync.Mutex
	progress string
}

func newState(ctx context.Context, src *source.Source, outDir string) *state {
	return &state{
		ctx: ctx, src: src, outDir: outDir,
		keepStock: map[string]bool{},
		configs:   config.New(),
		selected:  map[string]bool{},
		target:    build.TargetRecovery,
	}
}

// loadRelease fetches a manifest and resets the selection to its defaults.
func (s *state) loadRelease(ref source.ReleaseRef) error {
	m, err := s.src.Manifest(s.ctx, ref)
	if err != nil {
		return err
	}
	s.release = ref
	s.cat = catalog.New(m)
	s.selected = map[string]bool{}
	for _, id := range s.cat.Defaults() {
		s.selected[id] = true
	}
	s.resolve()
	return nil
}

// resolve re-runs dependency and conflict resolution after every toggle.
func (s *state) resolve() {
	if s.cat == nil {
		return
	}
	s.res, s.resErr = s.cat.Resolve(s.selection())
}

func (s *state) selection() []string {
	out := make([]string, 0, len(s.selected))
	for id, on := range s.selected {
		if on {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// implied reports packages pulled in by something else rather than chosen.
func (s *state) implied() map[string]bool {
	out := map[string]bool{}
	if s.res != nil {
		for id := range s.res.Implied {
			out[id] = true
		}
	}
	return out
}

// summary is the line under the list: what will be installed, or why it cannot.
func (s *state) summary() string {
	if s.cat == nil {
		return ""
	}
	if s.resErr != nil {
		return s.resErr.Error()
	}
	if s.res == nil {
		return ""
	}
	line := fmt.Sprintf("%d packages · %s installed",
		len(s.res.Packages), humanSize(s.res.Size))
	if n := len(s.res.Implied); n > 0 {
		line += fmt.Sprintf(" · %d pulled in as dependencies", n)
	}
	return line
}

func (s *state) variants() []manifest.Variant {
	if s.cat == nil {
		return nil
	}
	return s.cat.Variants()
}

// applyVariant replaces the selection with a preset.
func (s *state) applyVariant(id string) {
	v, ok := s.cat.Variant(id)
	if !ok {
		return
	}
	s.selected = map[string]bool{}
	for _, pid := range s.cat.Prune(v.Packages) {
		s.selected[pid] = true
	}
	s.resolve()
}

// matchingVariant names the preset the selection exactly matches, or "".
func (s *state) matchingVariant() string {
	if s.cat == nil || s.res == nil {
		return ""
	}
	// A saved selection keeps its own label even when it equals a preset.
	if s.keptLabel != "" {
		return ""
	}
	have := map[string]bool{}
	for _, p := range s.res.Packages {
		have[p.ID] = true
	}
	for _, v := range s.cat.Variants() {
		want := map[string]bool{}
		for _, id := range s.cat.Prune(v.Packages) {
			want[id] = true
		}
		// Compare resolutions, not ticks: a preset that omits a dependency still installs it.
		if r, err := s.cat.Resolve(keys(want)); err == nil && sameSet(r, have) {
			return v.ID
		}
	}
	return ""
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameSet(r *catalog.Resolution, have map[string]bool) bool {
	if len(r.Packages) != len(have) {
		return false
	}
	for _, p := range r.Packages {
		if !have[p.ID] {
			return false
		}
	}
	return true
}

// keepStockIDs returns packages whose removals are skipped, limited to ones being installed.
func (s *state) keepStockIDs() []string {
	var out []string
	if s.res == nil {
		return nil
	}
	for _, p := range s.res.Packages {
		if s.keepStock[p.ID] {
			out = append(out, p.ID)
		}
	}
	sort.Strings(out)
	return out
}

// replaces reports whether a package removes anything, the only case where keep-stock means something.
func replaces(p manifest.Package) bool { return len(p.Removes) > 0 }

func (s *state) groups() []manifest.Group {
	if s.cat == nil {
		return nil
	}
	return s.cat.Groups()
}

func (s *state) packagesIn(group string) []manifest.Package {
	if s.cat == nil {
		return nil
	}
	return s.cat.ByGroup()[group]
}

func (s *state) setProgress(line string) {
	s.mu.Lock()
	s.progress = line
	s.mu.Unlock()
}

func (s *state) readProgress() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.progress
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
