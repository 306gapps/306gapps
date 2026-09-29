// Package desktop installs the freedesktop entry that gives the window its icon.
package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/306gapps/306gapps/internal/gui/icon"
)

// Install writes the desktop entry and icon, returning the paths it wrote.
//
// Under Wayland an application cannot hand the compositor its own icon. The
// compositor takes the surface's app_id, looks for the desktop entry of that
// name and reads Icon= from it, which is why an unintegrated AppImage shows a
// placeholder too. Fyne sets app_id from the app's unique ID, so the entry has
// to be named icon.AppID.
func Install() ([]string, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("desktop entries are a Linux thing; on %s the icon is in the binary", runtime.GOOS)
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	written := []string{}
	entryPath := filepath.Join(dataDir(), "applications", icon.AppID+".desktop")
	if err := write(entryPath, []byte(entry(self))); err != nil {
		return nil, err
	}
	written = append(written, entryPath)

	// A panel draws this at 22px, where a downscaled 512 is mush, so ship the
	// size the theme asks for.
	names, err := icon.Sizes.ReadDir("sizes")
	if err != nil {
		return nil, err
	}
	for _, e := range names {
		px := strings.TrimSuffix(e.Name(), ".png")
		data, err := icon.Sizes.ReadFile("sizes/" + e.Name())
		if err != nil {
			return nil, err
		}
		p := filepath.Join(dataDir(), "icons", "hicolor", px+"x"+px, "apps", icon.AppID+".png")
		if err := write(p, data); err != nil {
			return nil, err
		}
		written = append(written, p)
	}

	// Plasma and GNOME both cache these directories.
	if bin, err := exec.LookPath("update-desktop-database"); err == nil {
		exec.Command(bin, filepath.Dir(entryPath)).Run()
	}
	if bin, err := exec.LookPath("gtk-update-icon-cache"); err == nil {
		exec.Command(bin, "-tq", filepath.Join(dataDir(), "icons", "hicolor")).Run()
	}
	return written, nil
}

func write(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// Ensure installs the entry unless one matching this binary is already there.
func Ensure() {
	if runtime.GOOS != "linux" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	current := entry(self)
	installed, err := os.ReadFile(filepath.Join(dataDir(), "applications", icon.AppID+".desktop"))
	if err == nil && string(installed) == current && iconsPresent() {
		return
	}
	Install()
}

func dataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

func entry(exe string) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=306gapps
GenericName=GApps builder
Comment=Build a Google apps package for a custom ROM
Exec=%s gui
TryExec=%s
Icon=%s
Terminal=false
Categories=Utility;
StartupWMClass=%s
StartupNotify=false
`, exe, exe, icon.AppID, icon.AppID)
}

func iconsPresent() bool {
	names, err := icon.Sizes.ReadDir("sizes")
	if err != nil {
		return false
	}
	for _, e := range names {
		px := strings.TrimSuffix(e.Name(), ".png")
		if _, err := os.Stat(filepath.Join(dataDir(),
			"icons", "hicolor", px+"x"+px, "apps", icon.AppID+".png")); err != nil {
			return false
		}
	}
	return true
}
