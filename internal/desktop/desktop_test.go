package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/306gapps/306gapps/internal/gui/icon"
)

func TestInstall(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	if _, err := Install(); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadFile(filepath.Join(dir, "applications", icon.AppID+".desktop"))
	if err != nil {
		t.Fatal(err)
	}
	// The compositor matches app_id to this file and reads Icon= from it.
	for _, want := range []string{"Icon=" + icon.AppID, "StartupWMClass=" + icon.AppID} {
		if !strings.Contains(string(entry), want) {
			t.Errorf("entry missing %q:\n%s", want, entry)
		}
	}
	for _, px := range []string{"16", "22", "48", "512"} {
		p := filepath.Join(dir, "icons", "hicolor", px+"x"+px, "apps", icon.AppID+".png")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("no %sx%s icon: %v", px, px, err)
		}
	}

	if !iconsPresent() {
		t.Error("iconsPresent false right after install")
	}
	os.Remove(filepath.Join(dir, "icons", "hicolor", "22x22", "apps", icon.AppID+".png"))
	if iconsPresent() {
		t.Error("iconsPresent true with a size missing")
	}
	Ensure()
	if !iconsPresent() {
		t.Error("Ensure did not put the missing size back")
	}
}
