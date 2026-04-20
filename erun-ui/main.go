package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	eruncommon "github.com/sophium/erun/erun-common"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	setAppIdentity("ERun")

	app := NewApp(erunUIDeps{
		store:           eruncommon.ConfigStore{},
		findProjectRoot: eruncommon.FindProjectRoot,
		resolveCLIPath:  resolveCLIExecutable,
	})

	err := wails.Run(&options.App{
		Title:     "ERun",
		Width:     1320,
		Height:    860,
		MinWidth:  960,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: options.NewRGB(245, 245, 247),
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
		},
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
