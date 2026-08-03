package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBookmarkStorePersistsUNCPaths(t *testing.T) {
	dataDir := t.TempDir()
	store, err := newBookmarkStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	path := `\\wsl.localhost\Ubuntu\home\chensj\tests\floe\dist`
	if err := store.Save("local", []pathBookmark{{Path: path, Label: path}}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newBookmarkStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	entries := reloaded.List()["local"]
	if len(entries) != 1 || entries[0].Path != path || entries[0].Label != path {
		t.Fatalf("reloaded bookmarks = %#v", entries)
	}
}

func TestBookmarkStoreReturnsCopiesAndRemovesEmptyScope(t *testing.T) {
	store, err := newBookmarkStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("session-test", []pathBookmark{{Path: "/release", Label: "/release"}}); err != nil {
		t.Fatal(err)
	}
	listed := store.List()
	listed["session-test"][0].Path = "/changed"
	if store.List()["session-test"][0].Path != "/release" {
		t.Fatal("List returned mutable store data")
	}
	if err := store.Save("session-test", nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := store.List()["session-test"]; exists {
		t.Fatal("empty bookmark scope was not removed")
	}
}

func TestBookmarkAPIStoresAndListsEntries(t *testing.T) {
	store, err := newBookmarkStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{bookmarks: store}
	body := `{"key":"local","entries":[{"path":"\\\\wsl.localhost\\Ubuntu\\home","label":"\\\\wsl.localhost\\Ubuntu\\home"}]}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/bookmarks", strings.NewReader(body))
	response := httptest.NewRecorder()
	server.api(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/bookmarks", nil)
	response = httptest.NewRecorder()
	server.api(response, request)
	var result map[string][]pathBookmark
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result["local"]) != 1 || result["local"][0].Path != `\\wsl.localhost\Ubuntu\home` {
		t.Fatalf("listed bookmarks = %#v", result)
	}
}
