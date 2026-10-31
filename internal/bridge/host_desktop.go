//go:build !web

package bridge

import (
	"context"
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// hostKind names the shell this binary is: the Wails desktop window.
const hostKind = "desktop"

// desktopApp is the Wails application that hosts the WebView2 window.
type desktopApp = *application.App

// ServiceStartup is called by the Wails runtime when the service is registered.
func (s *AppService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.app = application.Get()
	s.startTaskEvents()
	return nil
}

// ForwardFileDrops turns files dropped on the window into the "files:dropped" UI
// event. The window must be created with EnableFileDrop, and the page marks the
// areas that accept drops with a data-file-drop-target attribute.
func ForwardFileDrops(win *application.WebviewWindow) {
	if win == nil {
		return
	}
	win.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		emitEvent(EventFilesDropped, droppedFilesPayload(e.Context().DroppedFiles()))
	})
}

func desktopEventsReady() bool {
	app := application.Get()
	return app != nil && app.Event != nil
}

func desktopEmit(name string, data any) {
	if app := application.Get(); app != nil && app.Event != nil {
		app.Event.Emit(name, data)
	}
}

// openExternalURL opens a link in the system browser.
func (s *AppService) openExternalURL(url string) error {
	if s.app == nil {
		return nil
	}
	return s.app.Browser.OpenURL(url)
}

var errNoDesktopHost = errors.New("app not initialized")

func (s *AppService) openUtilityWindow(name, title, windowURL string, width, height, minWidth, minHeight int) map[string]any {
	if s.app == nil {
		return map[string]any{"success": false, "error": errNoDesktopHost.Error()}
	}
	if win, ok := s.app.Window.GetByName(name); ok {
		win.SetURL(windowURL).Show()
		win.Focus()
		return map[string]any{"success": true, "window": name, "reused": true}
	}

	s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      name,
		Title:     title,
		Width:     width,
		Height:    height,
		MinWidth:  minWidth,
		MinHeight: minHeight,
		URL:       windowURL,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})

	return map[string]any{"success": true, "window": name}
}
