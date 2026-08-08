package core

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Run with a disposable FTP server, for example:
// FLOE_TEST_FTP=127.0.0.1:2121 FLOE_TEST_FTP_USER=floe \
// FLOE_TEST_FTP_PASSWORD=floe-pass go test ./internal/core -run TestFTPIntegration
func TestFTPIntegrationUploadDownloadAndSHA256(t *testing.T) {
	address := os.Getenv("FLOE_TEST_FTP")
	if address == "" {
		t.Skip("FLOE_TEST_FTP is not set")
	}
	host, port := splitTestAddress(t, address)
	user := os.Getenv("FLOE_TEST_FTP_USER")
	password := os.Getenv("FLOE_TEST_FTP_PASSWORD")

	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source")
	downloadRoot := filepath.Join(root, "download")
	source, err := NewLocalFS("source", "Source", sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	download, err := NewLocalFS("download", "Download", downloadRoot)
	if err != nil {
		t.Fatal(err)
	}
	ftpFS, err := NewFTPFS("ftp", "FTP", "Test", FTPConfig{Host: host, Port: port, User: user, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	defer ftpFS.Close()

	manager := NewManager()
	manager.Add(source)
	manager.Add(download)
	manager.Add(ftpFS)
	engine := NewTransferEngine(manager, filepath.Join(root, "tasks.json"))

	// A multi-block file exercises resumed FTP reads/writes and verifies every
	// adaptive range. FTP providers may reduce concurrency to match server limits.
	content := make([]byte, defaultBlockSize+(3<<20))
	for i := range content {
		content[i] = byte((i*31 + 17) % 251)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "large.bin"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	remotePath := fmt.Sprintf("/floe-integration-%d.bin", time.Now().UnixNano())
	defer ftpFS.Remove(remotePath)

	upload, err := engine.Create(TransferRequest{
		SourceProvider: "source", SourcePath: "/large.bin", TargetProvider: "ftp", TargetPath: remotePath, Concurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForFTPTask(t, engine, upload.ID)
	uploaded, err := ftpFS.ReadFile(remotePath, int64(len(content))+1)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(uploaded) != sha256.Sum256(content) {
		t.Fatal("FTP upload SHA-256 differs from source")
	}

	downloadTask, err := engine.Create(TransferRequest{
		SourceProvider: "ftp", SourcePath: remotePath, TargetProvider: "download", TargetPath: "/roundtrip.bin", Concurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForFTPTask(t, engine, downloadTask.ID)
	roundtrip, err := os.ReadFile(filepath.Join(downloadRoot, "roundtrip.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(roundtrip, content) {
		t.Fatal("FTP round trip differs from source")
	}
}

func TestFTPIntegrationReconnectsAfterServerIdleTimeout(t *testing.T) {
	address := os.Getenv("FLOE_TEST_FTP")
	waitText := os.Getenv("FLOE_TEST_FTP_IDLE_WAIT")
	if address == "" || waitText == "" {
		t.Skip("FLOE_TEST_FTP and FLOE_TEST_FTP_IDLE_WAIT are not set")
	}
	wait, err := time.ParseDuration(waitText)
	if err != nil {
		t.Fatal(err)
	}
	host, port := splitTestAddress(t, address)
	provider, err := NewFTPFS("ftp-idle", "FTP idle", "Test", FTPConfig{
		Host: host, Port: port, User: os.Getenv("FLOE_TEST_FTP_USER"), Password: os.Getenv("FLOE_TEST_FTP_PASSWORD"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	if _, err := provider.List("/"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(wait)
	if _, err := provider.List("/"); err != nil {
		t.Fatalf("list after FTP idle timeout did not reconnect: %v", err)
	}
}

func waitForFTPTask(t *testing.T, engine *TransferEngine, id string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, task := range engine.List() {
			if task.ID != id {
				continue
			}
			if task.Status == "completed" {
				return
			}
			if task.Status == "failed" {
				t.Fatalf("FTP transfer failed: %s", task.Error)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("FTP transfer did not complete before timeout (status %s)", taskStatus(engine, id))
}

func splitTestAddress(t *testing.T, address string) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("invalid FLOE_TEST_FTP address %q", address)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("invalid FLOE_TEST_FTP port %q", portText)
	}
	return host, port
}
