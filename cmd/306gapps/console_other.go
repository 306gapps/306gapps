//go:build !windows

package main

// detachConsole is a Windows concern; elsewhere a terminal is just a terminal.
func detachConsole() {}
