package backend

import (
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func init() {
	application.RegisterEvent[string]("settings:page")
	application.RegisterEvent[application.Void]("settings:close-requested")
}

// WindowService opens the settings window (a separate window, one at a time).
type WindowService struct {
	mu       sync.Mutex
	settings *application.WebviewWindow
	unsaved  bool // the settings window has changes that aren't saved
	force    bool // close even with unsaved changes
}

func NewWindowService() *WindowService { return &WindowService{} }

// OpenSettings shows the settings window on the given page ("" = keep the
// current page), creating it if needed.
func (s *WindowService) OpenSettings(page string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	app := application.Get()
	if s.settings != nil {
		if page != "" {
			app.Event.Emit("settings:page", page)
		}
		s.settings.Show()
		s.settings.Focus()
		return
	}
	if page == "" {
		page = "general"
	}
	w := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "settings",
		Title:            "Ayarlar — Konuşmadan Yazıya",
		Width:            1000,
		Height:           720,
		MinWidth:         360,
		MinHeight:        480,
		BackgroundColour: application.NewRGB(9, 9, 11),
		URL:              "/#/settings/" + page,
	})
	// With unsaved changes, closing is held back and the window asks the user.
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		s.mu.Lock()
		block := s.unsaved && !s.force
		s.mu.Unlock()
		if block {
			e.Cancel()
			w.Show()
			w.Focus()
			app.Event.Emit("settings:close-requested")
		}
	})
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		s.mu.Lock()
		s.settings, s.unsaved, s.force = nil, false, false
		s.mu.Unlock()
	})
	s.settings = w
}

// SetSettingsUnsaved tells the backend whether the settings window has
// unsaved changes (closing it then asks first).
func (s *WindowService) SetSettingsUnsaved(unsaved bool) {
	s.mu.Lock()
	s.unsaved = unsaved
	s.mu.Unlock()
}

// DiscardAndCloseSettings closes the settings window without saving.
func (s *WindowService) DiscardAndCloseSettings() {
	s.mu.Lock()
	s.force = true
	w := s.settings
	s.mu.Unlock()
	if w != nil {
		w.Close()
	}
}

// CloseSettings closes the settings window if it is open (it asks first if
// there are unsaved changes).
func (s *WindowService) CloseSettings() {
	s.mu.Lock()
	w := s.settings
	s.mu.Unlock()
	if w != nil {
		w.Close()
	}
}
