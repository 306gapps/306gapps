package ota

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/306gapps/306gapps/internal/stage"
)

// Partition directories inside a target-files package are upper-cased.
func tfDir(partition string) string { return strings.ToUpper(partition) }

// InjectOptions control the target-files surgery.
type InjectOptions struct {
	// Base is the ROM's target-files zip.
	Base string
	// Out is the merged target-files zip to write.
	Out string
	// Grow raises a partition's size budget in misc_info.txt on overflow.
	Grow bool
	// Progress is called as files are written.
	Progress func(done, total int)
}

// InjectResult reports what the merge changed.
type InjectResult struct {
	Path       string
	Added      int
	Replaced   int
	Removed    int
	Partitions []string
	// Delta is the size change per partition, in bytes.
	Delta map[string]int64
	// Warnings are non-fatal problems the caller should surface.
	Warnings []string
}

// Inject writes a copy of the base target-files with the staged selection merged in.
// SELinux labels are left alone: every path we write already falls under the ROM's
// generic file_contexts rules.
func Inject(plan *stage.Plan, opt InjectOptions) (*InjectResult, error) {
	zr, err := zip.OpenReader(opt.Base)
	if err != nil {
		return nil, fmt.Errorf("open base target-files: %w", err)
	}
	defer zr.Close()

	if err := looksLikeTargetFiles(zr); err != nil {
		return nil, err
	}

	res := &InjectResult{Path: opt.Out, Delta: map[string]int64{}}

	// What we are adding, keyed by its path inside the target-files package.
	entries := append([]stage.Entry(nil), plan.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	incoming := make(map[string]stage.Entry, len(entries))
	parts := map[string]bool{}
	for _, e := range entries {
		part, rel, _ := strings.Cut(e.Path, "/")
		incoming[tfDir(part)+"/"+rel] = e
		parts[part] = true
	}
	for p := range parts {
		res.Partitions = append(res.Partitions, p)
	}
	sort.Strings(res.Partitions)

	// Paths the selection supersedes, as target-files prefixes.
	var removePrefixes []string
	for _, r := range plan.Removes {
		part, rel, ok := strings.Cut(r, "/")
		if !ok {
			continue
		}
		removePrefixes = append(removePrefixes, tfDir(part)+"/"+rel)
	}

	fsConfigs := map[string]*fsConfig{}
	var certs *apkCerts
	var miscInfo []byte

	out, err := os.Create(opt.Out)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	zw := zip.NewWriter(out)

	copied := 0
	for _, f := range zr.File {
		name := f.Name

		// Images are rebuilt from the trees we are about to change.
		if strings.HasPrefix(name, "IMAGES/") || isCareMap(name) {
			continue
		}
		// Metadata we rewrite at the end.
		if part, ok := fsConfigName(name); ok {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			cfg, err := parseFSConfig(rc, part)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", name, err)
			}
			fsConfigs[part] = cfg
			continue
		}
		if name == "META/apkcerts.txt" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			certs, err = parseAPKCerts(rc)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("parse apkcerts: %w", err)
			}
			continue
		}
		if name == "META/misc_info.txt" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			miscInfo, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			continue
		}

		if underAny(name, removePrefixes) {
			res.Removed++
			res.Delta[partitionOf(name)] -= int64(f.UncompressedSize64)
			continue
		}
		if _, replacing := incoming[name]; replacing {
			res.Delta[partitionOf(name)] -= int64(f.UncompressedSize64)
			res.Replaced++
			continue
		}

		// Everything else passes through without recompression.
		if err := zw.Copy(f); err != nil {
			return nil, fmt.Errorf("copy %s: %w", name, err)
		}
		copied++
	}

	if certs == nil {
		certs = &apkCerts{names: map[string]bool{}}
		res.Warnings = append(res.Warnings,
			"base target-files had no META/apkcerts.txt; created one")
	}

	// Drop metadata for the files we removed.
	for _, r := range plan.Removes {
		part, rel, ok := strings.Cut(r, "/")
		if !ok {
			continue
		}
		if cfg := fsConfigs[part]; cfg != nil {
			cfg.removeUnder(rel)
		}
		certs.remove(path.Base(rel) + ".apk")
	}

	// Write the payloads and record their ownership.
	for i, e := range entries {
		part, rel, _ := strings.Cut(e.Path, "/")
		name := tfDir(part) + "/" + rel

		src, err := os.Open(e.Local)
		if err != nil {
			return nil, err
		}
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: epoch}
		hdr.SetMode(e.Mode)
		if isStored(name) {
			hdr.Method = zip.Store
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			src.Close()
			return nil, err
		}
		if _, err := io.Copy(w, src); err != nil {
			src.Close()
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
		src.Close()

		cfg := fsConfigs[part]
		if cfg == nil {
			cfg = &fsConfig{part: part}
			fsConfigs[part] = cfg
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"base target-files had no filesystem_config for %s; created one", part))
		}
		// Parent directories need records too, or the image builder omits them.
		for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if !cfg.has(dir) {
				cfg.set(dir, 0, 0, 0o755, "capabilities=0x0")
			}
		}
		cfg.set(rel, 0, 0, uint32(e.Mode.Perm()), "capabilities=0x0")

		if strings.HasSuffix(rel, ".apk") {
			certs.addPresigned(path.Base(rel), part)
		}

		res.Added++
		res.Delta[part] += e.Size
		if opt.Progress != nil {
			opt.Progress(i+1, len(entries))
		}
	}

	// Rewrite the metadata we held back.
	for part, cfg := range fsConfigs {
		if err := writeBytes(zw, fmt.Sprintf("META/%s_filesystem_config.txt", part), cfg.render()); err != nil {
			return nil, err
		}
	}
	if err := writeBytes(zw, "META/apkcerts.txt", certs.render()); err != nil {
		return nil, err
	}
	if len(miscInfo) > 0 {
		updated, warnings := adjustMiscInfo(miscInfo, res.Delta, opt.Grow)
		res.Warnings = append(res.Warnings, warnings...)
		if err := writeBytes(zw, "META/misc_info.txt", updated); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	if err := out.Sync(); err != nil {
		return nil, err
	}
	return res, nil
}

