//go:build windows

package main

import "syscall"

// detachConsole hides the empty console the picker would otherwise leave behind.
// The binary stays on the console subsystem so the CLI has somewhere to print.
func detachConsole() {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole")
	_, _, _ = proc.Call()
}
