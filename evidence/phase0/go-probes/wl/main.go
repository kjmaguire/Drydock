package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed assets
var assets embed.FS

type Greeter struct{ app *application.App }

func (g *Greeter) ServiceStartup(ctx context.Context, o application.ServiceOptions) error { return nil }
func (g *Greeter) Greet(name string) string {
	g.app.Event.Emit("greeted", name)
	return "hi " + name
}

func main() {
	g := &Greeter{}
	app := application.New(application.Options{
		Name:     "t",
		Services: []application.Service{application.NewService(g)},
		Assets:   application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
	})
	g.app = app
	app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "x", Width: 800, Height: 600})
	_ = app.Run()
}