// looksLikeTargetFiles rejects a factory image, OTA zip, or plain system image passed by mistake.
func looksLikeTargetFiles(zr *zip.ReadCloser) error {
	var hasMeta, hasTree bool
	for _, f := range zr.File {
		switch {
		case strings.HasPrefix(f.Name, "META/"):
			hasMeta = true
		case strings.HasPrefix(f.Name, "SYSTEM/"), strings.HasPrefix(f.Name, "PRODUCT/"),
			strings.HasPrefix(f.Name, "SYSTEM_EXT/"):
			hasTree = true
		}
		if hasMeta && hasTree {
			return nil
		}
	}
	return fmt.Errorf(
		"this does not look like a target-files package (no META/ plus partition trees).\n" +
			"It should be the *-target_files-*.zip your ROM build produces, not a\n" +
			"factory image, an OTA zip, or a bare partition image")
}

var fsConfigSuffix = "_filesystem_config.txt"

func fsConfigName(name string) (string, bool) {
	if !strings.HasPrefix(name, "META/") || !strings.HasSuffix(name, fsConfigSuffix) {
		return "", false
	}
	part := strings.TrimSuffix(strings.TrimPrefix(name, "META/"), fsConfigSuffix)
	if part == "" {
		return "", false
	}
	return part, true
}

func isCareMap(name string) bool {
	return name == "META/care_map.pb" || name == "META/care_map.txt"
}

func partitionOf(tfPath string) string {
	head, _, _ := strings.Cut(tfPath, "/")
	return strings.ToLower(head)
}

func underAny(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if name == p || strings.HasPrefix(name, p+"/") {
			return true
		}
	}
	return false
}

var storedExts = map[string]bool{
	".apk": true, ".jar": true, ".so": true, ".capex": true, ".apex": true,
}

func isStored(name string) bool {
	return storedExts[strings.ToLower(path.Ext(name))]
}

func writeBytes(zw *zip.Writer, name string, content []byte) error {
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: epoch}
	hdr.SetMode(0o644)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = w.Write(content)
	return err
}

// adjustMiscInfo checks each partition's size budget; overflow is otherwise a
// late failure inside add_img_to_target_files.
func adjustMiscInfo(raw []byte, delta map[string]int64, grow bool) ([]byte, []string) {
	lines := strings.Split(string(raw), "\n")
	var warnings []string

	for i, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok || !strings.HasSuffix(key, "_size") {
			continue
		}
		part := strings.TrimSuffix(key, "_size")
		added, touched := delta[part]
		if !touched || added <= 0 {
			continue
		}
		budget, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || budget <= 0 {
			continue
		}
		// Leave headroom; a partition packed to the byte fails to build.
		need := added + added/20
		warnings = append(warnings, fmt.Sprintf(
			"/%s grows by %.1f MiB against a %.1f MiB budget",
			part, float64(added)/(1<<20), float64(budget)/(1<<20)))
		if grow {
			lines[i] = fmt.Sprintf("%s=%d", key, budget+need)
			warnings = append(warnings, fmt.Sprintf(
				"raised %s to %.1f MiB (--ota-grow)",
				key, float64(budget+need)/(1<<20)))
		}
	}
	return []byte(strings.Join(lines, "\n")), warnings
}
