package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"floe/internal/core"
)

func TestSessionStoreEncryptsSecretAndReloads(t *testing.T) {
	dataDir := t.TempDir()
	store, err := newSessionStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	want := core.ConnectRequest{
		ID: "session-test", Name: "测试服务器", Protocol: "ftp", Host: "ftp.example.test",
		Port: 2121, User: "floe", Password: "plain-text-password", Group: "测试组",
	}
	info, err := store.Save(want)
	if err != nil {
		t.Fatal(err)
	}
	if info.Connected {
		t.Fatal("saved session must not be marked connected")
	}

	persisted, err := os.ReadFile(filepath.Join(dataDir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(persisted, []byte(want.Password)) {
		t.Fatal("sessions.json contains the plaintext password")
	}
	key, err := os.ReadFile(filepath.Join(dataDir, "session.key"))
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("session key length = %d, want 32", len(key))
	}

	reloaded, err := newSessionStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Request(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != want.Password || got.Host != want.Host || got.Protocol != want.Protocol {
		t.Fatalf("reloaded request = %#v", got)
	}
}

func TestSessionStoreKeepsExistingSecretWhenEditingWithoutPassword(t *testing.T) {
	store, err := newSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := core.ConnectRequest{
		ID: "session-edit", Protocol: "sftp", Host: "host.example.test", User: "user", Password: "keep-me",
	}
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	request.Name = "新名称"
	request.Password = ""
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	got, err := store.Request(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "keep-me" || got.Name != "新名称" {
		t.Fatalf("edited request = %#v", got)
	}
}

func TestSessionStoreCopyPreservesCredentialsAndIncrementsName(t *testing.T) {
	dataDir := t.TempDir()
	store, err := newSessionStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	original := core.ConnectRequest{
		ID: "session-copy", Name: "生产服务器", Protocol: "sftp", Host: "host.example.test",
		Port: 4322, User: "root", Password: "copy-me", Fingerprint: "SHA256:test", Group: "生产",
	}
	if _, err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	first, err := store.Copy(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Copy(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == original.ID || first.Name != "生产服务器 (2)" || second.Name != "生产服务器 (3)" {
		t.Fatalf("copied sessions = %#v / %#v", first, second)
	}
	copied, err := store.Request(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copied.Password != original.Password || copied.Fingerprint != original.Fingerprint || copied.Group != original.Group {
		t.Fatalf("copied request = %#v", copied)
	}
	reloaded, err := newSessionStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Request(second.ID); err != nil {
		t.Fatalf("copied session was not persisted: %v", err)
	}
}

func TestSessionStoreDefaultsTerminalAutoPasswordOnAndPersistsOptOut(t *testing.T) {
	dataDir := t.TempDir()
	store, err := newSessionStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	request := core.ConnectRequest{
		ID: "session-terminal-password", Protocol: "sftp", Host: "host.example.test", User: "root", Password: "secret",
	}
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	details, err := store.Details(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !details.TerminalAutoPassword {
		t.Fatal("Terminal auto-password should default to enabled")
	}
	disabled := false
	request.TerminalAutoPassword = &disabled
	request.Password = ""
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newSessionStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	details, err = reloaded.Details(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if details.TerminalAutoPassword {
		t.Fatal("Terminal auto-password opt-out was not persisted")
	}
}

func TestSessionStoreDetailsHidePasswordAndDelete(t *testing.T) {
	store, err := newSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := core.ConnectRequest{
		ID: "session-details", Name: "属性测试", Protocol: "ftp", Host: "ftp.example.test",
		Port: 21, User: "floe", Password: "never-return-this", Group: "测试",
	}
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	details, err := store.Details(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !details.HasPassword || details.Host != request.Host || details.Name != request.Name {
		t.Fatalf("details = %#v", details)
	}
	if err := store.Delete(request.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Details(request.ID); err == nil {
		t.Fatal("deleted session is still available")
	}
	reloaded, err := newSessionStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.List()) != 0 {
		t.Fatal("deleted session reappeared after reload")
	}
}

func TestSessionStoreClearsFingerprintWhenEndpointChanges(t *testing.T) {
	store, err := newSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := core.ConnectRequest{
		ID: "session-host-change", Protocol: "sftp", Host: "old.example.test", Port: 22,
		User: "root", Password: "secret", Fingerprint: "SHA256:old",
	}
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	request.Host = "new.example.test"
	request.Password = ""
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	got, err := store.Request(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Fingerprint != "" {
		t.Fatalf("fingerprint after endpoint change = %q, want empty", got.Fingerprint)
	}
	if got.Password != "secret" {
		t.Fatal("editing endpoint lost the saved password")
	}
}

func TestSessionStorePersistsSSHKeepAlive(t *testing.T) {
	dataDir := t.TempDir()
	store, err := newSessionStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	request := core.ConnectRequest{
		ID: "session-keepalive", Protocol: "sftp", Host: "ssh.example.test", User: "root",
		SSHKeepAlive: true, ServerAliveInterval: 45, ServerAliveCountMax: 5,
	}
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	details, err := store.Details(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !details.SSHKeepAlive || details.ServerAliveInterval != 45 || details.ServerAliveCountMax != 5 {
		t.Fatalf("details keepalive = %#v", details)
	}
	reloaded, err := newSessionStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Request(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SSHKeepAlive || got.ServerAliveInterval != 45 || got.ServerAliveCountMax != 5 {
		t.Fatalf("reloaded keepalive = %#v", got)
	}
}

func TestSessionStoreSSHKeepAliveDefaultsToDisabled(t *testing.T) {
	store, err := newSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := core.ConnectRequest{ID: "session-default-keepalive", Protocol: "sftp", Host: "host", User: "root"}
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	got, err := store.Request(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SSHKeepAlive {
		t.Fatal("SSH keepalive was enabled by default")
	}
	if got.ServerAliveInterval != 60 || got.ServerAliveCountMax != 3 {
		t.Fatalf("disabled keepalive defaults = %d/%d", got.ServerAliveInterval, got.ServerAliveCountMax)
	}
}

func TestSessionStoreClearsSSHKeepAliveForFTP(t *testing.T) {
	store, err := newSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := core.ConnectRequest{
		ID: "ftp-no-keepalive", Protocol: "ftp", Host: "ftp.example.test", User: "user",
		SSHKeepAlive: true, ServerAliveInterval: 20, ServerAliveCountMax: 2,
	}
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	got, err := store.Request(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SSHKeepAlive || got.ServerAliveInterval != 0 || got.ServerAliveCountMax != 0 {
		t.Fatalf("FTP retained SSH keepalive: %#v", got)
	}
}

func TestSessionStoreRejectsInvalidEnabledSSHKeepAlive(t *testing.T) {
	tests := []core.ConnectRequest{
		{Protocol: "sftp", Host: "host", User: "root", SSHKeepAlive: true, ServerAliveInterval: 4, ServerAliveCountMax: 3},
		{Protocol: "sftp", Host: "host", User: "root", SSHKeepAlive: true, ServerAliveInterval: 60, ServerAliveCountMax: 21},
	}
	for _, request := range tests {
		store, err := newSessionStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Save(request); err == nil {
			t.Fatalf("invalid keepalive was accepted: %#v", request)
		}
	}
}

func TestSessionStoreInfersKeyAuthenticationAndClearsSecretWhenSwitching(t *testing.T) {
	store, err := newSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := core.ConnectRequest{
		ID: "session-key-auth", Protocol: "sftp", Host: "host", User: "root",
		PrivateKey: `C:\Users\test\.ssh\id_ed25519`, Password: "key-passphrase",
	}
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	got, err := store.Request(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthMethod != "key" || got.PrivateKey != request.PrivateKey || got.Password != request.Password {
		t.Fatalf("inferred key authentication = %#v", got)
	}

	request.AuthMethod = "password"
	request.Password = ""
	if _, err := store.Save(request); err != nil {
		t.Fatal(err)
	}
	got, err = store.Request(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthMethod != "password" || got.PrivateKey != "" || got.Password != "" {
		t.Fatalf("authentication switch retained key credentials: %#v", got)
	}
}
