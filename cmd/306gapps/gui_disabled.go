//go:build !gui

package main

import (
	"context"
	"errors"
	"runtime"

	"github.com/306gapps/306gapps/internal/source"
)

const hasGUI = false

func runGUI(_ context.Context, _ *source.Source, _ string) error {
	return errors.New("this build has no desktop picker.\n\n" +
		"The picker needs the platform's own GUI toolchain, which is not " +
		"available for " + runtime.GOOS + "/" + runtime.GOARCH + ".\n" +
		"Use \"306gapps pick\" for the terminal picker, which does the same job.")
}
