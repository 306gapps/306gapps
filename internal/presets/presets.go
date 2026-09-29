// Package presets carries the preset selections.
//
// They used to live in each release manifest, which meant correcting one
// needed a re-dump of every release. They ship with the app instead, and a
// copy on the assets repo supersedes them when it is reachable, so a bad
// preset can be fixed for everyone without either a dump or an app release.
package presets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	_ "embed"

	"gopkg.in/yaml.v3"

	"github.com/306gapps/306gapps/internal/manifest"
)

// Schema is the presets version this build understands.
const Schema = 1

// Remote is the copy that supersedes the built-in one, relative to the source root.
const Remote = "presets.json"

//go:embed presets.yaml
var builtin []byte

// Definition is one preset before its tier chain is flattened.
type Definition struct {
	ID       string   `json:"id" yaml:"id"`
	Name     string   `json:"name" yaml:"name"`
	Summary  string   `json:"summary,omitempty" yaml:"summary"`
	Includes string   `json:"includes,omitempty" yaml:"includes"`
	All      bool     `json:"all,omitempty" yaml:"all"`
	Packages []string `json:"packages,omitempty" yaml:"packages"`
}

type file struct {
	Schema       int               `json:"schema" yaml:"schema"`
	Variants     []Definition      `json:"variants" yaml:"variants"`
	Experimental map[string]string `json:"experimental,omitempty" yaml:"experimental"`
}

// Origin says where the presets in use came from, for the status line.
type Origin string

const (
	OriginBuiltin Origin = "built in"
	OriginRemote  Origin = "assets repo"
)

// Builtin returns the presets compiled into this binary.
func Builtin() []Definition {
	return builtinFile().Variants
}

func builtinFile() file {
	var f file
	if err := yaml.Unmarshal(builtin, &f); err != nil {
		panic("presets.yaml does not parse: " + err.Error())
	}
	return f
}

// Opener fetches a path relative to the source root.
type Opener interface {
	Open(ctx context.Context, ref string) (io.ReadCloser, int64, error)
}

// Load prefers the copy on the assets repo and falls back to the built-in one.
//
// Anything wrong with the remote copy -- unreachable, malformed, a schema this
// build predates, or empty -- leaves the built-in presets in place rather than
// leaving the picker with none.
func Load(ctx context.Context, src Opener) ([]Definition, map[string]string, Origin) {
	b := builtinFile()
	if src == nil {
		return b.Variants, b.Experimental, OriginBuiltin
	}
	rc, _, err := src.Open(ctx, Remote)
	if err != nil {
		return b.Variants, b.Experimental, OriginBuiltin
	}
	defer rc.Close()

	var f file
	if err := json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&f); err != nil {
		return b.Variants, b.Experimental, OriginBuiltin
	}
	if f.Schema != Schema || len(f.Variants) == 0 {
		return b.Variants, b.Experimental, OriginBuiltin
	}
	return f.Variants, f.Experimental, OriginRemote
}

// Resolve flattens the tier chains and drops ids the release does not ship.
//
// order is the manifest's package order, so a preset reads the way the picker
// is laid out.
func Resolve(defs []Definition, order []string) ([]manifest.Variant, error) {
	rank := make(map[string]int, len(order))
	for i, id := range order {
		rank[id] = i
	}
	by := make(map[string]Definition, len(defs))
	for _, d := range defs {
		by[d.ID] = d
	}

	var members func(string, map[string]bool) ([]string, error)
	members = func(id string, seen map[string]bool) ([]string, error) {
		if seen[id] {
			return nil, fmt.Errorf("preset %s includes itself", id)
		}
		d, ok := by[id]
		if !ok {
			return nil, fmt.Errorf("preset %s does not exist", id)
		}
		if d.All {
			return append([]string(nil), order...), nil
		}
		var out []string
		if d.Includes != "" {
			seen[id] = true
			base, err := members(d.Includes, seen)
			delete(seen, id)
			if err != nil {
				return nil, err
			}
			out = append(out, base...)
		}
		return append(out, d.Packages...), nil
	}

	out := make([]manifest.Variant, 0, len(defs))
	for _, d := range defs {
		ids, err := members(d.ID, map[string]bool{})
		if err != nil {
			return nil, err
		}
		keep := make([]string, 0, len(ids))
		seen := map[string]bool{}
		for _, id := range ids {
			if _, ok := rank[id]; !ok || seen[id] {
				continue
			}
			seen[id] = true
			keep = append(keep, id)
		}
		sort.Slice(keep, func(i, j int) bool { return rank[keep[i]] < rank[keep[j]] })
		out = append(out, manifest.Variant{
			ID: d.ID, Name: d.Name, Summary: d.Summary, Packages: keep,
		})
	}
	return out, nil
}

// Target is the part of a catalog these need.
type Target interface {
	Order() []string
	SetVariants([]manifest.Variant)
	SetExperimental(map[string]string)
}

// Apply loads the presets and puts them on the catalog.
//
// A preset that will not resolve leaves the catalog's own alone rather than
// emptying the picker.
func Apply(ctx context.Context, src Opener, c Target) Origin {
	defs, exp, origin := Load(ctx, src)
	vs, err := Resolve(defs, c.Order())
	if err != nil {
		if origin == OriginBuiltin {
			return origin
		}
		// A bad remote copy should not cost the user the built-in presets.
		b := builtinFile()
		if vs, err = Resolve(b.Variants, c.Order()); err != nil {
			return OriginBuiltin
		}
		exp = b.Experimental
		origin = OriginBuiltin
	}
	c.SetVariants(vs)
	c.SetExperimental(exp)
	return origin
}
