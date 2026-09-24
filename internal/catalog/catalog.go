// Package catalog turns a package selection into a resolved, ordered, conflict-free install set.
package catalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/306gapps/306gapps/internal/manifest"
)

type Catalog struct {
	m   *manifest.Manifest
	idx map[string]manifest.Package
}

func New(m *manifest.Manifest) *Catalog {
	return &Catalog{m: m, idx: m.Index()}
}

func (c *Catalog) Manifest() *manifest.Manifest { return c.m }

func (c *Catalog) Package(id string) (manifest.Package, bool) {
	p, ok := c.idx[id]
	return p, ok
}

// Defaults is the selection a fresh session starts with.
func (c *Catalog) Defaults() []string {
	var out []string
	for _, p := range c.m.Packages {
		if p.Required || p.Default {
			out = append(out, p.ID)
		}
	}
	return out
}

// ByCategory groups packages for display, preserving manifest order.
func (c *Catalog) ByCategory() map[string][]manifest.Package {
	g := map[string][]manifest.Package{}
	for _, p := range c.m.Packages {
		g[p.Category] = append(g[p.Category], p)
	}
	return g
}

// Resolution is the outcome of resolving a selection.
type Resolution struct {
	// Packages is the full install set in dependency order.
	Packages []manifest.Package
	// Implied maps an auto-added package to the packages that pulled it in.
	Implied map[string][]string
	// Files is every file to install, deduplicated and path-sorted.
	Files []manifest.File
	// Size is the total installed size in bytes.
	Size int64
}

// Selected reports whether id is in the resolved set.
func (r *Resolution) Selected(id string) bool {
	for _, p := range r.Packages {
		if p.ID == id {
			return true
		}
	}
	return false
}

// ConflictError reports mutually exclusive packages in a selection.
type ConflictError struct{ Pairs [][2]string }

func (e *ConflictError) Error() string {
	var b strings.Builder
	b.WriteString("conflicting packages selected:")
	for _, p := range e.Pairs {
		fmt.Fprintf(&b, "\n  - %s cannot be installed alongside %s", p[0], p[1])
	}
	return b.String()
}

// UnknownError reports selections that are not in the manifest.
type UnknownError struct{ IDs []string }

func (e *UnknownError) Error() string {
	return "unknown package(s): " + strings.Join(e.IDs, ", ")
}

// Resolve expands sel with its dependencies, checks conflicts, and returns a stable install order.
func (c *Catalog) Resolve(sel []string) (*Resolution, error) {
	want := map[string]bool{}
	var unknown []string
	for _, id := range sel {
		if _, ok := c.idx[id]; !ok {
			unknown = append(unknown, id)
			continue
		}
		want[id] = true
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, &UnknownError{IDs: unknown}
	}
	for _, p := range c.m.Packages {
		if p.Required {
			want[p.ID] = true
		}
	}

	implied := map[string][]string{}
	queue := keysOf(want)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, dep := range c.idx[id].Requires {
			if !want[dep] {
				want[dep] = true
				queue = append(queue, dep)
			}
			if !contains(implied[dep], id) && !inSlice(sel, dep) {
				implied[dep] = append(implied[dep], id)
			}
		}
	}

	var pairs [][2]string
	for _, id := range sortedKeys(want) {
		for _, other := range c.idx[id].Conflicts {
			if want[other] && id < other {
				pairs = append(pairs, [2]string{id, other})
			}
		}
	}
	if len(pairs) > 0 {
		return nil, &ConflictError{Pairs: pairs}
	}

	ordered, err := c.topo(want)
	if err != nil {
		return nil, err
	}

	res := &Resolution{Packages: ordered, Implied: implied}
	seen := map[string]bool{}
	for _, p := range ordered {
		for _, f := range p.Files {
			if seen[f.Path] {
				continue
			}
			seen[f.Path] = true
			res.Files = append(res.Files, f)
			res.Size += f.Size
		}
	}
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	return res, nil
}

// topo orders dependencies before dependents, breaking ties on ID for reproducible builds.
func (c *Catalog) topo(want map[string]bool) ([]manifest.Package, error) {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	state := map[string]int{}
	var out []manifest.Package
	var stack []string

	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case black:
			return nil
		case grey:
			cycle := append(append([]string{}, stack...), id)
			return fmt.Errorf("dependency cycle: %s", strings.Join(cycle, " -> "))
		}
		state[id] = grey
		stack = append(stack, id)
		deps := append([]string{}, c.idx[id].Requires...)
		sort.Strings(deps)
		for _, dep := range deps {
			if want[dep] {
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = black
		out = append(out, c.idx[id])
		return nil
	}

	for _, id := range sortedKeys(want) {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Dependents returns packages in the manifest that require id.
func (c *Catalog) Dependents(id string) []string {
	var out []string
	for _, p := range c.m.Packages {
		if contains(p.Requires, id) {
			out = append(out, p.ID)
		}
	}
	sort.Strings(out)
	return out
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := keysOf(m)
	sort.Strings(out)
	return out
}

func contains(s []string, v string) bool { return inSlice(s, v) }

func inSlice(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
