package ota

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Tools names the AOSP otatools binaries this package drives, in the order they run.
var Tools = []string{
	"add_img_to_target_files",
	"sign_target_files_apks",
	"ota_from_target_files",
}

// Toolchain locates and runs the AOSP OTA tooling.
type Toolchain struct {
	// Dir is an otatools bin directory. Empty means look on PATH.
	Dir string
	// KeyDir holds the ROM's release keys named as AOSP expects (releasekey.pk8,
	// releasekey.x509.pem, platform.*, shared.*, media.*).
	KeyDir string
	// PackageKey signs the OTA itself, without extension; defaults to <KeyDir>/releasekey.
	PackageKey string
	// Log receives each tool's output as it runs.
	Log func(string)
}

// Resolve returns the path to a tool, preferring Dir over PATH.
func (t Toolchain) Resolve(tool string) (string, error) {
	if t.Dir != "" {
		p := filepath.Join(t.Dir, tool)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("%s not found in %s", tool, t.Dir)
	}
	p, err := exec.LookPath(tool)
	if err != nil {
		return "", fmt.Errorf("%s not found on PATH "+
			"(pass -ota-tools with your otatools bin directory)", tool)
	}
	return p, nil
}

// Check looks for the tools and keys up front, so a two-hour merge does not fail at the last step.
func (t Toolchain) Check() error {
	var problems []string
	for _, tool := range Tools {
		if _, err := t.Resolve(tool); err != nil {
			problems = append(problems, "  - "+err.Error())
		}
	}

	if t.KeyDir == "" {
		problems = append(problems, "  - no key directory given")
	} else {
		key := t.packageKey()
		for _, ext := range []string{".pk8", ".x509.pem"} {
			if _, err := os.Stat(key + ext); err != nil {
				problems = append(problems, fmt.Sprintf(
					"  - signing key %s%s is missing", key, ext))
			}
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("cannot sign:\n%s\n\n"+
			"otatools comes from your ROM build as out/host/linux-x86/bin, or\n"+
			"from the otatools.zip your build produces. The keys are the ones you\n"+
			"already sign your ROM with.", strings.Join(problems, "\n"))
	}
	return nil
}

func (t Toolchain) packageKey() string {
	if t.PackageKey != "" {
		return t.PackageKey
	}
	return filepath.Join(t.KeyDir, "releasekey")
}

// SignResult reports the finished package.
type SignResult struct {
	Package string
	Signed  string
}

// Sign takes a merged target-files package through to a sideloadable zip.
func (t Toolchain) Sign(merged, out string) (*SignResult, error) {
	if err := t.Check(); err != nil {
		return nil, err
	}

	t.logf("building partition images from the merged trees")
	if err := t.run("add_img_to_target_files", "-a", merged); err != nil {
		return nil, fmt.Errorf("add_img_to_target_files: %w\n\n"+
			"A partition that no longer fits is the usual cause. Re-run with\n"+
			"-ota-grow, or deselect packages.", err)
	}

	signed := strings.TrimSuffix(merged, ".zip") + "-signed.zip"
	t.logf("re-signing apks with your release keys")
	if err := t.run("sign_target_files_apks",
		"--default_key_mappings", t.KeyDir, merged, signed); err != nil {
		return nil, fmt.Errorf("sign_target_files_apks: %w", err)
	}

	t.logf("generating the OTA package")
	if err := t.run("ota_from_target_files",
		"--package_key", t.packageKey(), signed, out); err != nil {
		return nil, fmt.Errorf("ota_from_target_files: %w", err)
	}

	return &SignResult{Package: out, Signed: signed}, nil
}

func (t Toolchain) logf(format string, a ...any) {
	if t.Log != nil {
		t.Log(fmt.Sprintf(format, a...))
	}
}

// run executes a tool, streaming its output and keeping the tail for errors.
func (t Toolchain) run(tool string, args ...string) error {
	bin, err := t.Resolve(tool)
	if err != nil {
		return err
	}

	// One pipe for both streams: these tools interleave stdout and stderr, and the
	// order matters when reading a failure.
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return err
	}
	pw.Close() // the child holds the only writer now
	defer pr.Close()

	const keep = 20
	var tail []string
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<22)
	for sc.Scan() {
		line := sc.Text()
		t.logf("  %s", line)
		tail = append(tail, line)
		if len(tail) > keep {
			tail = tail[1:]
		}
	}

	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(tail) > 0 {
			return fmt.Errorf("exited %d:\n%s",
				exit.ExitCode(), strings.Join(tail, "\n"))
		}
		return err
	}
	return nil
}
