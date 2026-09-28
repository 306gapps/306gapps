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

	target  build.Target
	busybox string

	mu       sync.Mutex
	progress string
}

func newState(ctx context.Context, src *source.Source, outDir string) *state {
	return &state{
		ctx: ctx, src: src, outDir: outDir,
		selected: map[string]bool{},
		target:   build.TargetRecovery,
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
