package main

import (
	"embed"
	"log"

	"changeme/backend"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The built frontend (frontend/dist) is embedded into the binary. This file
// stays at the module root because go:embed cannot reach parent directories;
// all application logic lives in the backend package.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// All data (database, models, attachments, webview storage) lives in the
	// app folder; move it there from the old shared location if needed.
	backend.SetupDirs()

	store, err := backend.OpenStore()
	if err != nil {
		log.Fatalf("ayar veritabanı açılamadı (%s): %v", backend.DBPath(), err)
	}
	defer store.Close()

	stt := backend.NewSTTService()
	models := backend.NewModelService(stt)
	llama := backend.NewLlamaService(store)
	ai := backend.NewAIService(store, llama)
	settings := backend.NewSettingsService(store, ai)
	transcript := backend.NewTranscriptService(store)
	attachments := backend.NewAttachmentService(store)
	windows := backend.NewWindowService()

	app := application.New(application.Options{
		Name:        "stt-app",
		Description: "Konuşmadan yazıya",
		Services: []application.Service{
			application.NewService(stt),
			application.NewService(models),
			application.NewService(ai),
			application.NewService(llama),
			application.NewService(settings),
			application.NewService(transcript),
			application.NewService(attachments),
			application.NewService(windows),
		},
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(assets),
			Middleware: backend.AttachmentMiddleware, // serves chat attachments for previews
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Konuşmadan Yazıya",
		Width:            1200,
		Height:           780,
		MinWidth:         360,
		MinHeight:        480,
		BackgroundColour: application.NewRGB(9, 9, 11),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
