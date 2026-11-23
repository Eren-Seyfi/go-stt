package backend

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	application.RegisterEvent[SettingsChange]("settings:changed")
}

// SettingsChange is emitted as "settings:changed" after settings are saved
// or reset, so every open window stays in sync.
type SettingsChange struct {
	Source string         `json:"source"` // ID of the window that made the change
	Values map[string]any `json:"values"` // the keys that changed
	Reset  bool           `json:"reset"`  // everything went back to defaults
}

// SettingsService keeps the UI settings in the local SQLite database.
type SettingsService struct {
	store *Store
	ai    *AIService
}

func NewSettingsService(store *Store, ai *AIService) *SettingsService {
	return &SettingsService{store: store, ai: ai}
}

// Load returns every saved setting; missing keys use the UI's defaults.
func (s *SettingsService) Load() (map[string]any, error) {
	return s.store.LoadSettings()
}

// Save stores the changed settings. source identifies the calling window so
// it can ignore its own change notification.
func (s *SettingsService) Save(source string, values map[string]any) error {
	if len(values) == 0 {
		return nil
	}
	if err := s.store.SaveSettings(values); err != nil {
		return err
	}
	application.Get().Event.Emit("settings:changed", SettingsChange{Source: source, Values: values})
	return nil
}

// Reset deletes all saved settings and puts the AI settings back to their
// defaults (API keys and downloaded models are kept).
func (s *SettingsService) Reset() error {
	if err := s.store.ClearSettings(); err != nil {
		return err
	}
	if err := s.ai.ResetConfig(); err != nil {
		return err
	}
	application.Get().Event.Emit("settings:changed", SettingsChange{Reset: true, Values: map[string]any{}})
	return nil
}

// Path is the database file's location.
func (s *SettingsService) Path() string { return DBPath() }
