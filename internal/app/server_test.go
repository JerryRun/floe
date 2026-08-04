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

func TestBrowserSessionDoesNotExpire(t *testing.T) {
	const token = "bootstrap-token"
	server := &Server{
		origin:         "http://localhost:47667",
		bootstrapToken: token,
		sessions:       make(map[string]session),
	}
	request := httptest.NewRequest(http.MethodGet, "/bootstrap/"+token, nil)
	request.SetPathValue("token", token)
	response := httptest.NewRecorder()

	server.bootstrap(response, request)

	result := response.Result()
	defer result.Body.Close()
	if result.StatusCode != http.StatusSeeOther {
		t.Fatalf("bootstrap status = %d, want %d", result.StatusCode, http.StatusSeeOther)
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
