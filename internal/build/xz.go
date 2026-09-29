package build

import (
	"io"
	"os/exec"

	"github.com/ulikunitz/xz"
)

// systemXZ reports whether a system xz binary is on PATH. Its absence (typically
// Windows) is why the recovery target only defaults to xz when it is present.
// A var so tests can exercise both the xz and the deflate path on any host.
var systemXZ = func() bool {
	_, err := exec.LookPath("xz")
	return err == nil
}

// xzCompress writes src to dst as an xz stream.
//
// The system xz is preferred: it is multi-threaded and much faster on the large
// apks this packs. Where it is absent (typically Windows) a pure-Go encoder
// keeps the builder working, slower.
func xzCompress(dst io.Writer, src io.Reader) error {
	if bin, err := exec.LookPath("xz"); err == nil {
		cmd := exec.Command(bin, "-6", "-T0", "-c")
		cmd.Stdin = src
		cmd.Stdout = dst
		if err := cmd.Run(); err == nil {
			return nil
		}
		// Fall through to the pure-Go path if the binary misbehaves.
	}
	w, err := xz.NewWriter(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		return err
	}
	return w.Close()
}
