//go:build gui

package gui

import (
	"context"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/306gapps/306gapps/internal/build"
	"github.com/306gapps/306gapps/internal/config"
	"github.com/306gapps/306gapps/internal/desktop"
	"github.com/306gapps/306gapps/internal/gui/icon"
	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/source"
	"github.com/306gapps/306gapps/internal/version"
)

// Run opens the picker.
func Run(ctx context.Context, src *source.Source, outDir string) error {
	s := newState(ctx, src, outDir)

	desktop.Ensure()
	a := app.NewWithID(icon.AppID)
	a.SetIcon(icon.Resource)
	w := a.NewWindow("306gapps " + version.String())
	w.SetIcon(icon.Resource)
	w.Resize(fyne.NewSize(940, 700))

	ui := &window{state: s, win: w, app: a}
	ui.loadPrefs(a)
	w.SetContent(ui.build())
	w.SetCloseIntercept(func() {
		ui.savePrefs(a)
		w.Close()
	})

	// Fetch after the window is up so a slow network shows a window, not nothing.
	go ui.loadIndex()

	w.ShowAndRun()
	return nil
}

type window struct {
	*state
	win fyne.Window
	app fyne.App

	releases *widget.Select

	variant    *widget.Select
	filter     *widget.Entry
	list       *fyne.Container
	summaryLbl *widget.Label
	warning    *widget.Label
	target     *widget.Select
	ota        *otaForm
	expertBox  *widget.Check
	frpBox     *widget.Check
	nameEntry  *widget.Entry
	expertBar  *fyne.Container
	importBtn  *widget.Button
	exportBtn  *widget.Button
	deleteBtn  *widget.Button
	outEntry   *widget.Entry
	verLbl     *widget.Label
	updateLink *widget.Hyperlink
	cacheLbl   *widget.Label
	clearBtn   *widget.Button
	buildBtn   *widget.Button
	status     *widget.Label

	variantIDs map[string]string
	// savedByLabel maps a dropdown entry to the user's own saved selection.
	savedByLabel map[string]config.Config
	// applyingVariant suppresses the select's callback while the label is synced,
	// which would otherwise re-apply the preset and undo the change.
	applyingVariant bool

	refs map[string]source.ReleaseRef
	// rows keeps the per-package widgets so the list can follow the resolution
	// after every toggle, including packages pulled in as dependencies.
	rows map[string]*packageRow
	// groupRows keeps the family boxes; a family shows as taken only while every member is.
	groupRows map[string]*groupRow
}

type groupRow struct {
	check   *widget.Check
	members []manifest.Package
}

// packageRow is one line: the box, plus the note saying why it is ticked.
type packageRow struct {
	check *widget.Check
	note  *widget.Label
	// detail holds the whole summary; expanded is the wrapper that is shown
	// and hidden, since hiding the label alone still reserves its height.
	detail   *widget.Label
	expanded *fyne.Container
	pkg      manifest.Package
}

