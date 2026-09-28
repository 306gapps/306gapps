//go:build gui

package gui

import (
	"context"
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"github.com/306gapps/306gapps/internal/source"
)

// TestLiveSnapshot renders the window against a real published release, so the
// layout can be judged at the size it will actually be used. Skipped unless
// LIVE_SNAPSHOT names a path prefix.
func TestLiveSnapshot(t *testing.T) {
	prefix := os.Getenv("LIVE_SNAPSHOT")
	if prefix == "" {
		t.Skip("set LIVE_SNAPSHOT to write snapshots")
	}
	src := os.Getenv("LIVE_SOURCE")
	if src == "" {
		src = "https://raw.githubusercontent.com/306gapps/306gapps-assets/main"
	}

	test.NewApp()
	s := newState(context.Background(),
		source.New(src, source.NewCache(os.Getenv("GAPPS_CACHE"))), t.TempDir())
	u := &window{state: s, win: test.NewWindow(nil)}
	u.win.SetContent(u.build())

	idx, err := u.src.Index(u.ctx)
	if err != nil {
		t.Skipf("no live source: %v", err)
	}
	ref, ok := idx.Latest(0)
	if !ok {
		t.Skip("no releases published")
	}
	u.index = idx
	if err := u.loadRelease(ref); err != nil {
		t.Fatal(err)
	}
	// Mirror what the release dropdown would show.
	u.releases.PlaceHolder = ""
	u.releases.Options = []string{"Android " + ref.Android.Version + " — " + ref.ID +
		" (" + ref.Device + " " + ref.Build + ")"}
	u.releases.SetSelectedIndex(0)

	u.rebuildList()
	// A realistic pick rather than the defaults alone.
	for _, id := range []string{"gboard", "dialer", "webview", "pixellauncher", "talkback"} {
		u.selected[id] = true
	}
	u.resolve()
	u.refreshSummary()

	u.win.Resize(fyne.NewSize(1000, 820))
	u.win.Content().Refresh()

	scroll := findScroll(u.win.Content())
	type view struct {
		filter string
		offset float32
	}
	for i, v := range []view{{"", 0}, {"", 700}, {os.Getenv("LIVE_FILTER"), 0}} {
		u.filter.SetText(v.filter)
		u.rebuildList()
		u.refreshSummary()
		if scroll != nil {
			scroll.Offset = fyne.NewPos(0, v.offset)
			scroll.Refresh()
		}
		u.win.Content().Refresh()
		f, err := os.Create(prefixPath(prefix, i))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, u.win.Canvas().Capture()); err != nil {
			t.Fatal(err)
		}
		f.Close()
		t.Logf("wrote %s", prefixPath(prefix, i))
	}
}

func prefixPath(prefix string, i int) string {
	return prefix + string(rune('1'+i)) + ".png"
}

func findScroll(o fyne.CanvasObject) *container.Scroll {
	switch v := o.(type) {
	case *container.Scroll:
		return v
	case *fyne.Container:
		for _, c := range v.Objects {
			if s := findScroll(c); s != nil {
				return s
			}
		}
	}
	return nil
}
