// Package tui is the interactive package picker.
package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/306gapps/306gapps/internal/build"
	"github.com/306gapps/306gapps/internal/catalog"
	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/presets"
	"github.com/306gapps/306gapps/internal/source"
	"github.com/306gapps/306gapps/internal/stage"
)

type step int

const (
	stepLoading step = iota
	stepRelease
	stepPackages
	stepTarget
	stepBuilding
	stepDone
	stepError
)

// row is one line in the package picker: either a group heading or a package.
type row struct {
	group string
	label string
	pkg   *manifest.Package
}

// Model drives the whole wizard.
type Model struct {
	ctx    context.Context
	src    *source.Source
	outDir string
	// busybox is resolved once the release is known.
	busybox string

	step step
	err  error

	index   *source.Index
	refs    []source.ReleaseRef
	refIdx  int
	release source.ReleaseRef

	cat      *catalog.Catalog
	rows     []row
	cursor   int
	selected map[string]bool
	res      *catalog.Resolution
	resErr   error

	targets   []build.Target
	targetIdx int

	// progress counters, written from the build goroutine.
	fetched  atomic.Int64
	total    atomic.Int64
	written  atomic.Int64
	toWrite  atomic.Int64
	phase    atomic.Value
	result   *build.Result
	width    int
	quitting bool
}

func New(ctx context.Context, src *source.Source, outDir string) *Model {
	m := &Model{
		ctx: ctx, src: src, outDir: outDir,
		step: stepLoading, selected: map[string]bool{},
		targets: build.Targets, width: 80,
	}
	m.phase.Store("")
	return m
}

func (m *Model) Init() tea.Cmd { return m.loadIndex }

// ---- messages --------------------------------------------------------------

type indexMsg struct{ index *source.Index }
type manifestMsg struct{ m *manifest.Manifest }
type builtMsg struct{ res *build.Result }
type errMsg struct{ err error }
type tickMsg struct{}

func (m *Model) loadIndex() tea.Msg {
	idx, err := m.src.Index(m.ctx)
	if err != nil {
		return errMsg{err}
	}
	return indexMsg{idx}
}

func (m *Model) loadManifest() tea.Msg {
	mf, err := m.src.Manifest(m.ctx, m.release)
	if err != nil {
		return errMsg{err}
	}
	return manifestMsg{mf}
}