func (u *window) build() fyne.CanvasObject {
	u.releases = widget.NewSelect(nil, u.onRelease)
	u.releases.PlaceHolder = "loading releases…"

	byLabel := map[string]build.Target{}
	labels := make([]string, 0, len(guiTargets))
	for _, t := range guiTargets {
		byLabel[targetLabel(t)] = t
		labels = append(labels, targetLabel(t))
	}
	u.target = widget.NewSelect(labels, func(label string) {
		if t, ok := byLabel[label]; ok {
			u.state.target = t
		}
		u.refreshOTA()
	})
	u.target.SetSelectedIndex(0)

	u.summaryLbl = widget.NewLabel("")
	u.warning = widget.NewLabel("")
	u.warning.Wrapping = fyne.TextWrapWord
	u.warning.Importance = widget.DangerImportance
	u.status = widget.NewLabel("")

	u.outEntry = pathEntry("")
	setPath(u.outEntry, u.outDir)
	browseOut := widget.NewButton("Browse…", func() {
		u.pickFolder(func(path string) {
			u.state.outDir = path
			setPath(u.outEntry, path)
		})
	})

	u.buildBtn = widget.NewButtonWithIcon("Build", theme.DownloadIcon(), u.onBuild)
	u.buildBtn.Importance = widget.HighImportance
	u.buildBtn.Disable()

	u.variant = widget.NewSelect(nil, func(label string) {
		if u.applyingVariant {
			return
		}
		if c, ok := u.savedByLabel[label]; ok {
			u.state.keptLabel = label
			u.applyConfig(c)
			u.rebuildList()
			u.refreshSummary()
			return
		}
		u.state.keptLabel = ""
		id, ok := u.variantIDs[label]
		if !ok {
			return
		}
		u.applyVariant(id)
		u.rebuildList()
		u.refreshSummary()
	})
	u.variant.PlaceHolder = "choose a starting point…"

	u.filter = widget.NewEntry()
	u.filter.SetPlaceHolder("Filter by name, id or family")
	u.filter.OnChanged = func(string) { u.rebuildList(); u.refreshSummary() }
	clear := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		u.filter.SetText("")
	})

	u.expertBox = widget.NewCheck("Expert", func(on bool) {
		u.state.expert = on
		u.rebuildList()
		u.refreshExpert()
		u.refreshSummary()
	})

	u.importBtn = widget.NewButton("Import…", u.onImport)
	u.exportBtn = widget.NewButton("Export…", u.onExport)
	u.deleteBtn = widget.NewButton("Delete", u.onDelete)
	u.deleteBtn.Importance = widget.DangerImportance

	u.nameEntry = widget.NewEntry()
	u.nameEntry.SetPlaceHolder("306gapps-<release>-<format>.zip")
	u.nameEntry.OnChanged = func(v string) {
		u.state.outName = v
		u.refreshSummary()
	}

	u.frpBox = widget.NewCheck("Also clear factory reset protection (recovery zip)", func(on bool) {
		u.state.wipeFRP = on
	})

	u.expertBar = container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("File name"), nil, u.nameEntry),
		container.NewBorder(nil, nil, widget.NewLabel("Saved selections"),
			container.NewHBox(u.importBtn, u.exportBtn, u.deleteBtn), layout.NewSpacer()),
		u.frpBox,
	)
	u.expertBar.Hide()

	u.list = container.NewVBox()

	top := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("Release"), nil, u.releases),
		container.NewBorder(nil, nil, widget.NewLabel("Variant"), nil, u.variant),
		container.NewBorder(nil, nil, widget.NewLabel("Filter"),
			container.NewHBox(clear, u.expertBox), u.filter),
		u.expertBar,
		widget.NewSeparator(),
	)
	u.ota = u.buildOTAForm()

	bottom := container.NewVBox(
		u.ota.panel,
		widget.NewSeparator(),
		u.warning,
		u.summaryLbl,
		container.NewBorder(nil, nil,
			widget.NewLabel("Save to"), browseOut, u.outEntry),
		container.NewBorder(nil, nil,
			widget.NewLabel("Format"), u.buildBtn, u.target),
		u.status,
		widget.NewSeparator(),
		u.footer(),
	)
	return container.NewBorder(top, bottom, nil, nil,
		container.NewVScroll(u.list))
}

// guiTargets is what the picker offers; the Magisk/KernelSU module stays CLI-only until it is better tested.
var guiTargets = []build.Target{build.TargetRecovery, build.TargetOTA}

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
	// The release chosen last time, if it is still published.
	want := ""
	if u.app != nil {
		want = u.preferredRelease(u.app)
	}
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
			pick := labels[0]
			for label, ref := range u.refs {
				if ref.ID == want {
					pick = label
					break
				}
			}
			u.releases.SetSelected(pick)
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
			u.rebuildVariants()
			u.rebuildList()
			u.refreshSummary()
		})
	}()
}

const customVariant = "Custom"

func (u *window) rebuildVariants() {
	u.variantIDs = map[string]string{}
	u.savedByLabel = map[string]config.Config{}
	labels := []string{customVariant}
	for _, v := range u.variants() {
		label := fmt.Sprintf("%s — %d packages", v.Name, len(u.cat.Prune(v.Packages)))
		u.variantIDs[label] = v.ID
		labels = append(labels, label)
	}
	saved, err := u.configs.List()
	if err == nil {
		for _, c := range saved {
			label := savedPrefix + c.Name
			u.savedByLabel[label] = c
			labels = append(labels, label)
		}
	}
	u.variant.Options = labels
	u.variant.Refresh()
}

