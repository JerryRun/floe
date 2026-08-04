package app

import (
	"encoding/json"
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

func TestBrowserSessionIsCreatedWithoutBootstrapToken(t *testing.T) {
	server := &Server{
		origin:   "http://localhost:47667",
		sessions: make(map[string]session),
	}
	request := httptest.NewRequest(http.MethodGet, server.origin+"/api/v1/session", nil)
	response := httptest.NewRecorder()

	server.openSession(response, request)

	result := response.Result()
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d, want %d", result.StatusCode, http.StatusOK)
	}
	var payload map[string]string
	if err := json.NewDecoder(result.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["csrf"] == "" {
		t.Fatal("session response has no CSRF token")
	}
	cookies := result.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("bootstrap cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Fatalf("session cookie unexpectedly expires: MaxAge=%d Expires=%v", cookie.MaxAge, cookie.Expires)
	}

	protectedRequest := httptest.NewRequest(http.MethodGet, server.origin+"/", nil)
	protectedRequest.AddCookie(cookie)
	protectedResponse := httptest.NewRecorder()
	server.requireSession(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(protectedResponse, protectedRequest)
	if protectedResponse.Code != http.StatusNoContent {
		t.Fatalf("protected request status = %d, want %d", protectedResponse.Code, http.StatusNoContent)
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
