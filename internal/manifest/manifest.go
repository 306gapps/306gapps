// Package manifest defines the release manifest the assets repo publishes and the builder reads.
package manifest

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Schema is the manifest version this build understands.
const Schema = 1

// Partitions a dumped file may live on, in mount-order preference.
var Partitions = []string{"system", "system_ext", "product", "vendor"}

type Manifest struct {
	Schema   int       `json:"schema"`
	Release  Release   `json:"release"`
	Packages []Package `json:"packages"`
}

type Release struct {
	ID      string    `json:"id"`
	Android Android   `json:"android"`
	Source  Source    `json:"source"`
	Created time.Time `json:"created"`
	// AssetBase is the URL prefix every File.Asset is resolved against.
	AssetBase string `json:"asset_base"`
}

type Android struct {
	API      int    `json:"api"`
	Version  string `json:"version"`
	Codename string `json:"codename"`
}

// Source records the factory image a release was dumped from.
type Source struct {
	Device string `json:"device"`
	Build  string `json:"build"`
	Image  string `json:"image"`
	SHA256 string `json:"sha256"`
}

type Package struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Summary  string `json:"summary,omitempty"`

	// Required packages are always installed and cannot be deselected.
	Required bool `json:"required,omitempty"`
	// Default packages start selected.
	Default bool `json:"default,omitempty"`

	Requires  []string `json:"requires,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	// Removes lists AOSP packages the installer deletes, by install path.
	Removes []string `json:"removes,omitempty"`

	Files []File `json:"files"`
	// Props are appended to the target's build properties.
	Props map[string]string `json:"props,omitempty"`
}

// Kind classifies a file for the installer and the addon.d survival script.
type Kind string

const (
	KindAPK        Kind = "apk"
	KindPermission Kind = "permission"
	KindSysconfig  Kind = "sysconfig"
	KindOverlay    Kind = "overlay"
	KindLib        Kind = "lib"
	KindFramework  Kind = "framework"
	KindEtc        Kind = "etc"
)

type File struct {
	// Path is partition-relative, e.g. "product/priv-app/Foo/Foo.apk".
	Path string `json:"path"`
	// Asset is the payload name within the release's asset bundle.
	Asset   string `json:"asset"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	Context string `json:"context,omitempty"`
	Kind    Kind   `json:"kind"`
}

// Partition returns the partition a file installs to.
func (f File) Partition() string {
	p, _, _ := strings.Cut(f.Path, "/")
	return p
}

// FileMode parses the octal Mode, falling back to a sane default per kind.
func (f File) FileMode() os.FileMode {
	if f.Mode != "" {
		if n, err := strconv.ParseUint(f.Mode, 8, 32); err == nil {
			return os.FileMode(n)
		}
	}
	return 0o644
}

// Size is the total installed size of a package.
func (p Package) Size() int64 {
	var n int64
	for _, f := range p.Files {
		n += f.Size
	}
	return n
}

func Load(r io.Reader) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func LoadFile(name string) (*Manifest, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Load(f)
}

// Index returns packages keyed by ID.
func (m *Manifest) Index() map[string]Package {
	idx := make(map[string]Package, len(m.Packages))
	for _, p := range m.Packages {
		idx[p.ID] = p
	}
	return idx
}

// Categories returns the distinct categories in manifest order.
func (m *Manifest) Categories() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range m.Packages {
		if !seen[p.Category] {
			seen[p.Category] = true
			out = append(out, p.Category)
		}
	}
	return out
}

// Validate checks internal consistency: unique IDs, resolvable references, sane paths and digests.
func (m *Manifest) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if m.Schema != Schema {
		add("schema %d unsupported (want %d)", m.Schema, Schema)
	}
	if m.Release.ID == "" {
		add("release.id is empty")
	}
	if m.Release.Android.API <= 0 {
		add("release.android.api must be positive, got %d", m.Release.Android.API)
	}

	seen := map[string]bool{}
	paths := map[string]string{}
	for i, p := range m.Packages {
		where := p.ID
		if where == "" {
			where = fmt.Sprintf("packages[%d]", i)
			add("%s: id is empty", where)
		}
		if seen[p.ID] {
			add("%s: duplicate package id", where)
		}
		seen[p.ID] = true
		if p.Name == "" {
			add("%s: name is empty", where)
		}
		if len(p.Files) == 0 {
			add("%s: has no files", where)
		}
		for _, f := range p.Files {
			switch {
			case f.Path == "":
				add("%s: file with empty path", where)
			case path.IsAbs(f.Path):
				add("%s: path %q must be partition-relative", where, f.Path)
			case !validPartition(f.Partition()):
				add("%s: path %q has unknown partition %q", where, f.Path, f.Partition())
			case path.Clean(f.Path) != f.Path:
				add("%s: path %q is not clean", where, f.Path)
			}
			if prev, dup := paths[f.Path]; dup && prev != p.ID {
				add("%s: path %q also provided by %s", where, f.Path, prev)
			}
			paths[f.Path] = p.ID
			if len(f.SHA256) != 64 {
				add("%s: file %q has malformed sha256", where, f.Path)
			}
			if f.Size < 0 {
				add("%s: file %q has negative size", where, f.Path)
			}
			if f.Asset == "" {
				add("%s: file %q has no asset", where, f.Path)
			}
		}
	}

	for _, p := range m.Packages {
		for _, dep := range p.Requires {
			if !seen[dep] {
				add("%s: requires unknown package %q", p.ID, dep)
			}
			if dep == p.ID {
				add("%s: requires itself", p.ID)
			}
		}
		for _, c := range p.Conflicts {
			if !seen[c] {
				add("%s: conflicts with unknown package %q", p.ID, c)
			}
			if c == p.ID {
				add("%s: conflicts with itself", p.ID)
			}
		}
		if p.Required {
			for _, c := range p.Conflicts {
				if idx := m.Index(); idx[c].Required {
					add("%s and %s are both required but conflict", p.ID, c)
				}
			}
		}
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("invalid manifest:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func validPartition(p string) bool {
	for _, known := range Partitions {
		if p == known {
			return true
		}
	}
	return false
}
