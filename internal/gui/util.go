//go:build gui

package gui

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"github.com/306gapps/306gapps/internal/build"
)

func widgetColumn(items ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(items...)
}

func osRename(from, to string) error { return os.Rename(from, to) }

// configDir matches the CLI's, so packages built either way carry the same signature.
func configDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "306gapps")
	}
	return ".306gapps"
}

// restat refreshes size and digest after signing rewrote the file.
func restat(res *build.Result) (*build.Result, error) {
	f, err := os.Open(res.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	out := *res
	out.Size = st.Size()
	out.SHA256 = hex.EncodeToString(h.Sum(nil))
	return &out, nil
}
