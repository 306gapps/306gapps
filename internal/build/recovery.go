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

	for i, e := range entries {
		if err := w.addFile("files/"+e.Path, e.Local, e.Mode); err != nil {
			return err
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
		fmt.Fprintf(&b, "%s\t%04o\t%s\t%d\n", e.Path, e.Mode.Perm(), ctx, e.Size)
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
