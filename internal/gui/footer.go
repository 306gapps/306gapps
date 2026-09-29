//go:build gui

package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/306gapps/306gapps/internal/update"
	"net/url"

	"github.com/306gapps/306gapps/internal/version"
)

// footer is the status line: which build this is, and what the payload cache
// is costing on disk.
// Repo is checked for a newer release.
const Repo = "306gapps/306gapps"

func (u *window) footer() fyne.CanvasObject {
	u.verLbl = widget.NewLabel("306gapps " + version.String())
	u.verLbl.Importance = widget.LowImportance

	// Replaces the version label when a newer release exists.
	u.updateLink = widget.NewHyperlink("", nil)
	u.updateLink.Hide()

	u.cacheLbl = widget.NewLabel("")
	u.cacheLbl.Importance = widget.LowImportance

	u.clearBtn = widget.NewButton("Clear cache", u.onClearCache)
	u.clearBtn.Importance = widget.LowImportance

	u.refreshCache()
	go u.checkForUpdate()
	return container.NewBorder(nil, nil,
		container.NewHBox(u.verLbl, u.updateLink),
		container.NewHBox(u.cacheLbl, u.clearBtn), nil)
}

// checkForUpdate asks GitHub once at startup. Failure is silence: not being
// able to reach GitHub is not something to interrupt anyone about.
func (u *window) checkForUpdate() {
	rel, newer, err := update.Check(u.ctx, Repo, version.String())
	if err != nil || !newer {
		return
	}
	link, err := url.Parse(rel.URL)
	if err != nil {
		return
	}
	fyne.Do(func() {
		u.updateLink.SetText(rel.Tag + " available")
		u.updateLink.SetURL(link)
		u.updateLink.Show()
	})
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
