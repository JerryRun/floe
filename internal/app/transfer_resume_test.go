package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"floe/internal/core"
)

func TestResumeTransferConnectsSavedSourceAndTargetFirst(t *testing.T) {
	server, taskID, providers := newResumeTestServer(t)
	connected := make([]string, 0, 2)
	server.connectProvider = func(request core.ConnectRequest) (core.ProviderInfo, error) {
		connected = append(connected, request.ID)
		provider := providers[request.ID]
		server.manager.Add(provider)
		return core.ProviderInfo{ID: request.ID, Connected: true}, nil
	}

	if err := server.resumeTransfer(taskID); err != nil {
		t.Fatal(err)
	}
	if len(connected) != 2 || connected[0] != "source-session" || connected[1] != "target-session" {
		t.Fatalf("connected sessions = %v", connected)
	}
	waitForAppTransferStatus(t, server.transfers, taskID, "completed")
}

func TestResumeTransferConnectionFailurePreservesTask(t *testing.T) {
	server, taskID, providers := newResumeTestServer(t)
	server.connectProvider = func(request core.ConnectRequest) (core.ProviderInfo, error) {
		if request.ID == "target-session" {
			return core.ProviderInfo{}, errors.New("dial timeout")
		}
		server.manager.Add(providers[request.ID])
		return core.ProviderInfo{ID: request.ID, Connected: true}, nil
	}

	err := server.resumeTransfer(taskID)
	if err == nil {
		t.Fatal("resume unexpectedly succeeded")
	}
	task, ok := server.transfers.Get(taskID)
	if !ok || task.Status != "failed" || task.Error != "saved checkpoint" {
		t.Fatalf("connection failure changed task state: %#v", task)
	}
}

func newResumeTestServer(t *testing.T) (*Server, string, map[string]core.FileSystem) {
	t.Helper()
	dataDir := t.TempDir()
	sourceRoot := filepath.Join(dataDir, "source")
	targetRoot := filepath.Join(dataDir, "target")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceRoot, "empty.bin")
	if err := os.WriteFile(sourcePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetRoot, "empty.bin.part"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := newSessionStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []core.ConnectRequest{
		{ID: "source-session", Name: "源会话", Protocol: "ftp", Host: "source.test", User: "user"},
		{ID: "target-session", Name: "目标会话", Protocol: "sftp", Host: "target.test", User: "user", Fingerprint: "SHA256:test"},
	} {
		if _, err := store.Save(request); err != nil {
			t.Fatal(err)
		}
	}

	taskID := "resume-saved-sessions"
	task := core.TransferTask{
		ID: taskID, SourceProvider: "source-session", SourcePath: "/empty.bin",
		TargetProvider: "target-session", TargetPath: "/empty.bin", PartPath: "/empty.bin.part",
		Size: 0, SourceModified: info.ModTime(), BlockSize: 64 << 20, Concurrency: 1,
		Status: "failed", Error: "saved checkpoint", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	data, err := os.Create(filepath.Join(dataDir, "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.WriteString("[" + mustJSON(t, task) + "]"); err != nil {
		t.Fatal(err)
	}
	if err := data.Close(); err != nil {
		t.Fatal(err)
	}

	manager := core.NewManager()
	source, err := core.NewLocalFS("source-session", "Source", sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	target, err := core.NewLocalFS("target-session", "Target", targetRoot)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		dataDir: dataDir, manager: manager, sessionStore: store,
		transfers: core.NewTransferEngine(manager, filepath.Join(dataDir, "tasks.json")),
		activity:  newActivityLog(dataDir),
	}
	return server, taskID, map[string]core.FileSystem{"source-session": source, "target-session": target}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func waitForAppTransferStatus(t *testing.T, engine *core.TransferEngine, id, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if task, ok := engine.Get(id); ok && task.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := engine.Get(id)
	t.Fatalf("task did not reach %s: %#v", want, task)
}
