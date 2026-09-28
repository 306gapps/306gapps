//go:build gui

package gui

import (
	"context"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/306gapps/306gapps/internal/build"
	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/source"
)

// Run opens the picker.
func Run(ctx context.Context, src *source.Source, outDir string) error {
	s := newState(ctx, src, outDir)

	a := app.NewWithID("com.306gapps.picker")
	w := a.NewWindow("306gapps")
	w.Resize(fyne.NewSize(940, 700))

	ui := &window{state: s, win: w}
	w.SetContent(ui.build())

	// Fetch after the window is up so a slow network shows a window, not nothing.
	go ui.loadIndex()

	w.ShowAndRun()
	return nil
}

type window struct {
	*state
	win fyne.Window

	releases   *widget.Select
	list       *fyne.Container
	summaryLbl *widget.Label
	warning    *widget.Label
	target     *widget.Select
	buildBtn   *widget.Button
	status     *widget.Label

	refs map[string]source.ReleaseRef
}

func (u *window) build() fyne.CanvasObject {
	u.releases = widget.NewSelect(nil, u.onRelease)
	u.releases.PlaceHolder = "loading releases…"

	byLabel := map[string]build.Target{}
	labels := make([]string, 0, len(build.Targets))
	for _, t := range build.Targets {
		byLabel[targetLabel(t)] = t
		labels = append(labels, targetLabel(t))
	}
	u.target = widget.NewSelect(labels, func(label string) {
		if t, ok := byLabel[label]; ok {
			u.state.target = t
		}
	})
	u.target.SetSelectedIndex(0)

	u.summaryLbl = widget.NewLabel("")
	u.warning = widget.NewLabel("")
	u.warning.Wrapping = fyne.TextWrapWord
	u.warning.Importance = widget.DangerImportance
	u.status = widget.NewLabel("")

	u.buildBtn = widget.NewButtonWithIcon("Build", theme.DownloadIcon(), u.onBuild)
	u.buildBtn.Importance = widget.HighImportance
	u.buildBtn.Disable()

	u.list = container.NewVBox()

	top := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("Release"), nil, u.releases),
		widget.NewSeparator(),
	)
	bottom := container.NewVBox(
		widget.NewSeparator(),
		u.warning,
		u.summaryLbl,
		container.NewBorder(nil, nil,
			widget.NewLabel("Format"), u.buildBtn, u.target),
		u.status,
	)
	return container.NewBorder(top, bottom, nil, nil,
		container.NewVScroll(u.list))
}

func targetLabel(t build.Target) string {
	return fmt.Sprintf("%s — %s", t, t.Description())
}

func (u *window) loadIndex() {
	idx, err := u.src.Index(u.ctx)
	if err != nil {
		fyne.Do(func() { dialog.ShowError(err, u.win) })
		return
	}
	u.index = idx
	u.refs = map[string]source.ReleaseRef{}
	var labels []string
	for _, api := range idx.APIs() {
		ref, ok := idx.Latest(api)
		if !ok {
			continue
		}
		label := fmt.Sprintf("Android %s — %s (%s %s)",
			ref.Android.Version, ref.ID, ref.Device, ref.Build)
		u.refs[label] = ref
		labels = append(labels, label)
	}

	fyne.Do(func() {
		u.releases.PlaceHolder = "Select a release"
		u.releases.Options = labels
		u.releases.Refresh()
		if len(labels) > 0 {
			u.releases.SetSelected(labels[0])
		} else {
			u.status.SetText("this source has published no releases yet")
		}
	})
}

func (u *window) onRelease(label string) {
	ref, ok := u.refs[label]
	if !ok {
		return
	}
	u.status.SetText("loading " + ref.ID + "…")
	go func() {
		err := u.loadRelease(ref)
		fyne.Do(func() {
			if err != nil {
				dialog.ShowError(err, u.win)
				u.status.SetText("")
				return
			}
			u.status.SetText("")
			u.rebuildList()
			u.refreshSummary()
		})
	}()
}

// rebuildList redraws the whole package list, which only the release changing needs.
func (u *window) rebuildList() {
	u.list.RemoveAll()
	for _, category := range u.categories() {
		header := widget.NewLabelWithStyle(
			categoryTitle(category), fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true})
		u.list.Add(header)

		for _, p := range u.packagesIn(category) {
			u.list.Add(u.packageRow(p))
		}
		u.list.Add(widget.NewSeparator())
	}
	u.list.Refresh()
	u.buildBtn.Enable()
}

func (u *window) packageRow(p manifest.Package) fyne.CanvasObject {
	check := widget.NewCheck("", nil)
	check.SetChecked(u.selected[p.ID] || p.Required)
	if p.Required {
		// A required package cannot be deselected, so say so rather than
		// offering a control that silently does nothing.
		check.Disable()
	}
	check.OnChanged = func(on bool) {
		u.selected[p.ID] = on
		u.resolve()
		u.refreshSummary()
	}

	name := widget.NewLabel(p.Name)
	id := widget.NewLabel(p.ID)
	id.Importance = widget.LowImportance
	size := widget.NewLabel(humanSize(p.Size()))
	size.Alignment = fyne.TextAlignTrailing

	row := container.NewBorder(nil, nil,
		container.NewHBox(check, name),
		size,
		container.New(layout.NewHBoxLayout(), id))

	if p.Summary != "" {
		note := widget.NewLabel(p.Summary)
		note.Wrapping = fyne.TextWrapWord
		note.Importance = widget.LowImportance
		return container.NewVBox(row, note)
	}
	return row
}

func categoryTitle(c string) string {
	switch c {
	case "core":
		return "CORE — required for anything Google to work"
	case "setup":
		return "SETUP"
	case "apps":
		return "APPS"
	case "pixel":
		return "PIXEL"
	case "accessibility":
		return "ACCESSIBILITY"
	case "extras":
		return "EXTRAS"
	}
	return c
}

func (u *window) refreshSummary() {
	u.summaryLbl.SetText(u.state.summary())
	if u.resErr != nil {
		u.warning.SetText(u.resErr.Error())
		u.buildBtn.Disable()
	} else {
		u.warning.SetText("")
		u.buildBtn.Enable()
	}
	u.summaryLbl.Refresh()
	u.warning.Refresh()
}
