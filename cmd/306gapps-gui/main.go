//go:build gui

// Command 306gapps-gui is the desktop picker.
//
// It is a view over the same core the command line uses: the manifest, the
// dependency resolver, the payload cache, the builders and the signer are all
// shared, so a package built here and one built from the command line are the
// same bytes.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/306gapps/306gapps/internal/gui"
	"github.com/306gapps/306gapps/internal/source"
)

// DefaultSource matches the command line's.
const DefaultSource = "https://raw.githubusercontent.com/306gapps/306gapps-assets/main"

func main() {
	src := flag.String("source", envOr("GAPPS_SOURCE", DefaultSource),
		"assets repo URL or local directory")
	cache := flag.String("cache", envOr("GAPPS_CACHE", ""), "download cache directory")
	out := flag.String("out", ".", "directory to write the built package to")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "306gapps-gui: "+err.Error())
		os.Exit(1)
	}

	s := source.New(*src, source.NewCache(*cache))
	if err := gui.Run(ctx, s, *out); err != nil {
		fmt.Fprintln(os.Stderr, "306gapps-gui: "+err.Error())
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
