//go:build gui

package gui

import (
	"fmt"
	"fyne.io/fyne/v2/container"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/306gapps/306gapps/internal/build"
	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/sign"
	"github.com/306gapps/306gapps/internal/stage"
)

// onBuild downloads whatever the selection needs and writes the package.
func (u *window) onBuild() {
	if u.res == nil || u.resErr != nil {
		return
	}
	bar := widget.NewProgressBar()
	status := widget.NewLabel("Starting…")
	d := dialog.NewCustomWithoutButtons("Building",
		widgetColumn(status, bar), u.win)
	d.Resize(fyne.NewSize(460, 140))
	d.Show()

	go func() {
		result, err := u.runBuild(func(line string, frac float64) {
			fyne.Do(func() {
				status.SetText(line)
				if frac >= 0 {
					bar.SetValue(frac)
				}
			})
		})
		fyne.Do(func() {
			d.Hide()
			if err != nil {
				dialog.ShowError(err, u.win)
				return
			}
			// Chained, not concurrent: two dialogs at once stack on each other.
			done := dialog.NewCustom("Done", "OK",
				doneContent(result), u.win)
			done.Resize(fyne.NewSize(620, 260))
			done.SetOnClosed(func() {
				if u.state.keptLabel == "" {
					u.askToSave()
				}
			})
			done.Show()
			u.status.SetText("wrote " + filepath.Base(result.Path))
			u.refreshCache()
		})
	}()
}

// runBuild is the same sequence the CLI performs, reported through report.
func (u *window) runBuild(report func(string, float64)) (*build.Result, error) {
	m := u.cat.Manifest()

	report("Fetching payloads…", 0)
	var done int
	total := len(u.res.Files)
	seen := map[string]bool{}

	plan, err := stage.Build(u.ctx, u.src, m, u.res, stage.Options{
		KeepStock: u.keepStockIDs(),
		Workers:   4,
		Progress: func(f manifest.File, got, want int64) {
			if got < want || seen[f.Path] {
				return
			}
			seen[f.Path] = true
			done++
			report(fmt.Sprintf("Fetching payloads… %d/%d", done, total),
				float64(done)/float64(max(total, 1))*0.6)
		},
	})
	if err != nil {
		return nil, err
	}

	report("Verifying archives…", 0.65)
	if err := plan.Verify(4); err != nil {
		return nil, err
	}

	// A bundled busybox gives the recovery installer one known toolset; missing is fine.
	busybox := ""
	if u.state.target == build.TargetRecovery && u.index != nil {
		if p, ok := u.index.BusyboxFor(plan.Release.Architecture()); ok {
			if local, err := u.src.FetchPayload(u.ctx, p); err == nil {
				busybox = local
			}
		}
	}

	if err := writableDir(u.outDir); err != nil {
		return nil, err
	}
	out := filepath.Join(u.outDir, u.zipName(plan.Release.ID, u.state.target))

	opts := build.Options{
		Target:  u.state.target,
		WipeFRP: u.state.wipeFRP && u.state.target == build.TargetRecovery,
		ForceXZ: u.state.forceXZ && u.state.target == build.TargetRecovery,
		Out:     out,
		Busybox: busybox,
		Progress: func(n, of int) {
			report(fmt.Sprintf("Packing… %d/%d", n, of),
				0.7+float64(n)/float64(max(of, 1))*0.25)
		},
	}
	if u.state.target == build.TargetOTA {
		s, err := u.signingOptions(report)
		if err != nil {
			return nil, err
		}
		opts.Signing = s
	}

	report("Packing…", 0.7)
	result, err := build.Build(plan, opts)
	if err != nil {
		return nil, err
	}

	// An ota is already signed with the ROM's keys; signing again with ours breaks it.
	if u.state.target == build.TargetOTA {
		report("Done", 1)
		return restat(result)
	}

	report("Signing…", 0.97)
	key, created, err := sign.LoadOrGenerate(configDir(), "306gapps")
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	if created {
		report("Generated a signing key…", 0.97)
	}
	tmp := result.Path + ".signed"
	if err := sign.Zip(result.Path, tmp, key); err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	if err := osRename(tmp, result.Path); err != nil {
		return nil, err
	}
	if err := sign.Verify(result.Path); err != nil {
		return nil, fmt.Errorf("the signature did not verify: %w", err)
	}

	report("Done", 1)
	return restat(result)
}

// doneContent lays the result out in labelled rows.
//
// A plain information dialog sizes itself to its text and then breaks long
// lines mid-word, which turned a Windows path into "...recovery.z" / "ip".
func doneContent(r *build.Result) fyne.CanvasObject {
	dir, file := filepath.Split(r.Path)

	name := widget.NewLabel(file)
	name.TextStyle = fyne.TextStyle{Bold: true}
	name.Wrapping = fyne.TextWrapBreak

	folder := widget.NewLabel(strings.TrimRight(dir, `/\`))
	folder.Wrapping = fyne.TextWrapBreak
	folder.Importance = widget.LowImportance

	sum := widget.NewLabel(r.SHA256)
	sum.Wrapping = fyne.TextWrapBreak
	sum.Importance = widget.LowImportance

	return container.NewVBox(
		name,
		folder,
		widget.NewSeparator(),
		widget.NewLabel(fmt.Sprintf("%s · %d entries", humanSize(r.Size), r.Files)),
		widget.NewLabel("sha256"),
		sum,
	)
}