// refreshVariant points the select at whichever preset matches the selection, or Custom.
func (u *window) refreshVariant() {
	if u.variant == nil || len(u.variantIDs) == 0 {
		return
	}
	want := customVariant
	if u.state.keptLabel != "" {
		want = u.state.keptLabel
	} else if id := u.matchingVariant(); id != "" {
		for label, vid := range u.variantIDs {
			if vid == id {
				want = label
				break
			}
		}
	}
	if u.variant.Selected == want {
		return
	}
	u.applyingVariant = true
	u.variant.SetSelected(want)
	u.applyingVariant = false
}

// rebuildList redraws the whole package list, which only the release changing needs.
func (u *window) rebuildList() {
	u.list.RemoveAll()
	u.rows = map[string]*packageRow{}
	u.groupRows = map[string]*groupRow{}

	needle := ""
	if u.filter != nil {
		needle = strings.ToLower(strings.TrimSpace(u.filter.Text))
	}

	shown := 0
	for _, g := range u.groups() {
		members := u.packagesIn(g.ID)
		var rows []fyne.CanvasObject
		var matched []manifest.Package
		for _, p := range members {
			if !matchesFilter(p, g.Name, needle) {
				continue
			}
			matched = append(matched, p)
		}
		if len(matched) == 0 {
			continue
		}
		// A heading above a single package just repeats its name.
		if len(members) > 1 {
			rows = append(rows, u.groupHeader(g, members))
			for _, p := range matched {
				rows = append(rows, indent(u.packageRow(p)))
			}
		} else {
			rows = append(rows, u.packageRow(matched[0]))
		}
		shown += len(matched)
		for _, r := range rows {
			u.list.Add(r)
		}
		u.list.Add(widget.NewSeparator())
	}

	if shown == 0 && needle != "" {
		empty := widget.NewLabel("Nothing matches " + needle)
		empty.Importance = widget.LowImportance
		u.list.Add(empty)
	}

	u.list.Refresh()
	u.refreshBuildButton()
}

// matchesFilter is a substring test over the name, id, group and summary.
func matchesFilter(p manifest.Package, group, needle string) bool {
	if needle == "" {
		return true
	}
	for _, field := range []string{p.Name, p.ID, group, p.Summary} {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
}

// groupHeader is the line above a family; its box takes or drops the whole family.
func (u *window) groupHeader(g manifest.Group, members []manifest.Package) fyne.CanvasObject {
	all := widget.NewCheck("", nil)
	all.SetChecked(u.allSelectedIn(members))
	all.OnChanged = u.groupToggle(members)

	name := widget.NewLabelWithStyle(g.Name, fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true})

	var total int64
	for _, p := range members {
		total += p.Size()
	}
	size := widget.NewLabel(humanSize(total))
	size.Alignment = fyne.TextAlignTrailing

	note := widget.NewLabel(g.Summary)
	note.Wrapping = fyne.TextWrapOff
	note.Truncation = fyne.TextTruncateEllipsis
	note.Importance = widget.LowImportance

	u.groupRows[g.ID] = &groupRow{check: all, members: members}

	return container.NewBorder(nil, nil,
		container.NewHBox(all, name), size, note)
}

func (u *window) groupToggle(members []manifest.Package) func(bool) {
	return func(on bool) {
		u.state.keptLabel = ""
		taken := map[string]bool{}
		for _, p := range members {
			if p.Required {
				continue
			}
			// A family cannot take both sides of a conflict; first in manifest order wins.
			if on && u.conflictsWithAny(p.ID, taken) {
				u.selected[p.ID] = false
				continue
			}
			u.selected[p.ID] = on
			taken[p.ID] = on
		}
		u.resolve()
		u.refreshSummary()
	}
}

func (u *window) conflictsWithAny(id string, taken map[string]bool) bool {
	for _, other := range u.cat.ConflictsWith(id) {
		if taken[other] {
			return true
		}
	}
	return false
}

// allSelectedIn reports whether every selectable package in the family is in.
func (u *window) allSelectedIn(members []manifest.Package) bool {
	for _, p := range members {
		if p.Required {
			continue
		}
		if u.res != nil {
			if !u.res.Selected(p.ID) {
				return false
			}
			continue
		}
		if !u.selected[p.ID] {
			return false
		}
	}
	return true
}

func indent(o fyne.CanvasObject) *fyne.Container {
	pad := widget.NewLabel("  ")
	return container.NewBorder(nil, nil, pad, nil, o)
}

