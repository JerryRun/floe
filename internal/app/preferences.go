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
	knowledgeBaseDir     string
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
		OpenBrowserOnStartup *bool  `json:"open_browser_on_startup"`
		KnowledgeBaseDir     string `json:"knowledge_base_dir,omitempty"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return preferences, err
	}
	if stored.OpenBrowserOnStartup != nil {
		preferences.openBrowserOnStartup = *stored.OpenBrowserOnStartup
	}
	preferences.knowledgeBaseDir = stored.KnowledgeBaseDir
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
	previous := p.openBrowserOnStartup
	p.openBrowserOnStartup = value
	if err := p.persistLocked(); err != nil {
		p.openBrowserOnStartup = previous
		return err
	}
	return nil
}

func (p *Preferences) KnowledgeBaseDir() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.knowledgeBaseDir
}

func (p *Preferences) SetKnowledgeBaseDir(value string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	previous := p.knowledgeBaseDir
	p.knowledgeBaseDir = value
	if err := p.persistLocked(); err != nil {
		p.knowledgeBaseDir = previous
		return err
	}
	return nil
}

func (p *Preferences) persistLocked() error {
	data, err := json.MarshalIndent(struct {
		OpenBrowserOnStartup bool   `json:"open_browser_on_startup"`
		KnowledgeBaseDir     string `json:"knowledge_base_dir,omitempty"`
	}{OpenBrowserOnStartup: p.openBrowserOnStartup, KnowledgeBaseDir: p.knowledgeBaseDir}, "", "  ")
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
	return nil
}
