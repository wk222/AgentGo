package main

import (
	"log"

	"agentgo/frontend"
	"agentgo/internal/bridge"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func main() {
	rt, err := bridge.NewRuntime()
	if err != nil {
		log.Fatal(err)
	}

	appService := bridge.NewAppService(rt)

	app := application.New(application.Options{
		Name:        "AgentGo",
		Description: "AgentGo Desktop IDE built with Wails v3 and Eino",
		Services: []application.Service{
			application.NewService(appService),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(frontend.Assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "AgentGo",
		Width:  1200,
		Height: 800,
		URL:    "/",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
