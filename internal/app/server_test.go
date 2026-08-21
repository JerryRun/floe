package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMarkdownPreviewAssetsBundled(t *testing.T) {
	assets := map[string]string{
		"web/assets/vendor/marked.min.js": "marked v15.0.12",
		"web/assets/vendor/purify.min.js": "DOMPurify 3.2.6",
	}
	for file, marker := range assets {
		data, err := webFiles.ReadFile(file)
		if err != nil {
			t.Fatalf("read bundled asset %s: %v", file, err)
		}
		if !strings.Contains(string(data), marker) {
			t.Errorf("bundled asset %s does not contain %q", file, marker)
		}
	}
}

func TestPreviewUIIsBundledAndSandboxed(t *testing.T) {
	data, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, marker := range []string{"editorPreviewToggle", "htmlViewportPreset", `sandbox="allow-scripts"`} {
		if !strings.Contains(page, marker) {
			t.Errorf("bundled preview UI does not contain %q", marker)
		}
	}
	if strings.Contains(page, "visibility</span>") {
		t.Fatal("preview button still relies on the missing visibility font ligature")
	}
	if strings.Contains(page, "allow-same-origin") {
		t.Fatal("HTML preview iframe must not share the Floe origin")
	}
}

func TestMemoryWorkbenchUIIsBundled(t *testing.T) {
	data, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, marker := range []string{"memoryPanel", "memorySearch", "memoryFullscreen", "memorySettingsDialog"} {
		if !strings.Contains(page, marker) {
			t.Errorf("bundled memory UI does not contain %q", marker)
		}
	}
}

func TestServerUsesFriendlyLocalhostURL(t *testing.T) {
	url, err := friendlyLoopbackOrigin("127.0.0.1:47667")
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://localhost:47667" {
		t.Fatalf("friendly URL = %q", url)
	}
}

func TestLocalAccessDoesNotRequireSessionOrCSRF(t *testing.T) {
	server := &Server{origin: "http://localhost:47667"}
	request := httptest.NewRequest(http.MethodPost, server.origin+"/api/v1/files/mkdir", nil)
	request.Header.Set("Origin", server.origin)
	response := httptest.NewRecorder()
	server.requireLocalAccess(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("local request status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestLocalHostAcceptsLocalhostAndNumericLoopback(t *testing.T) {
	server := &Server{origin: "http://localhost:47667"}
	for _, host := range []string{"localhost:47667", "127.0.0.1:47667", "[::1]:47667"} {
		if !server.isLocalHost(host) {
			t.Errorf("isLocalHost(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"example.com:47667", "localhost:12345", "localhost"} {
		if server.isLocalHost(host) {
			t.Errorf("isLocalHost(%q) = true, want false", host)
		}
	}
}
