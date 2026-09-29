package main

import (
	"fmt"

	"github.com/306gapps/306gapps/internal/desktop"
)

func cmdInstallDesktop([]string) error {
	paths, err := desktop.Install()
	if err != nil {
		return err
	}
	fmt.Println("installed:")
	for _, p := range paths {
		fmt.Println("  " + p)
	}
	fmt.Println("\nRestart 306gapps for the icon to appear.")
	return nil
}
