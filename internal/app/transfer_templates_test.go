package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"floe/internal/core"
)

func TestTransferTemplateStoreRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	store, err := newTransferTemplateStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(TransferTemplate{Name: "FTP 发布", SourceProvider: "local", TargetProvider: "ftp", ConflictPolicy: "rename", Concurrency: 6, Verify: true, PreserveStructure: true, Filter: "*.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || len(store.List()) != 1 {
		t.Fatalf("saved template = %#v", saved)
	}
	reloaded, err := newTransferTemplateStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	items := reloaded.List()
	if len(items) != 1 || items[0].Name != "FTP 发布" || items[0].ConflictPolicy != "rename" || items[0].Filter != "*.zip" {
		t.Fatalf("reloaded templates = %#v", items)
	}
	if len(items[0].Tasks) != 1 || items[0].Tasks[0].SourcePath != "" {
		t.Fatalf("single-task compatibility payload = %#v", items[0].Tasks)
	}
	multi, err := reloaded.Save(TransferTemplate{Name: "多文件发布", Tasks: []TransferTemplateTask{
		{SourceProvider: "local", SourcePath: "/a.bin", TargetProvider: "ftp", TargetPath: "/a.bin", ConflictPolicy: "overwrite", Concurrency: 4, Verify: true, PreserveStructure: true},
		{SourceProvider: "local", SourcePath: "/b.bin", TargetProvider: "ftp", TargetPath: "/b.bin", ConflictPolicy: "skip", Concurrency: 2, Verify: true, PreserveStructure: true},
	}})
	if err != nil || len(multi.Tasks) != 2 {
		t.Fatalf("multi-task template = %#v, err = %v", multi, err)
	}
	if err := reloaded.Delete(saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Delete(multi.ID); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.List()) != 0 {
		t.Fatal("template was not deleted")
	}
}

func TestSaveTransferTemplatePersistsLocalProviderRoot(t *testing.T) {
	dataDir := t.TempDir()
	sourceRoot := filepath.Join(dataDir, "source")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	manager := core.NewManager()
	source, err := core.NewLocalFSWithKind("local-123", "target", sourceRoot, "local", "本地")
	if err != nil {
		t.Fatal(err)
	}
	manager.Add(source)
	templates, err := newTransferTemplateStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{manager: manager, templates: templates, activity: newActivityLog(dataDir)}
	payload := TransferTemplate{Name: "发布 renren-fast", Tasks: []TransferTemplateTask{{
		SourceProvider: "local-123", SourcePath: "/renren-fast.jar",
		TargetProvider: "airych", TargetPath: "/root/renren-fast.jar",
		ConflictPolicy: "overwrite", Concurrency: 4, Verify: true, PreserveStructure: true,
	}}}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/transfer-templates", bytes.NewReader(body))
	response := httptest.NewRecorder()
	server.saveTransferTemplate(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", response.Code, response.Body.String())
	}
	var saved TransferTemplate
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Tasks) != 1 {
		t.Fatalf("saved tasks = %#v", saved.Tasks)
	}
	task := saved.Tasks[0]
	if task.SourceProviderKind != "local" || task.SourceProviderLocation != sourceRoot || task.SourceProviderName != "target" {
		t.Fatalf("local provider metadata was not persisted: %#v", task)
	}
}

func TestRunTransferTemplateRestoresSavedLocalProvider(t *testing.T) {
	dataDir := t.TempDir()
	sourceRoot := filepath.Join(dataDir, "source")
	targetRoot := filepath.Join(dataDir, "target")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "renren-fast.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	templates, err := newTransferTemplateStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	template, err := templates.Save(TransferTemplate{Name: "发布 renren-fast", Tasks: []TransferTemplateTask{{
		SourceProvider: "local-123", SourceProviderName: "target", SourceProviderKind: "local", SourceProviderLocation: sourceRoot,
		SourcePath:     "/renren-fast.jar",
		TargetProvider: "local-456", TargetProviderName: "root", TargetProviderKind: "local", TargetProviderLocation: targetRoot,
		TargetPath:     "/renren-fast.jar",
		ConflictPolicy: "overwrite", Concurrency: 1, Verify: true, PreserveStructure: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	manager := core.NewManager()
	server := &Server{
		dataDir: dataDir, manager: manager, templates: templates,
		transfers: core.NewTransferEngine(manager, filepath.Join(dataDir, "tasks.json")),
		activity:  newActivityLog(dataDir),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/transfer-templates/"+template.ID+"/run", bytes.NewReader([]byte("{}")))
	response := httptest.NewRecorder()
	server.runTransferTemplate(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("run status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Tasks []core.TransferTask `json:"tasks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("run result = %#v", result)
	}
	waitForAppTransferStatus(t, server.transfers, result.Tasks[0].ID, "completed")
	data, err := os.ReadFile(filepath.Join(targetRoot, "renren-fast.jar"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "jar" {
		t.Fatalf("target content = %q", data)
	}
}
