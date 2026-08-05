package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Preferences struct {
	mu                   sync.RWMutex
	path                 string
	openBrowserOnStartup bool
}

func LoadPreferences(dataDir string) (*Preferences, error) {
	preferences := &Preferences{
		path: filepath.Join(dataDir, "settings.json"), openBrowserOnStartup: true,
	}
	data, err := os.ReadFile(preferences.path)
	if errors.Is(err, os.ErrNotExist) {
		return preferences, nil
	}
	if err != nil {
		return preferences, err
	}
	var stored struct {
		OpenBrowserOnStartup *bool `json:"open_browser_on_startup"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return preferences, err
	}
	if stored.OpenBrowserOnStartup != nil {
		preferences.openBrowserOnStartup = *stored.OpenBrowserOnStartup
	}
	return preferences, nil
}

func (p *Preferences) OpenBrowserOnStartup() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.openBrowserOnStartup
}

func (p *Preferences) SetOpenBrowserOnStartup(value bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, err := json.MarshalIndent(struct {
		OpenBrowserOnStartup bool `json:"open_browser_on_startup"`
	}{OpenBrowserOnStartup: value}, "", "  ")
	if err != nil {
		return err
	}
	temporary := p.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, p.path); err != nil {
		return err
	}
	p.openBrowserOnStartup = value
	return nil
}
