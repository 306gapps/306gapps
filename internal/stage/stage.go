// Package stage fetches every payload and produces the install plan the builders consume.
package stage

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/306gapps/306gapps/internal/catalog"
	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/source"
)

// Entry is one file to install, paired with its verified local payload.
type Entry struct {
	Path    string
	Local   string
	Mode    os.FileMode
	Context string
	Kind    manifest.Kind
	Size    int64
	// SHA256 lets the installer re-verify the payload on the device.
	SHA256 string
	// Target is the link destination for symlink entries, which have no Local.
	Target string
	// Package is the display name of the package this file belongs to.
	Package string
}

// IsSymlink reports whether this entry is a link rather than a payload.
func (e Entry) IsSymlink() bool { return e.Kind == manifest.KindSymlink }

// IsEmpty reports a zero-length file, which has no payload to fetch.
func (e Entry) IsEmpty() bool { return !e.IsSymlink() && e.Size == 0 }

// Plan is everything a builder needs to emit a package.
type Plan struct {
	Release  manifest.Release
	Packages []manifest.Package
	Entries  []Entry
	// Removes are install paths of AOSP apps to delete before installing.
	Removes []string
	Props   map[string]string
	Size    int64
}

// Partitions returns the distinct partitions the plan writes to.
func (p *Plan) Partitions() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range p.Entries {
		part := manifest.File{Path: e.Path}.Partition()
		if !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}

// PackageIDs returns the install set in dependency order.
func (p *Plan) PackageIDs() []string {
	out := make([]string, len(p.Packages))
	for i, pkg := range p.Packages {
		out[i] = pkg.ID
	}
	return out
}

// Options tune how a plan is built.
type Options struct {
	// Workers bounds concurrent downloads. Zero means 4.
	Workers  int
	Progress source.Progress
	// KeepStock names packages whose removals are skipped, leaving the ROM's own app in place.
	KeepStock []string
}

// Build fetches every payload in the resolution and returns the install plan.
func Build(ctx context.Context, src *source.Source, m *manifest.Manifest, res *catalog.Resolution, opt Options) (*Plan, error) {
	// Serialised here rather than in every caller: the download workers all
	// call it, and a caller keeping a map of what it has seen would otherwise
	// hit "concurrent map writes", which is a hard crash rather than an error.
	if opt.Progress != nil {
		inner, mu := opt.Progress, new(sync.Mutex)
		opt.Progress = func(f manifest.File, got, want int64) {
			mu.Lock()
			defer mu.Unlock()
			inner(f, got, want)
		}
	}

	workers := opt.Workers
	if workers <= 0 {
		workers = 4
	}
	if workers > len(res.Files) {
		workers = len(res.Files)
	}

	props, err := mergeProps(res.Packages)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, len(res.Files))
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	jobs := make(chan int)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				f := res.Files[i]
				if f.IsSymlink() || f.IsEmpty() {
					entries[i] = Entry{
						Path: f.Path, Mode: f.FileMode(), Context: f.Context,
						Kind: f.Kind, Target: f.Target, Size: f.Size,
						Package: res.Owner[f.Path],
					}
					continue
				}
				local, err := src.Fetch(ctx, m, f, opt.Progress)
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					cancel()
					continue
				}
				entries[i] = Entry{
					Path: f.Path, Local: local, Mode: f.FileMode(),
					Context: f.Context, Kind: f.Kind, Size: f.Size,
					SHA256: f.SHA256, Package: res.Owner[f.Path],
				}
			}
		}()
	}
	for i := range res.Files {
		select {
		case jobs <- i:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()

	if len(errs) > 0 {
		return nil, fmt.Errorf("staging failed: %w (%d more)", errs[0], len(errs)-1)
	}

	plan := &Plan{
		Release:  m.Release,
		Packages: res.Packages,
		Entries:  entries,
		Removes:  mergeRemoves(res.Packages, opt.KeepStock),
		Props:    props,
		Size:     res.Size,
	}
	return plan, nil
}

func mergeProps(pkgs []manifest.Package) (map[string]string, error) {
	out := map[string]string{}
	owner := map[string]string{}
	for _, p := range pkgs {
		for k, v := range p.Props {
			if prev, ok := out[k]; ok && prev != v {
				return nil, fmt.Errorf("property %q set to %q by %s and %q by %s",
					k, prev, owner[k], v, p.ID)
			}
			out[k] = v
			owner[k] = p.ID
		}
	}
	return out, nil
}

func mergeRemoves(pkgs []manifest.Package, keepStock []string) []string {
	keep := map[string]bool{}
	for _, id := range keepStock {
		keep[id] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range pkgs {
		if keep[p.ID] {
			continue
		}
		for _, r := range p.Removes {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	sort.Strings(out)
	return out
}