func (u *window) packageRow(p manifest.Package) fyne.CanvasObject {
	check := widget.NewCheck("", nil)
	if p.Required {
		check.SetChecked(true)
		check.Disable()
	} else {
		check.SetChecked(u.selected[p.ID])
	}
	check.OnChanged = func(on bool) {
		if p.Required {
			return
		}
		u.selected[p.ID] = on
		u.state.keptLabel = ""
		// A conflict is a choice, not an error: taking one drops the other.
		if on {
			for _, other := range u.cat.ConflictsWith(p.ID) {
				u.selected[other] = false
			}
		}
		u.resolve()
		u.refreshSummary()
	}

	name := widget.NewLabel(p.Name)
	id := widget.NewLabel(p.ID)
	id.Importance = widget.LowImportance
	var nameCell fyne.CanvasObject = name
	if p.Experimental {
		tag := widget.NewLabel("experimental")
		tag.Importance = widget.WarningImportance
		nameCell = container.NewHBox(name, tag)
	}
	size := widget.NewLabel(humanSize(p.Size()))
	size.Alignment = fyne.TextAlignTrailing

	// Truncated to share the row; a paragraph per package ran to several screens.
	note := widget.NewLabel("")
	note.Wrapping = fyne.TextWrapOff
	note.Truncation = fyne.TextTruncateEllipsis
	note.Importance = widget.LowImportance

	// Full text lives in a second line that starts hidden; clicking the name unfolds it.
	detail := widget.NewLabel(p.Summary)
	detail.Wrapping = fyne.TextWrapWord
	detail.Importance = widget.LowImportance

	// Hide the wrapper, not the label: a visible wrapper round a hidden label
	// still reserves a row's worth of height.
	expanded := indent(detail)
	expanded.Hide()

	// Expert-only: keeping both the ROM's app and ours is rarely what anyone wants.
	var trailing fyne.CanvasObject = size
	if u.state.expert && replaces(p) {
		keep := widget.NewCheck("keep ROM app", func(on bool) {
			u.state.keepStock[p.ID] = on
			u.refreshSummary()
		})
		keep.SetChecked(u.state.keepStock[p.ID])
		trailing = container.NewHBox(keep, size)
	}

	line := container.NewBorder(nil, nil,
		container.NewHBox(check, newTappable(nameCell, func() {
			if p.Summary == "" {
				return
			}
			if expanded.Visible() {
				expanded.Hide()
			} else {
				expanded.Show()
			}
			u.list.Refresh()
		}), id), trailing, note)

	u.rows[p.ID] = &packageRow{check: check, note: note,
		detail: detail, expanded: expanded, pkg: p}

	return container.NewVBox(line, expanded)
}

// refreshRows brings every line back in line with the resolution.
func (u *window) refreshRows() {
	if u.res == nil {
		return
	}
	implied := u.implied()
	for _, g := range u.groupRows {
		if want := u.allSelectedIn(g.members); g.check.Checked != want {
			g.check.OnChanged = nil
			g.check.SetChecked(want)
			g.check.OnChanged = u.groupToggle(g.members)
		}
	}
	for id, row := range u.rows {
		want := u.res.Selected(id)
		if row.check.Checked != want {
			// Assign the field rather than SetChecked, which would re-enter OnChanged.
			row.check.Checked = want
			row.check.Refresh()
		}

		var note string
		switch {
		case row.pkg.Required:
			note = "required"
		case implied[id]:
			note = "added as a dependency of " + strings.Join(u.res.Implied[id], ", ")
		default:
			// One whole sentence, not however much happens to fit before the
			// label clips. The rest is a click away.
			note, more := firstSentence(row.pkg.Summary)
			if more {
				note += " …"
			}
			row.note.SetText(note)
			continue
		}
		row.note.SetText(note)
	}
}

// refreshExpert shows the expert-only controls.
func (u *window) refreshExpert() {
	if u.expertBar == nil {
		return
	}
	if u.state.expert {
		u.expertBar.Show()
	} else {
		u.expertBar.Hide()
	}
}

func (u *window) refreshSummary() {
	u.refreshRows()
	u.refreshVariant()
	u.summaryLbl.SetText(u.state.summary())
	if u.resErr != nil {
		u.warning.SetText(u.resErr.Error())
	} else {
		u.warning.SetText("")
	}
	u.refreshBuildButton()
	u.summaryLbl.Refresh()
	u.warning.Refresh()
}
