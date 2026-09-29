//go:build gui

// Package icon carries the application icon.
package icon

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed icon.png
var png []byte

// Resource is the window and taskbar icon.
var Resource = &fyne.StaticResource{StaticName: "306gapps.png", StaticContent: png}
