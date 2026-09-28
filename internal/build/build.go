// Package build turns a staged plan into a flashable package.
package build

import (
	"archive/zip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/306gapps/306gapps/internal/stage"
)

//go:embed templates
var templates embed.FS

// Target selects the package format to emit.
type Target string

const (
	// TargetRecovery is a recovery-flashable zip. No root required.
	TargetRecovery Target = "recovery"
	// TargetModule is a Magisk/KernelSU systemless module. Root required.
	TargetModule Target = "module"
	// TargetOTA is a sideloadable A/B update package.
	TargetOTA Target = "ota"
)

var Targets = []Target{TargetRecovery, TargetModule, TargetOTA}

func ParseTarget(s string) (Target, error) {
	for _, t := range Targets {
		if string(t) == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("unknown target %q (want one of %s)", s, strings.Join(targetNames(), ", "))
}

func targetNames() []string {
	out := make([]string, len(Targets))
	for i, t := range Targets {
		out[i] = string(t)
	}
	return out
}

// Description explains what a target produces and what it requires.
func (t Target) Description() string {
	switch t {
	case TargetRecovery:
		return "Recovery-flashable zip (TWRP/LineageOS recovery, no root needed)"
	case TargetModule:
		return "Magisk / KernelSU module (systemless, root required)"
	case TargetOTA:
		return "Sideloadable A/B package, signed with your own ROM keys"
	}
	return string(t)
}

// Options control a single build.
type Options struct {
	Target Target
	// Out is the output zip path.
	Out string
	// Busybox is an optional static binary bundled with the recovery installer so
	// it runs against one known toolset instead of whatever the recovery provides.
	Busybox string
	// Signing configures the OTA target; ignored by the others.
	Signing SigningOptions
	// Progress is called after each file is written.
	Progress func(done, total int)
}

// Result describes the emitted package.
type Result struct {
	Path   string
	Target Target
	Size   int64
	Files  int
	SHA256 string
}

// Build emits the package described by plan.
func Build(plan *stage.Plan, opt Options) (*Result, error) {
	if len(plan.Entries) == 0 {
		return nil, fmt.Errorf("nothing to build: no files selected")
	}
	if opt.Out == "" {
		return nil, fmt.Errorf("no output path given")
	}
	// The OTA target shells out to the AOSP tools and manages its own output.
	if opt.Target == TargetOTA {
		return buildOTA(plan, opt)
	}

	tmp := opt.Out + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	defer func() {
		f.Close()
		os.Remove(tmp)
	}()

	h := sha256.New()
	zw := newWriter(io.MultiWriter(f, h))

	switch opt.Target {
	case TargetRecovery:
		err = buildRecovery(plan, zw, opt)
	case TargetModule:
		err = buildModule(plan, zw, opt)
	default:
		err = fmt.Errorf("unknown target %q", opt.Target)
	}
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, opt.Out); err != nil {
		return nil, err
	}

	return &Result{
		Path:   opt.Out,
		Target: opt.Target,
		Size:   st.Size(),
		Files:  zw.count,
		SHA256: hex.EncodeToString(h.Sum(nil)),
	}, nil
}

// Fixed timestamp on every entry, so identical inputs produce identical zips.
var epoch = time.Date(2009, time.January, 1, 0, 0, 0, 0, time.UTC)

// writer wraps archive/zip with deterministic headers and per-extension compression.
type writer struct {
	zw    *zip.Writer
	count int
	seen  map[string]bool
}

func newWriter(w io.Writer) *writer {
	return &writer{zw: zip.NewWriter(w), seen: map[string]bool{}}
}

// Formats that do not compress further. Apks and apexes are deliberately absent:
// Google leaves many entries in them uncompressed for mapping, and deflating the
// container recovers 1.55x over storing it.
var storeExts = map[string]bool{
	".gz": true, ".xz": true, ".zst": true, ".br": true,
}

func (w *writer) header(name string, mode os.FileMode) *zip.FileHeader {
	h := &zip.FileHeader{Name: name, Modified: epoch}
	h.SetMode(mode)
	if storeExts[strings.ToLower(path.Ext(name))] {
		h.Method = zip.Store
	} else {
		h.Method = zip.Deflate
	}
	return h
}

func (w *writer) addFile(name, local string, mode os.FileMode) error {
	if w.seen[name] {
		return fmt.Errorf("duplicate archive entry %q", name)
	}
	src, err := os.Open(local)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := w.zw.CreateHeader(w.header(name, mode))
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	w.seen[name] = true
	w.count++
	return nil
}

func (w *writer) addBytes(name string, mode os.FileMode, content []byte) error {
	if w.seen[name] {
		return fmt.Errorf("duplicate archive entry %q", name)
	}
	dst, err := w.zw.CreateHeader(w.header(name, mode))
	if err != nil {
		return err
	}
	if _, err := dst.Write(content); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	w.seen[name] = true
	w.count++
	return nil
}

// addSymlink records a link: the target goes in the entry body with the symlink mode bit set.
func (w *writer) addSymlink(name, target string) error {
	if w.seen[name] {
		return fmt.Errorf("duplicate archive entry %q", name)
	}
	hdr := &zip.FileHeader{Name: name, Method: zip.Store, Modified: epoch}
	hdr.SetMode(os.ModeSymlink | 0o777)
	dst, err := w.zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(dst, target); err != nil {
		return fmt.Errorf("write symlink %s: %w", name, err)
	}
	w.seen[name] = true
	w.count++
	return nil
}

// addTemplate copies an embedded installer script into the archive.
func (w *writer) addTemplate(name, tmpl string, mode os.FileMode) error {
	b, err := templates.ReadFile(path.Join("templates", tmpl))
	if err != nil {
		return fmt.Errorf("read template %s: %w", tmpl, err)
	}
	return w.addBytes(name, mode, b)
}

func (w *writer) Close() error { return w.zw.Close() }

// hashFile digests an already-open file from the start.
func hashFile(f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// releaseInfo is the key=value block both installers read for display and the API guard.
func releaseInfo(plan *stage.Plan) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "name=306gapps\n")
	fmt.Fprintf(&b, "release=%s\n", plan.Release.ID)
	fmt.Fprintf(&b, "android=%s\n", plan.Release.Android.Version)
	fmt.Fprintf(&b, "api=%d\n", plan.Release.Android.API)
	fmt.Fprintf(&b, "device=%s\n", plan.Release.Source.Device)
	fmt.Fprintf(&b, "build=%s\n", plan.Release.Source.Build)
	fmt.Fprintf(&b, "packages=%d\n", len(plan.Packages))
	fmt.Fprintf(&b, "size=%d\n", plan.Size)
	fmt.Fprintf(&b, "verbose=0\n")
	fmt.Fprintf(&b, "selection=%s\n", strings.Join(plan.PackageIDs(), ","))
	return []byte(b.String())
}

// propsFile renders build properties in a stable order.
func propsFile(plan *stage.Plan) []byte {
	keys := make([]string, 0, len(plan.Props))
	for k := range plan.Props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, plan.Props[k])
	}
	return []byte(b.String())
}

// packagesFile lists the selection's Android package ids so an uninstall can clear app data.
func packagesFile(plan *stage.Plan) []byte {
	seen := map[string]bool{}
	var out []string
	for _, p := range plan.Packages {
		for _, id := range p.Packages {
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return linesFile(out)
}

func linesFile(lines []string) []byte {
	if len(lines) == 0 {
		return nil
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// sortedEntries returns plan entries in a stable path order.
func sortedEntries(plan *stage.Plan) []stage.Entry {
	out := append([]stage.Entry(nil), plan.Entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
