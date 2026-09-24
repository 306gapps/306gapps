package build

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/306gapps/306gapps/internal/stage"
)

// SigningOptions describe the ROM-specific inputs the OTA target needs.
//
// A sideloadable A/B package is verified by the device against the certificate
// in /system/etc/security/otacerts.zip, so it must be signed with the ROM's own
// release key. We cannot supply that key, and neither can a generic build
// service -- only whoever builds the ROM has it. The OTA target therefore emits
// a target-files overlay plus the exact AOSP command line to merge and sign it,
// rather than pretending to produce a finished payload.
type SigningOptions struct {
	// Base is the ROM's signed target-files zip to merge into.
	Base string
	// Key and Cert are the ROM's release signing key pair (.pk8 / .x509.pem).
	Key  string
	Cert string
	// ToolsDir is an AOSP otatools directory containing ota_from_target_files
	// and merge_target_files.
	ToolsDir string
}

func (s SigningOptions) complete() bool {
	return s.Base != "" && s.Key != "" && s.Cert != "" && s.ToolsDir != ""
}

// buildOTA emits a target-files overlay: the gapps payload laid out the way
// AOSP's OTA tooling expects, with the filesystem_config and file_contexts
// records the image builder needs to reproduce ownership and SELinux labels.
func buildOTA(plan *stage.Plan, w *writer, opt Options) error {
	entries := sortedEntries(plan)

	byPart := map[string][]stage.Entry{}
	for _, e := range entries {
		part, _, _ := strings.Cut(e.Path, "/")
		byPart[part] = append(byPart[part], e)
	}

	for i, e := range entries {
		part, rest, _ := strings.Cut(e.Path, "/")
		if err := w.addFile(strings.ToUpper(part)+"/"+rest, e.Local, e.Mode); err != nil {
			return err
		}
		if opt.Progress != nil {
			opt.Progress(i+1, len(entries))
		}
	}

	parts := make([]string, 0, len(byPart))
	for p := range byPart {
		parts = append(parts, p)
	}
	sort.Strings(parts)

	for _, part := range parts {
		if err := w.addBytes("META/"+part+"_filesystem_config.txt", 0o644, filesystemConfig(byPart[part])); err != nil {
			return err
		}
		if err := w.addBytes("META/"+part+"_file_contexts.txt", 0o644, fileContexts(byPart[part])); err != nil {
			return err
		}
	}

	if err := w.addBytes("META/306gapps-release.txt", 0o644, releaseInfo(plan)); err != nil {
		return err
	}
	if err := w.addBytes("META/306gapps-props.txt", 0o644, propsFile(plan)); err != nil {
		return err
	}
	if err := w.addBytes("META/306gapps-removals.txt", 0o644, linesFile(plan.Removes)); err != nil {
		return err
	}
	return w.addBytes("merge-and-sign.sh", 0o755, mergeScript(plan, parts, opt.Signing))
}

