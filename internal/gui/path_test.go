//go:build gui

package gui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	cases := []struct {
		goos, in, want string
	}{
		{"linux", "/home/you/roms/target_files.zip", "/home/you/roms/target_files.zip"},
		{"darwin", "/Users/you/keys", "/Users/you/keys"},

		// A Windows file URI holds forward slashes; the os package takes them.
		{"windows", "C:/Users/you/roms/target_files.zip", `C:\Users\you\roms\target_files.zip`},
		// The leading slash before a drive letter has to go.
		{"windows", "/C:/Users/you/keys", `C:\Users\you\keys`},
		{"windows", "/D:/build", `D:\build`},
		{"windows", "/c:/build", `c:\build`},
		// A drive root.
		{"windows", "/C:/", `C:\`},
		// UNC paths keep both leading slashes.
		{"windows", "//server/share/roms", `\\server\share\roms`},
		// Not a drive letter, so nothing is stripped.
		{"windows", "/1:/odd", `\1:\odd`},
	}
	for _, c := range cases {
		if got := normalizePath(c.in, c.goos); got != c.want {
			t.Errorf("normalizePath(%q, %s) = %q, want %q", c.in, c.goos, got, c.want)
		}
	}
}

func TestWritableDir(t *testing.T) {
	if err := writableDir(""); err == nil {
		t.Error("an empty destination should be refused")
	}

	// A folder that does not exist yet is made, not refused.
	fresh := filepath.Join(t.TempDir(), "new", "nested")
	if err := writableDir(fresh); err != nil {
		t.Errorf("should create a missing folder: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("folder was not created: %v", err)
	}

	// And the probe leaves nothing behind.
	entries, err := os.ReadDir(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the writability probe left files behind: %v", entries)
	}

	if os.Geteuid() != 0 {
		ro := filepath.Join(t.TempDir(), "ro")
		if err := os.Mkdir(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		if err := writableDir(ro); err == nil {
			t.Error("a read-only folder should be refused")
		}
	}
}
