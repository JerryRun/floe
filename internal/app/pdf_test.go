package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"floe/internal/core"
)

func TestPDFByteRangesAndReload(t *testing.T) {
	root := t.TempDir()
	content := "%PDF-1.7\n0123456789\n%%EOF"
	if err := os.WriteFile(filepath.Join(root, "报告.PDF"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := core.NewLocalFSWithKind("local", "local", root, "local", "local")
	if err != nil {
		t.Fatal(err)
	}
	manager := core.NewManager()
	manager.Add(provider)
	t.Cleanup(func() { _ = manager.Close() })
	server := &Server{manager: manager}
	for _, tc := range []struct {
		name, method, byteRange, body string
		status                        int
	}{
		{"full", "GET", "", content, http.StatusOK},
		{"head", "HEAD", "", "", http.StatusOK},
		{"prefix", "GET", "bytes=0-4", "%PDF-", http.StatusPartialContent},
		{"suffix", "GET", "bytes=-5", "%%EOF", http.StatusPartialContent},
		{"unsatisfiable", "GET", "bytes=999-", "invalid range: failed to overlap\n", http.StatusRequestedRangeNotSatisfiable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "/api/v1/files/pdf?provider=local&path=/报告.PDF", nil)
			if tc.byteRange != "" {
				request.Header.Set("Range", tc.byteRange)
			}
			response := httptest.NewRecorder()
			server.api(response, request)
			if response.Code != tc.status || response.Body.String() != tc.body {
				t.Fatalf("response = %d %q, want %d %q", response.Code, response.Body.String(), tc.status, tc.body)
			}
			if tc.status < 400 {
				if response.Header().Get("Content-Type") != "application/pdf" || response.Header().Get("Accept-Ranges") != "bytes" {
					t.Fatalf("PDF/range headers = %v", response.Header())
				}
				if !strings.Contains(response.Header().Get("Content-Disposition"), "inline") || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
					t.Fatalf("disposition/cache headers = %v", response.Header())
				}
			}
		})
	}
	if err := os.WriteFile(filepath.Join(root, "报告.PDF"), []byte("%PDF-1.7\nchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.api(response, httptest.NewRequest("GET", "/api/v1/files/pdf?provider=local&path=/报告.PDF&reload=2", nil))
	if response.Body.String() != "%PDF-1.7\nchanged" {
		t.Fatalf("reload returned old content: %q", response.Body.String())
	}
}

func TestPDFRejectsUnsupportedAndMissingFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "folder.pdf"), 0o700); err != nil {
		t.Fatal(err)
	}
	provider, err := core.NewLocalFSWithKind("local", "local", root, "local", "local")
	if err != nil {
		t.Fatal(err)
	}
	manager := core.NewManager()
	manager.Add(provider)
	t.Cleanup(func() { _ = manager.Close() })
	server := &Server{manager: manager}
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/file.txt", http.StatusUnsupportedMediaType},
		{"/missing.pdf", http.StatusBadRequest},
		{"/folder.pdf", http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		server.api(response, httptest.NewRequest("GET", "/api/v1/files/pdf?provider=local&path="+tc.path, nil))
		if response.Code != tc.code {
			t.Errorf("%s: status = %d, want %d", tc.path, response.Code, tc.code)
		}
	}
}
