//go:build gui

package gui

import (
	"fmt"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/306gapps/306gapps/internal/config"
)

// savedPrefix keeps a saved selection distinct from a built-in preset of the same name.
const savedPrefix = "Saved: "

func (u *window) currentConfig(name string) config.Config {
	c := config.Config{
		Name:      name,
		Packages:  u.selection(),
		KeepStock: u.keepStockIDs(),
		Target:    string(u.state.target),
	}
	if u.cat != nil {
		c.Release = u.cat.Manifest().Release.Android.Version
	}
	return c
}

// applyConfig replaces the selection with a saved one.
func (u *window) applyConfig(c config.Config) {
	u.selected = map[string]bool{}
	for _, id := range u.cat.Prune(c.Packages) {
		u.selected[id] = true
	}
	u.state.keepStock = map[string]bool{}
	for _, id := range c.KeepStock {
		u.state.keepStock[id] = true
	}
	u.resolve()
}

// askToSave offers to keep the selection under a name, after the build rather
// than before, so a selection that did not build is never saved.
func (u *window) askToSave() {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("name this selection")
	d := dialog.NewForm("Save this selection?", "Save", "No thanks",
		[]*widget.FormItem{{Text: "Name", Widget: entry}},
		func(ok bool) {
			if !ok || strings.TrimSpace(entry.Text) == "" {
				return
			}
			if err := u.configs.Save(u.currentConfig(entry.Text)); err != nil {
				dialog.ShowError(err, u.win)
				return
			}
			u.rebuildVariants()
		}, u.win)
	d.Resize(fyne.NewSize(420, 180))
	d.Show()
}

func (u *window) onExport() {
	label := u.variant.Selected
	if !strings.HasPrefix(label, savedPrefix) {
		dialog.ShowInformation("Nothing to export",
			"Choose a saved selection first. The built-in presets come with the "+
				"release and are the same for everyone.", u.win)
		return
	}
	name := strings.TrimPrefix(label, savedPrefix)
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil || w == nil {
			return
		}
		path := uriPath(w.URI())
		w.Close()
		if err := u.configs.Export(name, path); err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		dialog.ShowInformation("Exported", filepath.Base(path), u.win)
	}, u.win)
	d.SetFileName(configFileName(name))
	showSized(d)
}

func (u *window) onImport() {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			return
		}
		path := uriPath(r.URI())
		r.Close()
		c, err := u.configs.Import(path)
		if err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		u.rebuildVariants()
		u.variant.SetSelected(savedPrefix + c.Name)
		dialog.ShowInformation("Imported",
			fmt.Sprintf("%s: %d packages", c.Name, len(c.Packages)), u.win)
	}, u.win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
	showSized(d)
}

func configFileName(name string) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		case r == ' ', r == '_':
			return '-'
		}
		return -1
	}, name)
	if s == "" {
		s = "selection"
	}
	return s + ".306gapps.json"
}
