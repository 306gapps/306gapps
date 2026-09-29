// Package icon carries the application icon.
package icon

import (
	"embed"
	_ "embed"
)

//go:embed icon.png
var PNG []byte

// Sizes holds the icon rendered at each icon-theme size, named "<n>.png".
//
//go:embed sizes
var Sizes embed.FS

// AppID is the Wayland app_id, and so also the desktop entry's basename.
const AppID = "com.306gapps.picker"
