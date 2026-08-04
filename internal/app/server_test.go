package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