// filesystemConfig records ownership and mode for every path, including the
// intermediate directories the image builder has to create.
func filesystemConfig(entries []stage.Entry) []byte {
	type meta struct {
		uid, gid int
		mode     uint32
	}
	rows := map[string]meta{}

	for _, e := range entries {
		_, rest, _ := strings.Cut(e.Path, "/")
		// Privileged apps and their parent dirs stay root-owned 0755/0644.
		rows[rest] = meta{0, 0, uint32(e.Mode.Perm())}
		for dir := parentDirs(rest); dir != ""; dir = parentDirs(dir) {
			if _, ok := rows[dir]; !ok {
				rows[dir] = meta{0, 0, 0o755}
			}
		}
	}

	paths := make([]string, 0, len(rows))
	for p := range rows {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var b strings.Builder
	for _, p := range paths {
		m := rows[p]
		fmt.Fprintf(&b, "%s %d %d %o capabilities=0x0\n", p, m.uid, m.gid, m.mode)
	}
	return []byte(b.String())
}

// fileContexts maps each installed path to its SELinux label, escaped as the
// regex form file_contexts uses.
func fileContexts(entries []stage.Entry) []byte {
	seen := map[string]string{}
	for _, e := range entries {
		ctx := e.Context
		if ctx == "" {
			ctx = defaultContext(e.Path)
		}
		seen["/"+e.Path] = ctx
		for dir := parentDirs("/" + e.Path); strings.Count(dir, "/") > 1; dir = parentDirs(dir) {
			if _, ok := seen[dir]; !ok {
				seen[dir] = ctx
			}
		}
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var b strings.Builder
	for _, p := range paths {
		fmt.Fprintf(&b, "%s %s\n", escapeContextPath(p), seen[p])
	}
	return []byte(b.String())
}

var contextSpecials = regexp.MustCompile(`([.+*?\[\]{}()|^$\\])`)

func escapeContextPath(p string) string {
	return contextSpecials.ReplaceAllString(p, `\$1`)
}

func parentDirs(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return ""
	}
	return p[:i]
}

// mergeScript writes the exact AOSP invocation needed to turn this overlay into
// a signed, sideloadable package.
func mergeScript(plan *stage.Plan, parts []string, s SigningOptions) []byte {
	base, key, cert, tools := s.Base, s.Key, s.Cert, s.ToolsDir
	if base == "" {
		base = "<rom-target-files.zip>"
	}
	if key == "" {
		key = "<releasekey.pk8>"
	}
	if cert == "" {
		cert = "<releasekey.x509.pem>"
	}
	if tools == "" {
		tools = "<aosp>/out/host/linux-x86/bin"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "#!/usr/bin/env bash\n")
	fmt.Fprintf(&b, "# Generated by 306gapps for release %s.\n#\n", plan.Release.ID)
	fmt.Fprintf(&b, "# This overlay is not itself flashable. A sideloadable A/B package must be\n")
	fmt.Fprintf(&b, "# signed with the key your device already trusts (the one whose certificate\n")
	fmt.Fprintf(&b, "# is in /system/etc/security/otacerts.zip), so the final two steps have to run\n")
	fmt.Fprintf(&b, "# wherever that key lives -- normally your own ROM build tree.\n#\n")
	fmt.Fprintf(&b, "# Partitions touched: %s\n\n", strings.Join(parts, " "))
	fmt.Fprintf(&b, "set -euo pipefail\n\n")
	fmt.Fprintf(&b, "OVERLAY=${OVERLAY:-%q}\n", "306gapps-overlay.zip")
	fmt.Fprintf(&b, "BASE=${BASE:-%q}\n", base)
	fmt.Fprintf(&b, "KEY=${KEY:-%q}\n", key)
	fmt.Fprintf(&b, "CERT=${CERT:-%q}\n", cert)
	fmt.Fprintf(&b, "TOOLS=${TOOLS:-%q}\n", tools)
	fmt.Fprintf(&b, "export PATH=\"$TOOLS:$PATH\"\n\n")
	fmt.Fprintf(&b, "# 1. Merge the gapps overlay into the ROM's target-files.\n")
	fmt.Fprintf(&b, "merge_target_files \\\n")
	fmt.Fprintf(&b, "  --framework-target-files \"$BASE\" \\\n")
	fmt.Fprintf(&b, "  --vendor-target-files \"$OVERLAY\" \\\n")
	fmt.Fprintf(&b, "  --output-target-files merged-target-files.zip\n\n")
	fmt.Fprintf(&b, "# 2. Rebuild the partition images with the gapps included.\n")
	fmt.Fprintf(&b, "add_img_to_target_files -a merged-target-files.zip\n\n")
	fmt.Fprintf(&b, "# 3. Sign with the ROM's release key.\n")
	fmt.Fprintf(&b, "sign_target_files_apks \\\n")
	fmt.Fprintf(&b, "  --default_key_mappings \"$(dirname \"$KEY\")\" \\\n")
	fmt.Fprintf(&b, "  merged-target-files.zip signed-target-files.zip\n\n")
	fmt.Fprintf(&b, "# 4. Produce the sideloadable package.\n")
	fmt.Fprintf(&b, "ota_from_target_files \\\n")
	fmt.Fprintf(&b, "  --package_key \"${KEY%%.pk8}\" \\\n")
	fmt.Fprintf(&b, "  signed-target-files.zip 306gapps-%s-ota.zip\n\n", plan.Release.ID)
	fmt.Fprintf(&b, "echo \"adb sideload 306gapps-%s-ota.zip\"\n", plan.Release.ID)
	return []byte(b.String())
}
