//go:build gui

package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/306gapps/306gapps/internal/version"
)

// footer is the status line: which build this is, and what the payload cache
// is costing on disk.
func (u *window) footer() fyne.CanvasObject {
	ver := widget.NewLabel("306gapps " + version.String())
	ver.Importance = widget.LowImportance

	u.cacheLbl = widget.NewLabel("")
	u.cacheLbl.Importance = widget.LowImportance

	u.clearBtn = widget.NewButton("Clear cache", u.onClearCache)
	u.clearBtn.Importance = widget.LowImportance

	u.refreshCache()
	return container.NewBorder(nil, nil, ver,
		container.NewHBox(u.cacheLbl, u.clearBtn), nil)
}

// refreshCache measures the cache. Done inline: it holds a few hundred large
// blobs rather than a deep tree, so the walk is cheap, and a background
// goroutine touching widgets is how you get a torn render.
func (u *window) refreshCache() {
	if u.cacheLbl == nil || u.src == nil {
		return
	}
	n, err := u.src.Cache.Size()
	switch {
	case err != nil:
		u.cacheLbl.SetText("cache unavailable")
	case n == 0:
		u.cacheLbl.SetText("cache empty")
	default:
		u.cacheLbl.SetText("cache " + humanSize(n))
	}
	if n == 0 || err != nil {
		u.clearBtn.Disable()
	} else {
		u.clearBtn.Enable()
	}
}

func (u *window) onClearCache() {
	n, _ := u.src.Cache.Size()
	dialog.ShowConfirm("Clear the download cache?",
		"This frees "+humanSize(n)+". Payloads are downloaded again next build.",
		func(ok bool) {
			if !ok {
				return
			}
			if err := u.src.Cache.Clear(); err != nil {
				dialog.ShowError(err, u.win)
			}
			u.refreshCache()
		}, u.win)
}
