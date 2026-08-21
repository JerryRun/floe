package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreferencesDefaultAndPersistence(t *testing.T) {
	dataDir := t.TempDir()
	preferences, err := LoadPreferences(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !preferences.OpenBrowserOnStartup() {
		t.Fatal("open browser on startup should default to true")
	}
	if err := preferences.SetOpenBrowserOnStartup(false); err != nil {
		t.Fatal(err)
	}
	knowledgeBaseDir := filepath.Join(dataDir, "knowledge")
	if err := preferences.SetKnowledgeBaseDir(knowledgeBaseDir); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadPreferences(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.OpenBrowserOnStartup() {
		t.Fatal("open browser on startup was not persisted as false")
	}
	if reloaded.KnowledgeBaseDir() != knowledgeBaseDir {
		t.Fatalf("knowledge base directory = %q", reloaded.KnowledgeBaseDir())
	}
}

func TestPreferencesMalformedFileUsesDefaults(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "settings.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	preferences, err := LoadPreferences(dataDir)
	if err == nil {
		t.Fatal("malformed settings should return an error")
	}
	if !preferences.OpenBrowserOnStartup() {
		t.Fatal("malformed settings should retain the safe default")
	}
}
