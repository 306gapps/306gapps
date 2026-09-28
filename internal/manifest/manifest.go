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
	Groups   []Group   `json:"groups"`
	Variants []Variant `json:"variants,omitempty"`
	Packages []Package `json:"packages"`
}

// Variant is a preset selection; picking one replaces the current selection.
type Variant struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Summary  string   `json:"summary,omitempty"`
	Packages []string `json:"packages"`
}

// Group is a family of related packages, such as Chrome with its WebView and Trichrome library.
type Group struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
}

type Release struct {
	ID      string    `json:"id"`
	Android Android   `json:"android"`
	Source  Source    `json:"source"`
	Created time.Time `json:"created"`
	// AssetBase is the URL prefix every File.Asset is resolved against.
	AssetBase string `json:"asset_base"`
	// Definitions is the digest of the package definitions this release was built from.
	Definitions string `json:"definitions,omitempty"`
	// Arch is the device architecture the payloads target; empty means arm64.
	Arch string `json:"arch,omitempty"`
}

// Architecture returns the release's arch, defaulting to arm64.
func (r Release) Architecture() string {
	if r.Arch == "" {
		return "arm64"
	}
	return r.Arch
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

// EncodingGzip is the only transport encoding we publish: no third-party package needed to read it.
const EncodingGzip = "gzip"

type Package struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Group   string `json:"group"`
	Summary string `json:"summary,omitempty"`

	// Required packages are always installed and cannot be deselected.
	Required bool `json:"required,omitempty"`
	// Default packages start selected.
	Default bool `json:"default,omitempty"`

	Requires  []string `json:"requires,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	// Removes lists what the installer deletes for this package: an entry with a
	// slash is an exact path, a bare name matches every app location on every partition.
	Removes []string `json:"removes,omitempty"`
	// Packages are the Android package ids this installs, used to clear app data on uninstall.
	Packages []string `json:"packages,omitempty"`

	Files []File `json:"files"`
	// Props are appended to the target's build properties.
	Props map[string]string `json:"props,omitempty"`
	// Version records the principal apk's version.
	Version map[string]string `json:"version,omitempty"`
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
	// KindSymlink is a link rather than a payload: it carries a Target and no asset.
	KindSymlink Kind = "symlink"
)

type File struct {
	// Path is partition-relative, e.g. "product/priv-app/Foo/Foo.apk".
	Path string `json:"path"`
	// Asset is the payload name within the release's asset bundle, empty for symlinks.
	Asset   string `json:"asset,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	Context string `json:"context,omitempty"`
	Kind    Kind   `json:"kind"`
	// Target is the link destination, set only when Kind is KindSymlink.
	Target string `json:"target,omitempty"`
	// Stub marks a placeholder apk Google ships beside the real one. Installing one
	// without its counterpart leaves an app entry that cannot start.
	Stub bool `json:"stub,omitempty"`
	// Kanged marks a payload taken from elsewhere rather than this release's dump, so a new build never refreshes it.
	Kanged bool `json:"kanged,omitempty"`
	// Encoding names how the asset is compressed for transport, empty meaning it is
	// not. Unrelated to a path ending in .gz: some apks install compressed.
	Encoding string `json:"encoding,omitempty"`
	// AssetSHA256 and AssetSize describe the artifact as downloaded; SHA256 and Size
	// always describe the file that lands on the device.
	AssetSHA256 string `json:"asset_sha256,omitempty"`
	AssetSize   int64  `json:"asset_size,omitempty"`
	// Synthetic marks a payload this project generates rather than extracts.
	Synthetic bool `json:"synthetic,omitempty"`
}

// Compressed reports whether the asset must be decoded after downloading.
func (f File) Compressed() bool { return f.Encoding != "" }

// Download returns the number of bytes actually fetched for this file.
func (f File) Download() int64 {
	if f.Compressed() {
		return f.AssetSize
	}
	return f.Size
}

// IsSymlink reports whether this entry is a link rather than a payload.
func (f File) IsSymlink() bool { return f.Kind == KindSymlink }

