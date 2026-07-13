package main

import (
	"embed"
	"net/http"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// The embedded frontend bundle is now just the Panel-URL settings page,
// reachable at /__beam/. The actual UI is the Dylaris Panel, reverse-
// proxied through the Wails asset server (see proxy.go) so the webview
// stays on the wails:// origin and Wails keeps injecting its runtime —
// that's what makes window.go.main.App.* available to the Panel.
//
// There is no native menu or splash: the window loads the Panel
// directly. The settings page is reached as a fallback when the Panel
// can't be loaded (the proxy's error page links to it).
//
//go:embed all:frontend/dist
var assets embed.FS

// defaultPanelURL is the production Panel address. Overridable per
// install through the in-app Settings page (saved to a config.json in
// the user's config dir), or per build via the DYLARIS_PANEL_URL env
// var so dev / staging installs can ship with their own default.
const defaultPanelURL = "https://panel.dylaris.com"

func main() {
	app := NewApp()
	panelURL := os.Getenv("DYLARIS_PANEL_URL")
	if panelURL == "" {
		panelURL = defaultPanelURL
	}
	app.panelURL = panelURL

	err := wails.Run(&options.App{
		Title:  "Dylaris Beam",
		Width:  1400,
		Height: 900,
		// Below ~1280 wide the Panel's server-tab bar wraps in a way that
		// hides labels behind the right edge; below ~720 tall the bottom
		// console gets squashed. Pin a floor that keeps everything usable.
		MinWidth:  1280,
		MinHeight: 720,
		AssetServer: &assetserver.Options{
			Assets: assets,
			// Turns the window into a transparent shell around the
			// remote Panel — see newPanelMiddleware for the why.
			Middleware: func(next http.Handler) http.Handler {
				return newPanelMiddleware(app, next)
			},
		},
		Windows: &windows.Options{
			// Show the branded titlebar icon. Windows-window-chrome only; does
			// not touch the BC3-patched webview dispatcher or asset server.
			DisableWindowIcon: false,
		},
		OnStartup: app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
