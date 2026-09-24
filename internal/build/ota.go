package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/306gapps/306gapps/internal/ota"
	"github.com/306gapps/306gapps/internal/stage"
)

// SigningOptions describe the ROM-specific inputs the OTA target needs.
// A sideload is checked against /system/etc/security/otacerts.zip, so only the
// key the ROM itself was signed with will do.
type SigningOptions struct {
	// Base is the ROM's target-files zip. Required.
	Base string
	// KeyDir holds the release keys named as AOSP expects (releasekey.pk8,
	// releasekey.x509.pem, platform.*, shared.*, media.*). Empty stops at the merge.
	KeyDir string
	// PackageKey signs the OTA itself, without extension; defaults to <KeyDir>/releasekey.
	PackageKey string
	// ToolsDir is an otatools bin directory. Empty means look on PATH.
	ToolsDir string
	// Grow raises a partition's size budget when the selection overflows it.
	Grow bool
	// Log receives progress from the AOSP tools.
	Log func(string)
}

// MergedOnly reports whether the build stops at the merged target-files.
func (s SigningOptions) MergedOnly() bool { return s.KeyDir == "" }

// buildOTA merges the selection into the ROM's target-files and signs it when keys are given.
func buildOTA(plan *stage.Plan, opt Options) (*Result, error) {
	s := opt.Signing
	if s.Base == "" {
		return nil, fmt.Errorf(
			"the ota target needs your ROM's target-files package.\n\n" +
				"Pass -ota-base with the *-target_files-*.zip your build produces, and\n" +
				"-ota-keys with the directory holding the keys you sign that ROM with.\n" +
				"Without keys the merge still runs and stops at a merged target-files\n" +
				"package you can sign yourself.")
	}

	tc := ota.Toolchain{
		Dir:        s.ToolsDir,
		KeyDir:     s.KeyDir,
		PackageKey: s.PackageKey,
		Log:        s.Log,
	}
	// Merging is slow, so fail now rather than after it if signing cannot follow.
	if !s.MergedOnly() {
		if err := tc.Check(); err != nil {
			return nil, err
		}
	}

	merged := mergedName(opt.Out, s.MergedOnly())
	if dir := filepath.Dir(merged); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	logf(s.Log, "merging %d files into %s", len(plan.Entries), filepath.Base(s.Base))
	inj, err := ota.Inject(plan, ota.InjectOptions{
		Base:     s.Base,
		Out:      merged,
		Grow:     s.Grow,
		Progress: opt.Progress,
	})
	if err != nil {
		return nil, err
	}
	for _, w := range inj.Warnings {
		logf(s.Log, "warning: %s", w)
	}
	logf(s.Log, "merged: %d added, %d replaced, %d removed across %s",
		inj.Added, inj.Replaced, inj.Removed, strings.Join(inj.Partitions, ", "))

	if s.MergedOnly() {
		return describe(merged, TargetOTA, inj.Added)
	}

	if _, err := tc.Sign(merged, opt.Out); err != nil {
		return nil, err
	}
	return describe(opt.Out, TargetOTA, inj.Added)
}

// mergedName picks where the intermediate target-files lands, or the requested name when it is the deliverable.
func mergedName(out string, final bool) string {
	if final {
		return out
	}
	return strings.TrimSuffix(out, ".zip") + "-target-files.zip"
}

func describe(path string, target Target, files int) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	sum, err := hashFile(f)
	if err != nil {
		return nil, err
	}
	return &Result{Path: path, Target: target, Size: st.Size(), Files: files, SHA256: sum}, nil
}

func logf(log func(string), format string, a ...any) {
	if log != nil {
		log(fmt.Sprintf(format, a...))
	}
}
