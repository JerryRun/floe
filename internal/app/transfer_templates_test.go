package app

import "testing"

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
