package build

import (
	"fmt"
	"strings"

	"github.com/306gapps/306gapps/internal/stage"
)

// buildModule emits a Magisk/KernelSU module, which overlays rather than writes into the ROM.
func buildModule(plan *stage.Plan, w *writer, opt Options) error {
	if err := w.addTemplate("META-INF/com/google/android/update-binary", "module/update-binary", 0o755); err != nil {
		return err
	}
	if err := w.addBytes("META-INF/com/google/android/updater-script", 0o644, []byte("#MAGIC=!/sbin/magisk\n")); err != nil {
		return err
	}
	if err := w.addTemplate("customize.sh", "module/customize.sh", 0o755); err != nil {
		return err
	}
	if err := w.addTemplate("service.sh", "module/service.sh", 0o755); err != nil {
		return err
	}
	if err := w.addBytes("module.prop", 0o644, moduleProp(plan)); err != nil {
		return err
	}
	if err := w.addBytes("release.txt", 0o644, releaseInfo(plan)); err != nil {
		return err
	}
	if err := w.addBytes("props.txt", 0o644, propsFile(plan)); err != nil {
		return err
	}
	if err := w.addBytes("removals.txt", 0o644, linesFile(plan.Removes)); err != nil {
		return err
	}

	entries := sortedEntries(plan)
	for i, e := range entries {
		if err := w.addFile(modulePath(e.Path), e.Local, e.Mode); err != nil {
			return err
		}
		if opt.Progress != nil {
			opt.Progress(i+1, len(entries))
		}
	}
	return nil
}

// modulePath maps an install path into the module overlay: the manager mirrors
// system/ onto /system, and reaches /product through /system/product.
func modulePath(p string) string {
	part, rest, ok := strings.Cut(p, "/")
	if !ok {
		return "system/" + p
	}
	if part == "system" {
		return "system/" + rest
	}
	return "system/" + part + "/" + rest
}

func moduleProp(plan *stage.Plan) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "id=306gapps\n")
	fmt.Fprintf(&b, "name=306gapps (Android %s)\n", plan.Release.Android.Version)
	fmt.Fprintf(&b, "version=%s\n", plan.Release.ID)
	fmt.Fprintf(&b, "versionCode=%d\n", versionCode(plan))
	fmt.Fprintf(&b, "author=306gapps\n")
	fmt.Fprintf(&b, "description=%d Google packages dumped from %s %s. Disable or remove this module to revert.\n",
		len(plan.Packages), plan.Release.Source.Device, plan.Release.Source.Build)
	return []byte(b.String())
}

// versionCode is the release date as an integer, which module managers compare for upgrades.
func versionCode(plan *stage.Plan) int {
	if t := plan.Release.Created; !t.IsZero() {
		return t.Year()*10000 + int(t.Month())*100 + t.Day()
	}
	return 1
}
