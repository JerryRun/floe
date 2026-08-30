package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"floe/internal/core"
)

func TestListFilesReportsSymlinkedDirectoryAsDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "release-v3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("release-v3", filepath.Join(root, "current")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	provider, err := core.NewLocalFSWithKind("local", "local", root, "local", "local")
	if err != nil {
		t.Fatal(err)
	}
	manager := core.NewManager()
	manager.Add(provider)
	server := &Server{manager: manager, activity: newActivityLog(t.TempDir())}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/files?provider=local&path=/", nil)
	response := httptest.NewRecorder()
	server.api(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct{ Entries []core.Entry }
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	var link core.Entry
	for _, entry := range payload.Entries {
		if entry.Name == "current" {
			link = entry
		}
	}
	if !link.IsDir || !link.IsLink || link.LinkTarget != "release-v3" {
		t.Fatalf("symlinked directory entry = %#v", link)
	}
}

func TestResolveLinksReportsTargetsAndBrokenLinks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("data", filepath.Join(root, "data-link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink("notes.txt", filepath.Join(root, "notes-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nowhere", filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	provider, err := core.NewLocalFSWithKind("local", "local", root, "local", "local")
	if err != nil {
		t.Fatal(err)
	}
	manager := core.NewManager()
	manager.Add(provider)
	server := &Server{manager: manager, activity: newActivityLog(t.TempDir())}

	body, _ := json.Marshal(map[string]any{
		"provider": "local",
		"paths":    []string{"/data-link", "/notes-link", "/dangling"},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/files/resolve-links", bytes.NewReader(body))
	response := httptest.NewRecorder()
	server.api(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Links []struct {
			Path   string `json:"path"`
			Target string `json:"link_target"`
			IsDir  bool   `json:"is_dir"`
			Size   int64  `json:"size"`
			Broken bool   `json:"link_broken"`
		} `json:"links"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Links) != 3 {
		t.Fatalf("resolved links = %#v", payload.Links)
	}
	if link := payload.Links[0]; !link.IsDir || link.Target != "data" {
		t.Fatalf("directory link = %#v", link)
	}
	if link := payload.Links[1]; link.IsDir || link.Size != 5 || link.Target != "notes.txt" {
		t.Fatalf("file link = %#v", link)
	}
	if link := payload.Links[2]; !link.Broken || link.IsDir {
		t.Fatalf("dangling link = %#v", link)
	}
}
