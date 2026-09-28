package build

import (
	"fmt"
	"strings"

	"github.com/306gapps/306gapps/internal/stage"
)

// buildRecovery emits a recovery-flashable zip: the shell installer plus the tree under files/.
func buildRecovery(plan *stage.Plan, w *writer, opt Options) error {
	if err := w.addTemplate("META-INF/com/google/android/update-binary", "recovery/update-binary", 0o755); err != nil {
		return err
	}
	// Recovery only checks that updater-script exists; update-binary does the work.
	if err := w.addBytes("META-INF/com/google/android/updater-script", 0o644, []byte("#MAGIC=306gapps\n")); err != nil {
		return err
	}
	// Fixed order: map iteration would vary the archive layout between builds.
	for _, t := range [][2]string{
		{"installer/installer.sh", "recovery/installer.sh"},
		{"installer/util.sh", "recovery/util.sh"},
		{"installer/addon.d.sh", "recovery/addon.d.sh"},
	} {
		if err := w.addTemplate(t[0], t[1], 0o755); err != nil {
			return err
		}
	}

	entries := sortedEntries(plan)
	if err := w.addBytes("installer/files.list", 0o644, filesList(entries)); err != nil {
		return err
	}
	if err := w.addBytes("installer/release.txt", 0o644, releaseInfo(plan)); err != nil {
		return err
	}
	if err := w.addBytes("installer/removals.txt", 0o644, linesFile(plan.Removes)); err != nil {
		return err
	}
	if err := w.addBytes("installer/props.txt", 0o644, propsFile(plan)); err != nil {
		return err
	}
	if err := w.addBytes("installer/digests.txt", 0o644, digestList(entries)); err != nil {
		return err
	}
	if err := w.addBytes("installer/packages.txt", 0o644, packagesFile(plan)); err != nil {
		return err
	}
	if opt.Busybox != "" {
		if err := w.addFile("installer/busybox", opt.Busybox, 0o755); err != nil {
			return fmt.Errorf("bundle busybox: %w", err)
		}
		if err := w.addTemplate("installer/NOTICE.busybox", "recovery/NOTICE.busybox", 0o644); err != nil {
			return err
		}
	}

	for i, e := range entries {
		switch {
		case e.IsSymlink():
			// Created by the installer from files.list; nothing to carry.
		case e.IsEmpty():
			// No payload was published, so carry the empty file itself.
			if err := w.addBytes("files/"+e.Path, e.Mode, nil); err != nil {
				return err
			}
		default:
			if err := w.addFile("files/"+e.Path, e.Local, e.Mode); err != nil {
				return err
			}
		}
		if opt.Progress != nil {
			opt.Progress(i+1, len(entries))
		}
	}
	return nil
}

// filesList renders the installer's work list, one tab-separated record per file.
func filesList(entries []stage.Entry) []byte {
	var b strings.Builder
	for _, e := range entries {
		ctx := e.Context
		if ctx == "" {
			ctx = defaultContext(e.Path)
		}
		// A fifth field marks a symlink and names its target; records without
		// it are payloads, so older readers see the same four columns.
		if e.IsSymlink() {
			fmt.Fprintf(&b, "%s\t%04o\t%s\t0\t%s\n", e.Path, e.Mode.Perm(), ctx, e.Target)
			continue
		}
		fmt.Fprintf(&b, "%s\t%04o\t%s\t%d\n", e.Path, e.Mode.Perm(), ctx, e.Size)
	}
	return []byte(b.String())
}

// digestList lets the installer re-verify each payload on the device.
func digestList(entries []stage.Entry) []byte {
	var b strings.Builder
	for _, e := range entries {
		if e.IsSymlink() || e.SHA256 == "" {
			continue
		}
		fmt.Fprintf(&b, "%s  %s\n", e.Path, e.SHA256)
	}
	return []byte(b.String())
}

// defaultContext supplies the SELinux label for a partition when the dump recorded none.
func defaultContext(path string) string {
	part, _, _ := strings.Cut(path, "/")
	switch part {
	case "product":
		return "u:object_r:system_file:s0"
	case "system_ext":
		return "u:object_r:system_file:s0"
	case "vendor":
		return "u:object_r:vendor_file:s0"
	default:
		return "u:object_r:system_file:s0"
	}
}