// ---- update ----------------------------------------------------------------

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case errMsg:
		m.err = msg.err
		m.step = stepError
		return m, nil

	case indexMsg:
		m.index = msg.index
		m.refs = append([]source.ReleaseRef(nil), msg.index.Releases...)
		sort.Slice(m.refs, func(i, j int) bool {
			if m.refs[i].Android.API != m.refs[j].Android.API {
				return m.refs[i].Android.API > m.refs[j].Android.API
			}
			return m.refs[i].Created.After(m.refs[j].Created)
		})
		m.step = stepRelease
		return m, nil

	case manifestMsg:
		m.cat = catalog.New(msg.m)
		presets.Apply(m.ctx, m.src, m.cat)
		m.buildRows()
		for _, id := range m.cat.Defaults() {
			m.selected[id] = true
		}
		m.resolve()
		m.step = stepPackages
		m.cursor = m.firstPackageRow()
		return m, nil

	case builtMsg:
		m.result = msg.res
		m.step = stepDone
		return m, nil

	case tickMsg:
		if m.step == stepBuilding {
			return m, tick()
		}
		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m *Model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c", "q":
		if m.step != stepBuilding {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	switch m.step {
	case stepRelease:
		switch k.String() {
		case "up", "k":
			m.refIdx = max(0, m.refIdx-1)
		case "down", "j":
			m.refIdx = min(len(m.refs)-1, m.refIdx+1)
		case "enter":
			m.release = m.refs[m.refIdx]
			m.step = stepLoading
			return m, m.loadManifest
		}

	case stepPackages:
		switch k.String() {
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case " ", "x":
			m.toggle()
		case "a":
			m.setGroup(true)
		case "n":
			m.setGroup(false)
		case "r":
			m.selected = map[string]bool{}
			for _, id := range m.cat.Defaults() {
				m.selected[id] = true
			}
			m.resolve()
		case "enter":
			if m.resErr == nil {
				m.step = stepTarget
			}
		case "esc":
			m.step = stepRelease
		}

	case stepTarget:
		switch k.String() {
		case "up", "k":
			m.targetIdx = max(0, m.targetIdx-1)
		case "down", "j":
			m.targetIdx = min(len(m.targets)-1, m.targetIdx+1)
		case "esc":
			m.step = stepPackages
		case "enter":
			m.step = stepBuilding
			return m, tea.Batch(m.runBuild, tick())
		}

	case stepDone, stepError:
		if k.String() == "enter" {
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func tick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

// ---- selection -------------------------------------------------------------

func (m *Model) buildRows() {
	m.rows = nil
	members := m.cat.ByGroup()
	for _, g := range m.cat.Groups() {
		ps := members[g.ID]
		// A family of one needs no heading; the package row says it all.
		if len(ps) > 1 {
			m.rows = append(m.rows, row{group: g.ID, label: g.Name})
		}
		for i := range ps {
			p := ps[i]
			m.rows = append(m.rows, row{group: g.ID, pkg: &p})
		}
	}
}

func (m *Model) firstPackageRow() int {
	for i, r := range m.rows {
		if r.pkg != nil {
			return i
		}
	}
	return 0
}

func (m *Model) move(d int) {
	for i := m.cursor + d; i >= 0 && i < len(m.rows); i += d {
		if m.rows[i].pkg != nil {
			m.cursor = i
			return
		}
	}
}

func (m *Model) toggle() {
	r := m.rows[m.cursor]
	if r.pkg == nil || r.pkg.Required {
		return
	}
	on := !m.selected[r.pkg.ID]
	m.selected[r.pkg.ID] = on
	// A conflict is a choice, not an error: taking one drops the other.
	if on {
		for _, other := range m.cat.ConflictsWith(r.pkg.ID) {
			m.selected[other] = false
		}
	}
	m.resolve()
}

func (m *Model) setGroup(on bool) {
	cat := m.rows[m.cursor].group
	taken := map[string]bool{}
	for _, r := range m.rows {
		if r.pkg == nil || r.group != cat || r.pkg.Required {
			continue
		}
		if on && m.conflictsWithAny(r.pkg.ID, taken) {
			m.selected[r.pkg.ID] = false
			continue
		}
		m.selected[r.pkg.ID] = on
		taken[r.pkg.ID] = on
	}
	m.resolve()
}

func (m *Model) conflictsWithAny(id string, taken map[string]bool) bool {
	for _, other := range m.cat.ConflictsWith(id) {
		if taken[other] {
			return true
		}
	}
	return false
}

func (m *Model) selection() []string {
	var out []string
	for id, on := range m.selected {
		if on {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func (m *Model) resolve() {
	m.res, m.resErr = m.cat.Resolve(m.selection())
}

// ---- build -----------------------------------------------------------------

func (m *Model) runBuild() tea.Msg {
	m.phase.Store("downloading")

	seen := map[string]bool{}
	m.total.Store(int64(len(m.res.Files)))

	plan, err := stage.Build(m.ctx, m.src, m.cat.Manifest(), m.res, stage.Options{
		Workers: 4,
		Progress: func(f manifest.File, done, tot int64) {
			if done >= tot && !seen[f.Path] {
				seen[f.Path] = true
				m.fetched.Add(1)
			}
		},
	})
	if err != nil {
		return errMsg{err}
	}

	if err := plan.Verify(4); err != nil {
		return errMsg{err}
	}

	m.phase.Store("packing")
	m.toWrite.Store(int64(len(plan.Entries)))

	target := m.targets[m.targetIdx]
	if target == build.TargetRecovery && m.index != nil {
		if p, ok := m.index.BusyboxFor(plan.Release.Architecture()); ok {
			if local, err := m.src.FetchPayload(m.ctx, p); err == nil {
				m.busybox = local
			}
		}
	}
	out := filepath.Join(m.outDir, fmt.Sprintf("306gapps-%s-%s.zip", plan.Release.ID, target))
	res, err := build.Build(plan, build.Options{
		Target:   target,
		Out:      out,
		Busybox:  m.busybox,
		Progress: func(done, _ int) { m.written.Store(int64(done)) },
	})
	if err != nil {
		return errMsg{err}
	}
	return builtMsg{res}
}

// ---- view ------------------------------------------------------------------

func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	b.WriteString(title.Render("306gapps") + "\n")

	switch m.step {
	case stepLoading:
		b.WriteString(dim.Render("loading...") + "\n")

	case stepRelease:
		b.WriteString(dim.Render("Select an Android release\n\n"))
		for i, r := range m.refs {
			mark := "  "
			line := fmt.Sprintf("Android %-4s  %s  %s %s", r.Android.Version, r.ID, r.Device, r.Build)
			if i == m.refIdx {
				mark = cursorOn.Render("> ")
				line = cursorOn.Render(line)
			}
			b.WriteString(mark + line + "\n")
		}
		b.WriteString("\n" + help.Render("↑/↓ move · enter select · q quit") + "\n")

	case stepPackages:
		b.WriteString(dim.Render(fmt.Sprintf("%s · Android %s\n\n",
			m.release.ID, m.release.Android.Version)))
		b.WriteString(m.viewPackages())

	case stepTarget:
		b.WriteString(dim.Render("Select a package format\n\n"))
		for i, t := range m.targets {
			mark := "  "
			line := fmt.Sprintf("%-9s %s", t, t.Description())
			if i == m.targetIdx {
				mark = cursorOn.Render("> ")
				line = cursorOn.Render(line)
			}
			b.WriteString(mark + line + "\n")
		}
		b.WriteString("\n" + help.Render("↑/↓ move · enter build · esc back") + "\n")

	case stepBuilding:
		b.WriteString(m.viewBuilding())

	case stepDone:
		b.WriteString(good.Render("Build complete") + "\n\n")
		fmt.Fprintf(&b, "  %s\n", m.result.Path)
		fmt.Fprintf(&b, "  %s · %d entries\n", human(m.result.Size), m.result.Files)
		fmt.Fprintf(&b, "  sha256 %s\n", m.result.SHA256)
		b.WriteString("\n" + help.Render("enter to exit") + "\n")

	case stepError:
		b.WriteString(bad.Render("Error") + "\n\n")
		for _, line := range strings.Split(m.err.Error(), "\n") {
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("\n" + help.Render("enter to exit") + "\n")
	}
	return b.String()
}

func (m *Model) viewPackages() string {
	var b strings.Builder
	implied := map[string]bool{}
	if m.res != nil {
		for id := range m.res.Implied {
			implied[id] = true
		}
	}

	// Keep the cursor in view on short terminals.
	const window = 22
	start := 0
	if m.cursor > window/2 {
		start = m.cursor - window/2
	}
	if start+window > len(m.rows) {
		start = max(0, len(m.rows)-window)
	}

	for i := start; i < len(m.rows) && i < start+window; i++ {
		r := m.rows[i]
		if r.pkg == nil {
			b.WriteString("\n" + category.Render(strings.ToUpper(r.label)) + "\n")
			continue
		}
		box := "[ ]"
		switch {
		case r.pkg.Required:
			box = good.Render("[■]")
		case m.selected[r.pkg.ID]:
			box = "[x]"
		case implied[r.pkg.ID]:
			box = dim.Render("[·]")
		}
		line := fmt.Sprintf("%s %-22s %8s  %s", box, r.pkg.ID, human(r.pkg.Size()), r.pkg.Name)
		prefix := "  "
		if i == m.cursor {
			prefix = cursorOn.Render("> ")
			line = cursorOn.Render(line)
		}
		if r.pkg.Experimental {
			line += " " + warn.Render("(experimental)")
		}
		b.WriteString(prefix + line + "\n")
	}

	b.WriteString("\n")
	if m.resErr != nil {
		for _, line := range strings.Split(m.resErr.Error(), "\n") {
			b.WriteString(bad.Render(line) + "\n")
		}
	} else if m.res != nil {
		fmt.Fprintf(&b, "%s\n", dim.Render(fmt.Sprintf(
			"%d packages · %s installed", len(m.res.Packages), human(m.res.Size))))
		if n := len(m.res.Implied); n > 0 {
			b.WriteString(dim.Render(fmt.Sprintf("[·] %d pulled in as dependencies", n)) + "\n")
		}
	}
	b.WriteString(help.Render("space toggle · a all in section · n none · r reset · enter continue · q quit") + "\n")
	return b.String()
}

func (m *Model) viewBuilding() string {
	var b strings.Builder
	phase, _ := m.phase.Load().(string)
	switch phase {
	case "downloading":
		done, tot := m.fetched.Load(), m.total.Load()
		fmt.Fprintf(&b, "Downloading %d/%d files\n", done, tot)
		b.WriteString(bar(done, tot, 40) + "\n")
	case "packing":
		done, tot := m.written.Load(), m.toWrite.Load()
		fmt.Fprintf(&b, "Packing %d/%d files\n", done, tot)
		b.WriteString(bar(done, tot, 40) + "\n")
	default:
		b.WriteString(dim.Render("starting...") + "\n")
	}
	return b.String()
}

func bar(done, total int64, width int) string {
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	filled := int(int64(width) * done / total)
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + dim.Render(strings.Repeat("░", width-filled))
}

// Run starts the interactive picker.
func Run(ctx context.Context, src *source.Source, outDir string) error {
	p := tea.NewProgram(New(ctx, src, outDir))
	_, err := p.Run()
	return err
}
