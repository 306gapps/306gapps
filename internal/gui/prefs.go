//go:build gui

package gui

import "fyne.io/fyne/v2"

// Preferences survive a restart. Only the choices that are tedious to redo:
// the picker deliberately does not remember a selection, since a saved config
// is the explicit way to do that.
const (
	prefRelease = "last.release"
	prefExpert  = "last.expert"
	prefOutDir  = "last.outdir"
)

func (u *window) loadPrefs(a fyne.App) {
	p := a.Preferences()
	u.state.expert = p.Bool(prefExpert)
	if d := p.String(prefOutDir); d != "" {
		u.state.outDir = d
	}
}

func (u *window) savePrefs(a fyne.App) {
	p := a.Preferences()
	p.SetBool(prefExpert, u.state.expert)
	p.SetString(prefOutDir, u.state.outDir)
	if u.cat != nil {
		p.SetString(prefRelease, u.cat.Manifest().Release.ID)
	}
}

func (u *window) preferredRelease(a fyne.App) string {
	return a.Preferences().String(prefRelease)
}
