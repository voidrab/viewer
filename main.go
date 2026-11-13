// Command viewer is a small, fast PDF viewer with PDF/A verification and
// conversion, built with Wails v3 and gopdfrab.
//
// This file is only the desktop wiring: native dialogs, file drop and the
// window. Everything else lives in internal/viewer and is tested there.
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"viewer/internal/viewer"
)

// appID identifies the app to the desktop: GTK, the Wayland window and the
// installed build/linux/<appID>.desktop entry must all carry the same one.
const appID = "com.voidrab.gopdfrab.Viewer"

//go:embed all:frontend/dist
var assets embed.FS

// icon is the gopdfrab gopher, shown in the taskbar and window switcher.
//
//go:embed build/appicon.png
var icon []byte

func main() {
	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatal(err)
	}

	var svc *viewer.Service
	app := application.New(application.Options{
		Name:        "Viewer",
		Description: "A fast, simple PDF viewer with PDF/A validation",
		Icon:        icon,
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(dist)},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		Linux:       application.LinuxOptions{ApplicationID: appID},
		// One process hosts every window. A second launch, such as opening
		// a PDF from the file manager, hands its files to this one and exits.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: appID,
			OnSecondInstanceLaunch: func(d application.SecondInstanceData) {
				svc.Launch(d.Args[1:], d.WorkingDir)
			},
		},
	})
	windows := &desktopWindows{app: app}
	svc = viewer.New(nativeDialogs{}, windows, func(name string, data any) {
		app.Event.Emit(name, data)
	}, callingWindow)
	windows.svc = svc
	app.RegisterService(application.NewServiceWithOptions(svc, application.ServiceOptions{Route: viewer.Route}))

	cwd, _ := os.Getwd()
	svc.Launch(os.Args[1:], cwd)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// callingWindow names the window a bound call came from.
func callingWindow(ctx context.Context) string {
	if w, ok := ctx.Value(application.WindowKey).(application.Window); ok {
		return w.Name()
	}
	return ""
}

// desktopWindows implements viewer.Windows with real webview windows.
type desktopWindows struct {
	app *application.App
	svc *viewer.Service
}

func (d *desktopWindows) New(name string, opening bool) {
	url := "/"
	if opening {
		url = "/?opening" // the page shows its loading bar from first paint
	}
	win := d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             name,
		Title:            "Viewer",
		Width:            1100,
		Height:           800,
		MinWidth:         480,
		MinHeight:        360,
		BackgroundColour: application.NewRGB(0x1b, 0x1c, 0x1f),
		EnableFileDrop:   true,
		URL:              url,
		// The toolbar doubles as the title bar, as in VS Code or Zed: on
		// Linux and Windows the frontend draws its own window controls; on
		// macOS the native traffic lights stay, inset into the toolbar.
		Frameless: runtime.GOOS != "darwin",
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 44,
		},
	})
	win.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		d.svc.HandleDrop(name, e.Context().DroppedFiles())
	})
	win.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) {
		d.svc.WindowFocused(name)
	})
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		d.svc.WindowClosed(name)
	})
}

func (d *desktopWindows) Focus(name string) {
	if w, ok := d.app.Window.GetByName(name); ok {
		w.UnMinimise()
		w.Show()
		w.Focus()
	}
}

// nativeDialogs implements viewer.Dialogs with the platform's file dialogs.
type nativeDialogs struct{}

func (nativeDialogs) OpenPDFs() ([]string, error) {
	return application.Get().Dialog.OpenFile().
		SetTitle("Open PDF").
		AddFilter("PDF documents", "*.pdf").
		AddFilter("All files", "*").
		PromptForMultipleSelection()
}

func (nativeDialogs) SavePDF(suggested string) (string, error) {
	return application.Get().Dialog.SaveFile().
		SetMessage("Save converted PDF").
		SetDirectory(filepath.Dir(suggested)).
		SetFilename(filepath.Base(suggested)).
		AddFilter("PDF documents", "*.pdf").
		PromptForSingleSelection()
}
