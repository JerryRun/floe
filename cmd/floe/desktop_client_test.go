package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"floe/internal/app"
	"floe/internal/core"
)

func TestDesktopClientSharesWebTransferQueue(t *testing.T) {
	server, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin, _, err := server.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	browser, err := http.Get(origin)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, browser.Body)
	_ = browser.Body.Close()
	if browser.StatusCode != http.StatusOK {
		t.Fatalf("Web UI status = %s", browser.Status)
	}

	client := newDesktopClient(origin)
	sourceDir, targetDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "示例.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(targetDir, "示例.txt")
	if err := os.WriteFile(targetPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	var source, target core.ProviderInfo
	for _, location := range []struct {
		dir      string
		provider *core.ProviderInfo
	}{{sourceDir, &source}, {targetDir, &target}} {
		if err := client.request(ctx, http.MethodPost, "/api/v1/local/tabs",
			map[string]string{"path": location.dir}, location.provider); err != nil {
			t.Fatal(err)
		}
	}

	request := core.TransferRequest{
		SourceProvider: source.ID, SourcePath: "/示例.txt",
		TargetProvider: target.ID, TargetPath: "/示例.txt",
		ConflictPolicy: core.ConflictAsk,
	}
	var conflict *desktopAPIError
	if err := client.request(ctx, http.MethodPost, "/api/v1/transfers", request, nil); !errors.As(err, &conflict) ||
		conflict.Code != "TRANSFER_CONFLICT" || conflict.TargetPath != "/示例.txt" {
		t.Fatalf("expected the target conflict, got %v", err)
	}
	request.ConflictPolicy = core.ConflictOverwrite
	var task core.TransferTask
	if err := client.request(ctx, http.MethodPost, "/api/v1/transfers", request, &task); err != nil {
		t.Fatal(err)
	}

	eventCtx, stopEvents := context.WithCancel(ctx)
	var completed bool
	err = client.events(eventCtx, func(tasks []core.TransferTask) {
		for _, item := range tasks {
			if item.ID == task.ID && item.Status == "completed" {
				completed = true
				stopEvents()
			}
		}
	}, nil)
	stopEvents()
	if !completed {
		t.Fatalf("completion missing from task events: %v", err)
	}

	// Closing the stream (as a desktop close does) leaves the web API and
	// its task history available to a separate connection.
	browserClient := newDesktopClient(origin)
	var taskList []core.TransferTask
	if err := browserClient.request(ctx, http.MethodGet, "/api/v1/transfers", nil, &taskList); err != nil {
		t.Fatal(err)
	}
	if len(taskList) != 1 || taskList[0].ID != task.ID || taskList[0].Status != "completed" {
		t.Fatalf("unexpected web queue: %+v", taskList)
	}
	if data, err := os.ReadFile(targetPath); err != nil || string(data) != "new" {
		t.Fatalf("destination = %q, %v", data, err)
	}
}
