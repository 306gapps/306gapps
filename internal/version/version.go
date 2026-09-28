// Package version reports which build this is.
package version

import "runtime/debug"

// Version is stamped at link time for a release build; String falls back to build info.
var Version = ""

// String returns the release version, or the best guess available.
func String() string {
	if Version != "" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	// A working-tree build: the commit is more use than "(devel)".
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 12 {
				rev = s.Value[:12]
			} else {
				rev = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev != "" {
		return "devel-" + rev + dirty
	}
	return "devel"
}
