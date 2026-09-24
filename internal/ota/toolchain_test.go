package ota

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTools writes stand-ins for the AOSP binaries that record how they were
// called, so the invocation order and arguments can be asserted without a ROM
// build tree.
func fakeTools(t *testing.T, failing string) (dir, logPath string) {
	t.Helper()
	dir = t.TempDir()
	logPath = filepath.Join(dir, "calls.log")

	for _, tool := range Tools {
		body := fmt.Sprintf(`#!/bin/sh
echo "%s $*" >> %q
echo "working on $*"
`, tool, logPath)
		if tool == failing {
			body += "echo 'error: partition product too big' >&2\nexit 1\n"
		} else if tool == "ota_from_target_files" || tool == "sign_target_files_apks" {
			// These produce their output file; the caller stats it.
			body += `eval "out=\$$#"; printf 'package' > "$out"` + "\n"
		}
		p := filepath.Join(dir, tool)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir, logPath
}

func fakeKeys(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"releasekey.pk8", "releasekey.x509.pem"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("key"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSignRunsTheToolsInOrderWithTheRightArguments(t *testing.T) {
	tools, logPath := fakeTools(t, "")
	keys := fakeKeys(t)
	work := t.TempDir()
	merged := filepath.Join(work, "merged.zip")
	os.WriteFile(merged, []byte("merged"), 0o644)
	out := filepath.Join(work, "ota.zip")

	tc := Toolchain{Dir: tools, KeyDir: keys}
	res, err := tc.Sign(merged, out)
	if err != nil {
		t.Fatal(err)
	}
	if res.Package != out {
		t.Errorf("got package %q", res.Package)
	}

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 tool invocations, got %d:\n%s", len(lines), b)
	}

	signed := strings.TrimSuffix(merged, ".zip") + "-signed.zip"
	want := []string{
		"add_img_to_target_files -a " + merged,
		"sign_target_files_apks --default_key_mappings " + keys + " " + merged + " " + signed,
		"ota_from_target_files --package_key " + filepath.Join(keys, "releasekey") + " " + signed + " " + out,
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("step %d:\n got %s\nwant %s", i+1, lines[i], w)
		}
	}
}

func TestSignHonoursAnExplicitPackageKey(t *testing.T) {
	tools, logPath := fakeTools(t, "")
	keys := fakeKeys(t)
	explicit := filepath.Join(keys, "releasekey")

	merged := filepath.Join(t.TempDir(), "m.zip")
	os.WriteFile(merged, []byte("m"), 0o644)

	tc := Toolchain{Dir: tools, KeyDir: keys, PackageKey: explicit}
	if _, err := tc.Sign(merged, filepath.Join(t.TempDir(), "o.zip")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(logPath)
	if !strings.Contains(string(b), "--package_key "+explicit) {
		t.Errorf("explicit package key not used:\n%s", b)
	}
}

func TestSignSurfacesTheToolsOwnError(t *testing.T) {
	tools, _ := fakeTools(t, "add_img_to_target_files")
	keys := fakeKeys(t)
	merged := filepath.Join(t.TempDir(), "m.zip")
	os.WriteFile(merged, []byte("m"), 0o644)

	tc := Toolchain{Dir: tools, KeyDir: keys}
	_, err := tc.Sign(merged, filepath.Join(t.TempDir(), "o.zip"))
	if err == nil {
		t.Fatal("expected failure")
	}
	// The tool's own diagnostic must reach the user, plus our hint.
	if !strings.Contains(err.Error(), "partition product too big") {
		t.Errorf("tool output not surfaced: %v", err)
	}
	if !strings.Contains(err.Error(), "-ota-grow") {
		t.Errorf("no actionable hint: %v", err)
	}
}

func TestCheckReportsEverythingMissingAtOnce(t *testing.T) {
	tc := Toolchain{Dir: t.TempDir(), KeyDir: t.TempDir()}
	err := tc.Check()
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	for _, want := range append(append([]string{}, Tools...), "releasekey.pk8", "releasekey.x509.pem") {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q from:\n%s", want, msg)
		}
	}
}

func TestCheckPassesWhenEverythingIsPresent(t *testing.T) {
	tools, _ := fakeTools(t, "")
	if err := (Toolchain{Dir: tools, KeyDir: fakeKeys(t)}).Check(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRejectsAMissingKeyDirectory(t *testing.T) {
	tools, _ := fakeTools(t, "")
	err := (Toolchain{Dir: tools}).Check()
	if err == nil || !strings.Contains(err.Error(), "no key directory") {
		t.Fatalf("got %v", err)
	}
}
