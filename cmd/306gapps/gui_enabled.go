//go:build gui

package main

import (
	"context"

	"github.com/306gapps/306gapps/internal/gui"
	"github.com/306gapps/306gapps/internal/source"
)

// hasGUI reports whether this build can open the desktop picker, which needs cgo
// and a platform GUI toolchain.
const hasGUI = true

func runGUI(ctx context.Context, s *source.Source, out string) error {
	detachConsole()
	return gui.Run(ctx, s, out)
}
