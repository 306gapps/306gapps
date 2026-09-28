package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// UninstallerOptions control the uninstaller zip.
type UninstallerOptions struct {
	Out string
	// Busybox is optional, bundled the same way as for an install.
	Busybox string
}

// BuildUninstaller emits a recovery-flashable zip that removes any 306gapps install.
// It reads back /system/etc/306gapps/files.list, so it carries no payload and is
// not tied to a release. Apps an install removed come back by dirty-flashing the ROM.
func BuildUninstaller(opt UninstallerOptions) (*Result, error) {
	if opt.Out == "" {
		return nil, fmt.Errorf("no output path given")
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
	w := newWriter(io.MultiWriter(f, h))

	if err := w.addTemplate("META-INF/com/google/android/update-binary",
		"uninstaller/update-binary", 0o755); err != nil {
		return nil, err
	}
	if err := w.addBytes("META-INF/com/google/android/updater-script", 0o644,
		[]byte("#MAGIC=306gapps-uninstall\n")); err != nil {
		return nil, err
	}
	for _, t := range [][2]string{
		{"installer/uninstaller.sh", "uninstaller/uninstaller.sh"},
		// An uninstall has to reach exactly the partitions an install did.
		{"installer/util.sh", "recovery/util.sh"},
	} {
		if err := w.addTemplate(t[0], t[1], 0o755); err != nil {
			return nil, err
		}
	}
	if opt.Busybox != "" {
		if err := w.addFile("installer/busybox", opt.Busybox, 0o755); err != nil {
			return nil, fmt.Errorf("bundle busybox: %w", err)
		}
		if err := w.addTemplate("installer/NOTICE.busybox", "recovery/NOTICE.busybox", 0o644); err != nil {
			return nil, err
		}
	}

	if err := w.Close(); err != nil {
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
		Path: opt.Out, Target: "uninstaller", Size: st.Size(),
		Files: w.count, SHA256: hex.EncodeToString(h.Sum(nil)),
	}, nil
}
