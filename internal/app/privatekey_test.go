package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestUploadPrivateKeyStoresPrivateFile(t *testing.T) {
	dataDir := t.TempDir()
	server := &Server{dataDir: dataDir, activity: newActivityLog(dataDir)}
	request := httptest.NewRequest("POST", "/api/v1/sessions/private-key", strings.NewReader("-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n"))
	response := httptest.NewRecorder()
	server.uploadPrivateKey(response, request)
	if response.Code != 201 {
		t.Fatalf("upload status=%d body=%q", response.Code, response.Body.String())
	}
	var result struct{ Path string }
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private key permissions = %o", info.Mode().Perm())
	}
}

func TestUploadPrivateKeyRejectsPublicKey(t *testing.T) {
	dataDir := t.TempDir()
	server := &Server{dataDir: dataDir, activity: newActivityLog(dataDir)}
	request := httptest.NewRequest("POST", "/api/v1/sessions/private-key", strings.NewReader("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA test@example"))
	response := httptest.NewRecorder()
	server.uploadPrivateKey(response, request)
	if response.Code != 400 {
		t.Fatalf("public key upload status=%d body=%q", response.Code, response.Body.String())
	}
}
