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
		MinWidth:  900,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 17, G: 19, B: 24, A: 1},
		OnStartup:        app.Startup,
		OnShutdown:       app.Shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
