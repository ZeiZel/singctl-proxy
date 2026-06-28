// Command singctl-gui is the desktop GUI for singctl: an unprivileged Wails app
// (Go + React) that drives the root singctl daemon over its control socket and
// reads observability from the daemon's loopback Clash API. It never links
// sing-box and never needs root — all privileged work stays in the daemon.
package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"singctl/gui/bridge"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := bridge.NewApp()

	err := wails.Run(&options.App{
		Title:     "singctl",
		Width:     1100,
		Height:    720,
		MinWidth:  320, // adapt down to a narrow phone-width window
		MinHeight: 520,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// Alpha 0 lets the macOS vibrancy show through; on Linux/Windows the
		// opaque CSS body (data-platform != darwin) covers the window anyway.
		BackgroundColour: &options.RGBA{R: 17, G: 19, B: 24, A: 0},
		OnStartup:        app.Startup,
		OnShutdown:       app.Shutdown,
		// macOS: hide the title bar but keep the native traffic-light buttons
		// (inset), and turn on frosted-glass vibrancy. These options are ignored
		// on other platforms, which keep their standard window frame for now.
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			Appearance:           mac.NSAppearanceNameDarkAqua,
		},
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
