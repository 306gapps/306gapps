package main

import (
	"os"
	"runtime"
)

// haveDisplay reports whether a desktop is there to draw on, so a bare invocation
// over ssh or in a container falls back to the terminal picker.
func haveDisplay() bool {
	switch runtime.GOOS {
	case "windows", "darwin":
		return true
	default:
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	}
}
