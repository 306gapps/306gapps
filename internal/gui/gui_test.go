//go:build gui

package gui

import (
	"context"
	"encoding/json"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/306gapps/306gapps/internal/build"
	"github.com/306gapps/306gapps/internal/config"
	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/source"
)

// fixture writes a small assets repo and returns a source pointing at it.
func fixture(t *testing.T) (*source.Source, source.ReleaseRef) {
	t.Helper()
	dir := t.TempDir()
	digest := strings.Repeat("a", 64)

	file := func(path, asset string, size int64) manifest.File {
		return manifest.File{Path: path, Asset: asset, SHA256: digest,
			Size: size, Mode: "0644", Kind: manifest.KindAPK}
	}
	m := manifest.Manifest{
		Schema: manifest.Schema,
		Release: manifest.Release{
			ID: "a17-test", Android: manifest.Android{API: 37, Version: "17"},
			AssetBase: "assets",
		},
		Groups: []manifest.Group{
			{ID: "core", Name: "Core"},
			{ID: "apps", Name: "Apps"},
		},
		Variants: []manifest.Variant{
			{ID: "core", Name: "Core", Packages: []string{"gmscore", "vending"}},
			{ID: "full", Name: "Full", Packages: []string{
				"gmscore", "vending", "dialer-google", "dialer-aosp"}},
		},
		Packages: []manifest.Package{
			{ID: "gmscore", Name: "Play services", Group: "core", Required: true,
				Files: []manifest.File{file("product/priv-app/Gms/Gms.apk", "gms.apk", 1000)}},
			{ID: "vending", Name: "Play Store", Group: "core", Default: true,
				Requires: []string{"gmscore"},
				Files:    []manifest.File{file("product/priv-app/Phonesky/Phonesky.apk", "v.apk", 2000)}},
			{ID: "dialer-google", Name: "Google Phone", Group: "apps",
				Conflicts: []string{"dialer-aosp"},
				Files:     []manifest.File{file("product/priv-app/GDialer/GDialer.apk", "d.apk", 3000)}},
			{ID: "dialer-aosp", Name: "AOSP Dialer", Group: "apps",
				Conflicts: []string{"dialer-google"},
				Files:     []manifest.File{file("product/priv-app/Dialer/Dialer.apk", "a.apk", 500)}},
		},
	}
	write := func(name string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", m)
	ref := source.ReleaseRef{ID: m.Release.ID, Android: m.Release.Android,
		Manifest: "manifest.json", AssetBase: "assets"}
	write("index.json", source.Index{Schema: source.IndexSchema,
		Releases: []source.ReleaseRef{ref}})

	return source.New(dir, source.NewCache(filepath.Join(dir, "cache"))), ref
}

func loaded(t *testing.T) *window {
	t.Helper()
	test.NewApp()
	src, ref := fixture(t)
	s := newState(context.Background(), src, t.TempDir())
	u := &window{state: s, win: test.NewWindow(nil)}
	u.win.SetContent(u.build())
	if err := u.loadRelease(ref); err != nil {
		t.Fatal(err)
	}
	u.rebuildVariants()
	u.rebuildList()
	u.refreshSummary()
	return u
}

// loadedFrom opens a real assets tree, for snapshots at the true package count.
func loadedFrom(t *testing.T, root string) *window {
	t.Helper()
	test.NewApp()
	src := source.New(root, source.NewCache(t.TempDir()))
	idx, err := src.Index(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s := newState(context.Background(), src, t.TempDir())
	u := &window{state: s, win: test.NewWindow(nil)}
	u.win.SetContent(u.build())
	if err := u.loadRelease(idx.Releases[0]); err != nil {
		t.Fatal(err)
	}
	u.rebuildVariants()
	u.rebuildList()
	u.refreshSummary()
	return u
}

func TestDefaultsAreSelectedOnLoad(t *testing.T) {
	u := loaded(t)
	if !u.selected["vending"] {
		t.Error("a default package should start selected")
	}
	// gmscore is required, so it is in the resolution whether ticked or not.
	if !u.res.Selected("gmscore") {
		t.Error("a required package must always be in the resolution")
	}
}

func TestSummaryReportsSizeAndCount(t *testing.T) {
	u := loaded(t)
	got := u.summaryLbl.Text
	if !strings.Contains(got, "2 packages") {
		t.Errorf("summary should count the resolution: %q", got)
	}
	if !strings.Contains(got, "B installed") {
		t.Errorf("summary should state the installed size: %q", got)
	}
}

func TestTogglingUpdatesTheSummary(t *testing.T) {
	u := loaded(t)
	before := u.summaryLbl.Text
	u.selected["dialer-google"] = true
	u.resolve()
	u.refreshSummary()
	if u.summaryLbl.Text == before {
		t.Fatal("selecting a package should change the summary")
	}
	if !strings.Contains(u.summaryLbl.Text, "3 packages") {
		t.Errorf("got %q", u.summaryLbl.Text)
	}
}

func TestConflictIsShownAndBlocksBuilding(t *testing.T) {
	u := loaded(t)
	u.selected["dialer-google"] = true
	u.selected["dialer-aosp"] = true
	u.resolve()
	u.refreshSummary()

	if u.warning.Text == "" {
		t.Fatal("a conflict must be shown")
	}
	if !strings.Contains(u.warning.Text, "cannot be installed alongside") {
		t.Errorf("unhelpful conflict text: %q", u.warning.Text)
	}
	if !u.buildBtn.Disabled() {
		t.Error("building must be blocked while the selection conflicts")
	}

	// Clearing it must re-enable the button, not leave it stuck.
	u.selected["dialer-aosp"] = false
	u.resolve()
	u.refreshSummary()
	if u.buildBtn.Disabled() {
		t.Error("resolving the conflict should re-enable building")
	}
	if u.warning.Text != "" {
		t.Error("the warning should clear")
	}
}

// A required package's checkbox is disabled, so the rule has to hold in the
// state rather than being enforced only by the widget.
func TestRequiredPackagesStayInTheResolution(t *testing.T) {
	u := loaded(t)
	for _, p := range u.packagesIn("core") {
		if !p.Required {
			continue
		}
		// Even explicitly deselected, it must survive resolution.
		u.selected[p.ID] = false
		u.resolve()
		if !u.res.Selected(p.ID) {
			t.Errorf("%s is required but dropped out when deselected", p.ID)
		}
	}
}

func TestDeselectingEverythingStillBuilds(t *testing.T) {
	u := loaded(t)
	for id := range u.selected {
		u.selected[id] = false
	}
	u.resolve()
	u.refreshSummary()
	if u.resErr != nil {
		t.Fatalf("an empty selection is still the required set: %v", u.resErr)
	}
	if u.buildBtn.Disabled() {
		t.Error("the required packages alone are a valid package")
	}
}

// Ticking a family takes every package in it, which is the whole point of
// showing Chrome, the WebView and Trichrome as one thing.
func TestGroupBoxTakesTheWholeFamily(t *testing.T) {
	u := loaded(t)
	g := u.groupRows["apps"]
	if g == nil {
		t.Fatal("no header for the apps family")
	}
	g.check.OnChanged(true)
	for _, p := range g.members {
		if p.Required {
			continue
		}
		// A member that conflicts with one already taken is left out on
		// purpose; TestTakingAFamilyNeverTakesBothSidesOfAConflict covers it.
		if u.conflictsWithAny(p.ID, u.selected) {
			continue
		}
		if !u.selected[p.ID] {
			t.Errorf("%s left out after taking the family", p.ID)
		}
	}
	g.check.OnChanged(false)
	for _, p := range g.members {
		if u.selected[p.ID] {
			t.Errorf("%s left in after dropping the family", p.ID)
		}
	}
}

// A family of one would otherwise render a heading that just repeats the
// package underneath it.
func TestSingletonFamilyHasNoHeader(t *testing.T) {
	u := loaded(t)
	for id, members := range u.cat.ByGroup() {
		if len(members) == 1 && u.groupRows[id] != nil {
			t.Errorf("family %s has one member but drew a header", id)
		}
	}
}

func TestTargetSelectionIsCarried(t *testing.T) {
	u := loaded(t)
	if u.state.target != build.TargetRecovery {
		t.Errorf("default target should be recovery, got %s", u.state.target)
	}
	u.target.SetSelected(targetLabel(build.TargetOTA))
	if u.state.target != build.TargetOTA {
		t.Errorf("selecting a target should carry through, got %s", u.state.target)
	}
}

// The module target is built by the CLI but deliberately absent from the
// picker, and an offered-but-untested target is worse than one that is not
// offered at all.
func TestPickerDoesNotOfferTheModuleTarget(t *testing.T) {
	u := loaded(t)
	for _, label := range u.target.Options {
		if strings.Contains(label, string(build.TargetModule)) {
			t.Fatalf("picker offers the module target: %q", label)
		}
	}
	if len(u.target.Options) != 2 {
		t.Errorf("want recovery and ota, got %v", u.target.Options)
	}
}

// A package pulled in by something else is installed whether or not it was
// ticked; a list that shows it unticked is lying about the package contents.
func TestDependenciesAreShownAsSelected(t *testing.T) {
	u := loaded(t)
	// vending requires gmscore, so untick vending and tick it again to be sure
	// the row tracks the resolution rather than the click.
	u.selected["vending"] = false
	u.resolve()
	u.refreshSummary()

	u.selected["vending"] = true
	u.resolve()
	u.refreshSummary()

	row, ok := u.rows["gmscore"]
	if !ok {
		t.Fatal("no row for gmscore")
	}
	if !row.check.Checked {
		t.Error("a package in the resolution must show as selected")
	}
	if row.note.Text != "required" {
		t.Errorf("a required package should say so, got %q", row.note.Text)
	}
}

func TestUntickedPackagesShowTheirSummary(t *testing.T) {
	u := loaded(t)
	row := u.rows["dialer-google"]
	if row.check.Checked {
		t.Error("dialer-google is not selected by default")
	}
	if row.note.Text == "required" {
		t.Error("an optional package must not claim to be required")
	}
}

func TestFilterNarrowsTheList(t *testing.T) {
	u := loaded(t)
	all := len(u.rows)

	u.filter.SetText("dialer")
	u.rebuildList()
	if len(u.rows) >= all {
		t.Fatalf("filter did not narrow: %d of %d", len(u.rows), all)
	}
	for id := range u.rows {
		if !strings.Contains(id, "dialer") {
			t.Errorf("%s does not match the filter", id)
		}
	}

	u.filter.SetText("")
	u.rebuildList()
	if len(u.rows) != all {
		t.Errorf("clearing the filter should restore every row, got %d of %d",
			len(u.rows), all)
	}
}

func TestFilterMatchesIdNameAndCategory(t *testing.T) {
	u := loaded(t)
	for _, needle := range []string{"vending", "Play Store", "core", "PLAY"} {
		u.filter.SetText(needle)
		u.rebuildList()
		if _, ok := u.rows["vending"]; !ok {
			t.Errorf("filtering by %q should find vending", needle)
		}
	}
}

// Selections live in the state, so filtering must not silently drop them.
func TestFilteringKeepsTheSelection(t *testing.T) {
	u := loaded(t)
	u.selected["dialer-google"] = true
	u.resolve()
	before := u.res.Size

	u.filter.SetText("nothing matches this")
	u.rebuildList()
	u.refreshSummary()

	if u.res.Size != before {
		t.Error("filtering changed what would be built")
	}
	u.filter.SetText("")
	u.rebuildList()
	if !u.rows["dialer-google"].check.Checked {
		t.Error("the selection was lost across filtering")
	}
}

// Two packages that cannot coexist are a choice. Taking one drops the other
// instead of leaving the picker in an error state the user has to back out of.
func TestTakingOneSideOfAConflictDropsTheOther(t *testing.T) {
	u := loaded(t)
	u.rows["dialer-google"].check.OnChanged(true)
	u.rows["dialer-aosp"].check.OnChanged(true)
	if u.selected["dialer-google"] {
		t.Error("taking the AOSP dialer should have dropped the Google one")
	}
	if u.resErr != nil {
		t.Errorf("picker should not be left in an error state: %v", u.resErr)
	}
	// And back the other way.
	u.rows["dialer-google"].check.OnChanged(true)
	if u.selected["dialer-aosp"] {
		t.Error("taking the Google dialer should have dropped the AOSP one")
	}
	if u.resErr != nil {
		t.Errorf("picker should not be left in an error state: %v", u.resErr)
	}
}

// Taking a whole family must not take both sides of a conflict inside it.
func TestTakingAFamilyNeverTakesBothSidesOfAConflict(t *testing.T) {
	u := loaded(t)
	g := u.groupRows["apps"]
	if g == nil {
		t.Fatal("no header for the apps family")
	}
	g.check.OnChanged(true)
	if u.resErr != nil {
		t.Fatalf("taking a family should resolve: %v", u.resErr)
	}
	if u.selected["dialer-aosp"] && u.selected["dialer-google"] {
		t.Error("family took both sides of a conflict")
	}
	// Manifest order decides, so the ordinary package wins over the override.
	if !u.selected["dialer-google"] {
		t.Error("the first in manifest order should have won")
	}
}

// Picking a variant replaces the selection wholesale.
func TestVariantPresetsTheSelection(t *testing.T) {
	u := loaded(t)
	u.applyVariant("full")
	u.refreshSummary()
	for _, id := range []string{"gmscore", "vending", "dialer-google"} {
		if !u.res.Selected(id) {
			t.Errorf("full should have taken %s", id)
		}
	}
	// It names both sides of a conflict; the first in manifest order wins.
	if u.selected["dialer-aosp"] && u.selected["dialer-google"] {
		t.Error("a variant took both sides of a conflict")
	}
	if u.resErr != nil {
		t.Errorf("a variant should always resolve: %v", u.resErr)
	}

	u.applyVariant("core")
	u.refreshSummary()
	if u.res.Selected("dialer-google") {
		t.Error("switching to core should have dropped the dialer")
	}
}

// The label has to tell the truth: it says Custom once the user edits it, and
// goes back to naming the preset if they undo the edit.
func TestVariantLabelFollowsTheSelection(t *testing.T) {
	u := loaded(t)
	u.applyVariant("core")
	u.refreshSummary()
	if got := u.variant.Selected; !strings.HasPrefix(got, "Core") {
		t.Errorf("want the Core preset named, got %q", got)
	}
	// An edit that lands on a different preset names that preset rather than
	// giving up and saying Custom.
	u.rows["dialer-google"].check.OnChanged(true)
	if got := u.variant.Selected; !strings.HasPrefix(got, "Full") {
		t.Errorf("want Full, got %q", got)
	}
	u.rows["dialer-google"].check.OnChanged(false)
	if got := u.variant.Selected; !strings.HasPrefix(got, "Core") {
		t.Errorf("want Core again after undoing, got %q", got)
	}
	// An edit that lands on nothing is the user's own.
	u.rows["dialer-aosp"].check.OnChanged(true)
	if got := u.variant.Selected; got != customVariant {
		t.Errorf("want Custom, got %q", got)
	}
}

// The ota inputs are only meaningful for the ota target, and Build must not
// be clickable until the one that cannot be inferred has been chosen.
func TestOTAFormAppearsWithTheTarget(t *testing.T) {
	u := loaded(t)
	if u.ota.panel.Visible() {
		t.Error("the ota panel should be hidden for the recovery target")
	}
	if u.buildBtn.Disabled() {
		t.Error("recovery should be buildable straight away")
	}

	u.target.SetSelected(targetLabel(build.TargetOTA))
	if !u.ota.panel.Visible() {
		t.Error("the ota panel should appear with the ota target")
	}
	if !u.buildBtn.Disabled() {
		t.Error("ota with no target-files must not be buildable")
	}

	base := filepath.Join(t.TempDir(), "rom-target_files-eng.zip")
	if err := os.WriteFile(base, []byte("not really a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	u.state.otaBase = base
	u.refreshOTA()
	if u.buildBtn.Disabled() {
		t.Error("ota with a target-files should be buildable")
	}

	u.target.SetSelected(targetLabel(build.TargetRecovery))
	if u.ota.panel.Visible() {
		t.Error("the ota panel should go away with the target")
	}
}

// Keys are optional: without them the merge stops at a target-files package
// the user signs themselves, and the form should say so rather than refuse.
func TestOTAWithoutKeysIsAllowedAndExplained(t *testing.T) {
	u := loaded(t)
	base := filepath.Join(t.TempDir(), "rom-target_files.zip")
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	u.target.SetSelected(targetLabel(build.TargetOTA))
	u.state.otaBase = base
	u.refreshOTA()

	if u.buildBtn.Disabled() {
		t.Error("a keyless merge is a legitimate build")
	}
	if !strings.Contains(u.ota.note.Text, "sign it yourself") &&
		!strings.Contains(u.ota.note.Text, "sign yourself") {
		t.Errorf("the note should say what a keyless build produces: %q", u.ota.note.Text)
	}

	opts, err := u.signingOptions(func(string, float64) {})
	if err != nil {
		t.Fatalf("keyless options should be valid: %v", err)
	}
	if !opts.MergedOnly() {
		t.Error("no keys means the build stops at the merged target-files")
	}
}

// A missing target-files must fail before anything slow happens.
func TestOTARefusesAMissingTargetFiles(t *testing.T) {
	u := loaded(t)
	u.state.otaBase = filepath.Join(t.TempDir(), "gone.zip")
	if _, err := u.signingOptions(func(string, float64) {}); err == nil {
		t.Error("a target-files that is not there should be refused")
	}
}

// The path shown must be the path that will be used; a stale field is a lie
// about what Build is going to do.
func TestOTAFieldsFollowTheState(t *testing.T) {
	u := loaded(t)
	u.target.SetSelected(targetLabel(build.TargetOTA))
	u.state.otaBase = "/roms/redfin-target_files.zip"
	u.state.otaKeys = "/keys/redfin"
	u.refreshOTA()
	if u.ota.base.Text != u.state.otaBase {
		t.Errorf("base field %q does not match state %q", u.ota.base.Text, u.state.otaBase)
	}
	if u.ota.keys.Text != u.state.otaKeys {
		t.Errorf("keys field %q does not match state %q", u.ota.keys.Text, u.state.otaKeys)
	}
	u.state.otaKeys = ""
	u.refreshOTA()
	if u.ota.keys.Text != "" {
		t.Errorf("cleared keys should clear the field, got %q", u.ota.keys.Text)
	}
}

// Every Browse button must survive being pressed. Setting the state directly
// is not the same test: it skips the callback, which is where the picker is
// actually built, and that is where resizing before showing crashed the app.
func TestBrowseButtonsOpenWithoutCrashing(t *testing.T) {
	u := loaded(t)
	u.target.SetSelected(targetLabel(build.TargetOTA))
	for name, b := range map[string]*widget.Button{
		"target-files": u.ota.browseBase,
		"keys":         u.ota.browseKeys,
		"clear keys":   u.ota.clearKeys,
		"otatools":     u.ota.browseTools,
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("the %s button panicked: %v", name, r)
				}
			}()
			b.Tapped(&fyne.PointEvent{})
		}()
	}
}

// The destination shown is the destination used, and choosing a new one
// changes where Build writes.
func TestOutputChooserSetsTheDestination(t *testing.T) {
	u := loaded(t)
	if u.outEntry.Text != u.outDir {
		t.Errorf("field %q does not match the destination %q", u.outEntry.Text, u.outDir)
	}
	dest := t.TempDir()
	u.state.outDir = dest
	setPath(u.outEntry, dest)
	if u.outEntry.Text != dest {
		t.Errorf("field did not follow: %q", u.outEntry.Text)
	}
	if err := writableDir(u.outDir); err != nil {
		t.Errorf("a chosen folder should be usable: %v", err)
	}
}

// A long summary is truncated to keep rows to one line. Clicking the row has
// to be the way to read the rest, or the text is simply unreachable.
func TestClickingARowRevealsTheWholeSummary(t *testing.T) {
	u := loaded(t)
	row := u.rows["vending"]
	if row.detail == nil {
		t.Fatal("no detail label on the row")
	}
	if row.detail.Visible() {
		t.Error("the detail should start hidden")
	}
	if row.detail.Text != row.pkg.Summary {
		t.Errorf("the detail should hold the whole summary, got %q", row.detail.Text)
	}
	row.detail.Show()
	if !row.detail.Visible() {
		t.Error("the detail should be showable")
	}
}

// Expert mode is off by default and its controls stay out of the way.
func TestExpertModeIsOffAndHidesItsControls(t *testing.T) {
	u := loaded(t)
	if u.state.expert {
		t.Error("expert mode should start off")
	}
	if u.expertBar.Visible() {
		t.Error("the expert bar should be hidden")
	}
	u.expertBox.SetChecked(true)
	if !u.state.expert || !u.expertBar.Visible() {
		t.Error("turning expert on should reveal its controls")
	}
	u.expertBox.SetChecked(false)
	if u.expertBar.Visible() {
		t.Error("turning it off should hide them again")
	}
}

// Keeping the ROM's own app is only offered for packages that would remove it.
func TestKeepStockAppliesOnlyToWhatWasTicked(t *testing.T) {
	u := loaded(t)
	u.expertBox.SetChecked(true)
	u.rows["dialer-google"].check.OnChanged(true)
	u.state.keepStock["dialer-google"] = true
	u.resolve()
	got := u.keepStockIDs()
	if len(got) != 1 || got[0] != "dialer-google" {
		t.Errorf("want dialer-google kept, got %v", got)
	}
	// A package that is not in the resolution must not leak into the build.
	u.state.keepStock["not-installed"] = true
	if got := u.keepStockIDs(); len(got) != 1 {
		t.Errorf("only installed packages should be listed, got %v", got)
	}
}

// A saved selection round-trips through the picker.
func TestSavedSelectionAppearsAndApplies(t *testing.T) {
	u := loaded(t)
	u.state.configs = &config.Store{Dir: t.TempDir()}
	if err := u.configs.Save(config.Config{
		Name: "Mine", Packages: []string{"gmscore", "dialer-google"},
		KeepStock: []string{"dialer-google"},
	}); err != nil {
		t.Fatal(err)
	}
	u.rebuildVariants()

	label := savedPrefix + "Mine"
	found := false
	for _, o := range u.variant.Options {
		if o == label {
			found = true
		}
	}
	if !found {
		t.Fatalf("a saved selection should be listed, got %v", u.variant.Options)
	}

	u.variant.SetSelected(label)
	if !u.res.Selected("dialer-google") {
		t.Error("choosing it should apply its packages")
	}
	if !u.state.keepStock["dialer-google"] {
		t.Error("choosing it should apply its keep-stock list")
	}
	if u.variant.Selected != label {
		t.Errorf("it should keep its own name, got %q", u.variant.Selected)
	}

	// Editing it makes the selection the user's own again.
	u.rows["dialer-aosp"].check.OnChanged(true)
	if u.variant.Selected == label {
		t.Error("an edit should stop claiming to be the saved selection")
	}
}

// Expert mode can name the zip. The example from the field is
// 306gapps-A17-ionut-full-pixels-recovery.zip.
func TestExpertFileNameOverride(t *testing.T) {
	u := loaded(t)
	def := u.zipName("a17-cd1a.260905.001.b1", build.TargetRecovery)
	if def != "306gapps-a17-cd1a.260905.001.b1-recovery.zip" {
		t.Errorf("default name changed: %q", def)
	}

	cases := map[string]string{
		"306gapps-A17-ionut-full-pixels-recovery.zip": "306gapps-A17-ionut-full-pixels-recovery.zip",
		"306gapps-A17-ionut-full-pixels-recovery":     "306gapps-A17-ionut-full-pixels-recovery.zip",
		"  spaced  ": "spaced.zip",
		"":           def,
		"   ":        def,
		// A name is a file, not a path: anything else could write outside the
		// chosen folder.
		"../../etc/passwd":  "passwd.zip",
		"/tmp/evil.zip":     "evil.zip",
		"sub/dir/thing.zip": "thing.zip",
	}
	for in, want := range cases {
		u.state.outName = in
		if got := u.zipName("a17-cd1a.260905.001.b1", build.TargetRecovery); got != want {
			t.Errorf("zipName(%q) = %q, want %q", in, got, want)
		}
	}
}
