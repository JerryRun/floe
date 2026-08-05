package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"floe/internal/core"
)

func TestHTMLPreviewDocumentAndResources(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "site"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "site", "style.css"), []byte("body { color: teal; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider, err := core.NewLocalFSWithKind("local", "local", root, "local", "local")
	if err != nil {
		t.Fatal(err)
	}
	manager := core.NewManager()
	manager.Add(provider)
	server := &Server{manager: manager, htmlPreviews: make(map[string]htmlPreviewDocument)}

	requestBody, _ := json.Marshal(map[string]string{
		"provider": "local", "path": "/site/index.html",
		"content": `<!doctype html><html><head><link rel="stylesheet" href="style.css"></head><body><img src="/images/logo.png"><h1>Preview</h1></body></html>`,
	})
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/files/html-preview", bytes.NewReader(requestBody))
	createResponse := httptest.NewRecorder()
	server.api(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create preview status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var created map[string]string
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["token"] == "" || created["url"] == "" {
		t.Fatalf("invalid preview response: %#v", created)
	}

	documentRequest := httptest.NewRequest(http.MethodGet, created["url"], nil)
	documentResponse := httptest.NewRecorder()
	server.api(documentResponse, documentRequest)
	if documentResponse.Code != http.StatusOK {
		t.Fatalf("preview document status = %d", documentResponse.Code)
	}
	document := documentResponse.Body.String()
	base := "/api/v1/files/html-resource/" + created["token"] + "/site/"
	if !strings.Contains(document, `<base href="`+base+`">`) {
		t.Fatalf("preview document does not contain provider base %q: %s", base, document)
	}
	if !strings.Contains(document, `src="/api/v1/files/html-resource/`+created["token"]+`/images/logo.png"`) {
		t.Fatalf("root-relative resource was not rewritten: %s", document)
	}
	if documentResponse.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("preview frame policy = %q", documentResponse.Header().Get("X-Frame-Options"))
	}
	if !strings.Contains(documentResponse.Header().Get("Content-Security-Policy"), "form-action 'none'") {
		t.Fatal("preview CSP does not block forms")
	}

	resourceRequest := httptest.NewRequest(http.MethodGet, base+"style.css", nil)
	resourceResponse := httptest.NewRecorder()
	server.api(resourceResponse, resourceRequest)
	if resourceResponse.Code != http.StatusOK || resourceResponse.Body.String() != "body { color: teal; }" {
		t.Fatalf("resource response = %d %q", resourceResponse.Code, resourceResponse.Body.String())
	}
	if !strings.HasPrefix(resourceResponse.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("resource content type = %q", resourceResponse.Header().Get("Content-Type"))
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, created["url"], nil)
	deleteResponse := httptest.NewRecorder()
	server.api(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete preview status = %d", deleteResponse.Code)
	}
}
