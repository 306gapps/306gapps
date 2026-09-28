//go:build gui

package gui

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
)

// uriPath turns a picker result into a path the os package will accept.
func uriPath(u fyne.URI) string {
	if u == nil {
		return ""
	}
	return normalizePath(u.Path(), runtime.GOOS)
}

// normalizePath takes goos as an argument so the Windows path can be tested anywhere.
// A file URI may render a Windows path as "/C:/dir/file", and os will not open
// that leading slash; UNC paths keep both of theirs.
func normalizePath(p, goos string) string {
	if goos != "windows" {
		return p
	}
	// Not filepath.FromSlash: it keys off the running OS, so it is a no-op elsewhere.
	toBackslash := func(s string) string { return strings.ReplaceAll(s, "/", `\`) }

	if strings.HasPrefix(p, "//") {
		// UNC: //server/share
		return toBackslash(p)
	}
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' && isDriveLetter(p[1]) {
		p = p[1:]
	}
	return toBackslash(p)
}

func isDriveLetter(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// writableDir checks the destination up front, before half an hour of downloading.
func writableDir(dir string) error {
	if dir == "" {
		return errors.New("choose a folder to save the package to")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot use %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".306gapps-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}
