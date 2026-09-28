//go:build gui

package gui

import (
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/306gapps/306gapps/internal/build"
)

// TestRenderSnapshot writes the window to a png so the layout can be looked at
// without a display. Skipped unless GUI_SNAPSHOT names a path.
func TestRenderSnapshot(t *testing.T) {
	out := os.Getenv("GUI_SNAPSHOT")
	if out == "" {
		t.Skip("set GUI_SNAPSHOT to write a snapshot")
	}
	u := loaded(t)
	// GUI_SNAPSHOT_SOURCE points the snapshot at a real release instead of the
	// fixture, which is the only way to see how the layout holds at the real
	// package count.
	pick := "dialer-google"
	if root := os.Getenv("GUI_SNAPSHOT_SOURCE"); root != "" {
		u = loadedFrom(t, root)
		pick = "chrome"
	}
	// Tick something that pulls a dependency in, so the snapshot shows the
	// states that matter rather than only the idle one.
	if v := os.Getenv("GUI_SNAPSHOT_VARIANT"); v != "" {
		u.applyVariant(v)
	} else {
		u.selected[pick] = true
	}
	if f := os.Getenv("GUI_SNAPSHOT_FILTER"); f != "" {
		u.filter.SetText(f)
		u.rebuildList()
	}
	u.resolve()
	u.refreshSummary()
	if os.Getenv("GUI_SNAPSHOT_OTA") != "" {
		u.target.SetSelected(targetLabel(build.TargetOTA))
		u.state.otaBase = "/home/you/out/target/product/redfin/cr13-target_files.zip"
		u.state.otaKeys = "/home/you/keys/redfin"
		u.refreshOTA()
	}
	u.win.Resize(fyne.NewSize(940, 760))
	u.win.Content().Refresh()

	img := u.win.Canvas().Capture()
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%v)", out, img.Bounds())
	_ = test.NewApp
}