// IsEmpty reports a zero-length file, which carries no asset.
func (f File) IsEmpty() bool { return !f.IsSymlink() && f.Size == 0 }

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
		return nil, decodeError(err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// An unknown field almost always means a schema mismatch, not a corrupt download.
func decodeError(err error) error {
	const unknown = "json: unknown field "
	if i := strings.Index(err.Error(), unknown); i >= 0 {
		field := strings.Trim(err.Error()[i+len(unknown):], `"`)
		return fmt.Errorf(
			"this release was published for a different version of 306gapps "+
				"(it carries a %q field this build does not know).\n"+
				"Update 306gapps, or pick a release published since it was built.",
			field)
	}
	return fmt.Errorf("decode manifest: %w", err)
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

// GroupIndex returns groups keyed by ID.
func (m *Manifest) GroupIndex() map[string]Group {
	idx := make(map[string]Group, len(m.Groups))
	for _, g := range m.Groups {
		idx[g.ID] = g
	}
	return idx
}

// Members returns a group's packages in manifest order.
func (m *Manifest) Members(group string) []Package {
	var out []Package
	for _, p := range m.Packages {
		if p.Group == group {
			out = append(out, p)
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

	groups := map[string]bool{}
	for i, g := range m.Groups {
		if g.ID == "" {
			add("groups[%d]: id is empty", i)
			continue
		}
		if groups[g.ID] {
			add("%s: duplicate group id", g.ID)
		}
		groups[g.ID] = true
		if g.Name == "" {
			add("%s: group name is empty", g.ID)
		}
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
		for _, f := range p.Files {
			switch f.Encoding {
			case "":
				if f.AssetSHA256 != "" || f.AssetSize != 0 {
					add("%s: %s describes a compressed artifact but names no encoding",
						where, f.Path)
				}
			case EncodingGzip:
				if len(f.AssetSHA256) != 64 {
					add("%s: %s is %s but has no artifact digest", where, f.Path, f.Encoding)
				}
				if f.AssetSize <= 0 {
					add("%s: %s is %s but has no artifact size", where, f.Path, f.Encoding)
				}
			default:
				add("%s: %s has unknown encoding %q", where, f.Path, f.Encoding)
			}
		}
		if p.Group == "" {
			add("%s: group is empty", where)
		} else if !groups[p.Group] {
			add("%s: group %q is not declared", where, p.Group)
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
			if f.Size < 0 {
				add("%s: file %q has negative size", where, f.Path)
			}
			if f.IsSymlink() {
				switch {
				case f.Target == "":
					add("%s: symlink %q has no target", where, f.Path)
				case f.Asset != "" || f.SHA256 != "":
					add("%s: symlink %q must not carry a payload", where, f.Path)
				case path.IsAbs(f.Target) && !targetsKnownPartition(f.Target):
					// An absolute target outside our partitions is a dump error.
					add("%s: symlink %q points at %q, which is outside the "+
						"partitions this package installs to", where, f.Path, f.Target)
				}
			} else if f.IsEmpty() {
				if f.Asset != "" || f.SHA256 != "" {
					add("%s: empty file %q must not carry a payload", where, f.Path)
				}
			} else {
				if len(f.SHA256) != 64 {
					add("%s: file %q has malformed sha256", where, f.Path)
				}
				if f.Asset == "" {
					add("%s: file %q has no asset", where, f.Path)
				}
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
		for _, r := range p.Removes {
			switch {
			case r == "":
				add("%s: empty removal entry", p.ID)
			case path.IsAbs(r):
				add("%s: removal %q must be a bare name or partition-relative", p.ID, r)
			case strings.Contains(r, ".."):
				add("%s: removal %q escapes the partition", p.ID, r)
			case strings.Contains(r, "/") && !validPartition(strings.SplitN(r, "/", 2)[0]):
				add("%s: removal %q has unknown partition %q", p.ID, r,
					strings.SplitN(r, "/", 2)[0])
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

func targetsKnownPartition(target string) bool {
	clean := path.Clean(target)
	for _, part := range Partitions {
		if clean == "/"+part || strings.HasPrefix(clean, "/"+part+"/") {
			return true
		}
	}
	// system-as-root devices reach /system through /system/system.
	return strings.HasPrefix(clean, "/system/")
}

func validPartition(p string) bool {
	for _, known := range Partitions {
		if p == known {
			return true
		}
	}
	return false
}
