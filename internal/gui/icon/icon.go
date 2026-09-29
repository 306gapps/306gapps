//go:build gui

package icon

import "fyne.io/fyne/v2"

// Resource is the window icon. Wayland ignores it; see install-desktop.
var Resource = &fyne.StaticResource{StaticName: "306gapps.png", StaticContent: PNG}
