package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"floe/internal/core"
)

func TestServerCopiesSessionEndpoint(t *testing.T) {
	dataDir := t.TempDir()
	store, err := newSessionStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(core.ConnectRequest{
		ID: "session-api-copy", Name: "构建机", Protocol: "sftp", Host: "192.0.2.10", User: "root", Password: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{sessionStore: store, activity: newActivityLog(dataDir)}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/session-api-copy/copy", nil)
	response := httptest.NewRecorder()
	server.api(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var copied core.ProviderInfo
	if err := json.NewDecoder(response.Body).Decode(&copied); err != nil {
		t.Fatal(err)
	}
	if copied.ID == "session-api-copy" || copied.Name != "构建机 (2)" {
		t.Fatalf("copied provider = %#v", copied)
	}
	requestCopy, err := store.Request(copied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requestCopy.Password != "secret" {
		t.Fatal("copy endpoint did not preserve the encrypted password")
	}
}
