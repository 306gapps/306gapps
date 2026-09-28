//go:build gui

package gui

import (
	"fmt"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/306gapps/306gapps/internal/build"
)

// otaForm collects the ROM target-files and the keys that ROM was signed with.
type otaForm struct {
	panel *fyne.Container
	base  *widget.Entry
	keys  *widget.Entry
	tools *widget.Entry
	grow  *widget.Check
	note  *widget.Label

	browseBase  *widget.Button
	browseKeys  *widget.Button
	clearKeys   *widget.Button
	browseTools *widget.Button
}

// sized is the dialog interface the two pickers share.
type sized interface {
	Show()
	Resize(fyne.Size)
}

// Show must come first: FileDialog.Resize calls MinSize, which panics on a field Show creates.
func showSized(d sized) {
	d.Show()
	d.Resize(fyne.NewSize(760, 560))
}

func (u *window) buildOTAForm() *otaForm {
	f := &otaForm{}

	f.base = pathEntry("no target-files chosen")
	f.keys = pathEntry("none — stops at a merged target-files")
	f.tools = pathEntry("otatools on PATH")

	f.note = widget.NewLabel("")
	f.note.Wrapping = fyne.TextWrapWord
	f.note.Importance = widget.LowImportance

	f.grow = widget.NewCheck("Grow a partition if the selection overflows it", func(on bool) {
		u.state.otaGrow = on
	})

	f.browseBase = widget.NewButton("Browse…", func() {
		d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil || r == nil {
				return
			}
			defer r.Close()
			u.state.otaBase = uriPath(r.URI())
			u.refreshOTA()
		}, u.win)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".zip"}))
		showSized(d)
	})

	f.browseKeys = widget.NewButton("Browse…", func() {
		u.pickFolder(func(path string) {
			u.state.otaKeys = path
			u.refreshOTA()
		})
	})
	f.clearKeys = widget.NewButton("Clear", func() {
		u.state.otaKeys = ""
		u.refreshOTA()
	})

	f.browseTools = widget.NewButton("Browse…", func() {
		u.pickFolder(func(path string) {
			u.state.otaTools = path
			u.refreshOTA()
		})
	})

	row := func(label string, e *widget.Entry, buttons ...fyne.CanvasObject) fyne.CanvasObject {
		return container.NewBorder(nil, nil,
			widget.NewLabel(label), container.NewHBox(buttons...), e)
	}

	f.panel = container.NewVBox(
		widget.NewSeparator(),
		row("ROM target-files", f.base, f.browseBase),
		row("Signing keys", f.keys, f.browseKeys, f.clearKeys),
		row("otatools", f.tools, f.browseTools),
		f.grow,
		f.note,
	)
	f.panel.Hide()
	return f
}

func pathEntry(placeholder string) *widget.Entry {
	e := widget.NewEntry()
	e.SetPlaceHolder(placeholder)
	// Read-only: the path always comes from the file picker.
	e.Disable()
	return e
}

// setPath briefly enables the entry, since Fyne ignores SetText while disabled.
func setPath(e *widget.Entry, path string) {
	if e.Text == path {
		return
	}
	e.Enable()
	e.SetText(path)
	e.Disable()
}

func (u *window) pickFolder(set func(string)) {
	d := dialog.NewFolderOpen(func(list fyne.ListableURI, err error) {
		if err != nil || list == nil {
			return
		}
		set(uriPath(list))
	}, u.win)
	showSized(d)
}

// refreshOTA shows the panel only for the ota target and says what the inputs will produce.
func (u *window) refreshOTA() {
	if u.ota == nil {
		return
	}
	if u.state.target != build.TargetOTA {
		u.ota.panel.Hide()
		u.refreshBuildButton()
		return
	}
	u.ota.panel.Show()

	setPath(u.ota.base, u.state.otaBase)
	setPath(u.ota.keys, u.state.otaKeys)
	setPath(u.ota.tools, u.state.otaTools)
	u.ota.grow.SetChecked(u.state.otaGrow)

	switch {
	case u.state.otaBase == "":
		u.ota.note.SetText("Choose the *-target_files-*.zip your ROM build produces.")
	case u.state.otaKeys == "":
		u.ota.note.SetText("Without keys the merge still runs and stops at a merged " +
			"target-files package you can sign yourself.")
	default:
		u.ota.note.SetText(fmt.Sprintf(
			"Signing with the keys in %s. They must be the keys this ROM was "+
				"built with: a sideload is checked against the certificate "+
				"already on the device.", filepath.Base(u.state.otaKeys)))
	}
	u.refreshBuildButton()
}

// refreshBuildButton keeps Build disabled while the chosen target cannot run.
func (u *window) refreshBuildButton() {
	if u.buildBtn == nil {
		return
	}
	if u.res == nil || u.resErr != nil {
		u.buildBtn.Disable()
		return
	}
	if u.state.target == build.TargetOTA && u.state.otaBase == "" {
		u.buildBtn.Disable()
		return
	}
	u.buildBtn.Enable()
}

// signingOptions describes the ota build, or reports why it cannot run.
func (u *window) signingOptions(report func(string, float64)) (build.SigningOptions, error) {
	s := u.state
	if s.otaBase == "" {
		return build.SigningOptions{}, fmt.Errorf(
			"choose your ROM's target-files package first")
	}
	if st, err := os.Stat(s.otaBase); err != nil || st.IsDir() {
		return build.SigningOptions{}, fmt.Errorf(
			"%s is not a target-files package", s.otaBase)
	}
	return build.SigningOptions{
		Base:     s.otaBase,
		KeyDir:   s.otaKeys,
		ToolsDir: s.otaTools,
		Grow:     s.otaGrow,
		Log:      func(line string) { report(line, -1) },
	}, nil
}
