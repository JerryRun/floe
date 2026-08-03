package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type memoryFile struct{ data []byte }

type failingReader struct{}

func (failingReader) ReadAt([]byte, int64) (int, error) {
	return 0, errors.New("read-back should not be used")
}

type slowFileSystem struct {
	FileSystem
	delay time.Duration
}

type slowReader struct {
	ReadAtCloser
	delay time.Duration
}

type recordingWriteFS struct {
	FileSystem
	truncateValues []int64
}

type readLimitedFS struct {
	FileSystem
	limit int
}

func (r readLimitedFS) MaxConcurrentReads() int { return r.limit }

func (r *recordingWriteFS) OpenWrite(path string, truncateTo *int64) (WriteAtCloser, error) {
	if truncateTo != nil {
		r.truncateValues = append(r.truncateValues, *truncateTo)
	}
	return r.FileSystem.OpenWrite(path, truncateTo)
}

func (s slowFileSystem) OpenRead(path string) (ReadAtCloser, error) {
	reader, err := s.FileSystem.OpenRead(path)
	if err != nil {
		return nil, err
	}
	return &slowReader{ReadAtCloser: reader, delay: s.delay}, nil
}

func (s *slowReader) ReadAt(p []byte, offset int64) (int, error) {
	time.Sleep(s.delay)
	return s.ReadAtCloser.ReadAt(p, offset)
}

func (m *memoryFile) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(m.data).ReadAt(p, off)
}

func (m *memoryFile) WriteAt(p []byte, off int64) (int, error) {
	copy(m.data[int(off):], p)
	return len(p), nil
}

func TestTransferBlockWritesAtFixedOffsetAndVerifiesSHA256(t *testing.T) {
	sourceData := bytes.Repeat([]byte("floe-data-"), 4096)
	source := &memoryFile{data: append([]byte(nil), sourceData...)}
	target := &memoryFile{data: bytes.Repeat([]byte{0xaa}, len(sourceData))}
	block := BlockState{Index: 1, Offset: 1024, Length: int64(len(sourceData) - 2048)}

	digest, err := transferBlock(context.Background(), source, target, target, block)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(sourceData[block.Offset : block.Offset+block.Length])
	if digest != hex.EncodeToString(want[:]) {
		t.Fatalf("digest = %s, want %x", digest, want)
	}
	if !bytes.Equal(target.data[block.Offset:block.Offset+block.Length], sourceData[block.Offset:block.Offset+block.Length]) {
		t.Fatal("target block differs from source block")
	}
	if target.data[0] != 0xaa || target.data[len(target.data)-1] != 0xaa {
		t.Fatal("transfer wrote outside the assigned block offset")
	}
}

func TestTransferBlockUsesRemoteSHA256WithoutReadingTargetBack(t *testing.T) {
	sourceData := bytes.Repeat([]byte("remote-hash-"), 2048)
	source := &memoryFile{data: append([]byte(nil), sourceData...)}
	target := &memoryFile{data: make([]byte, len(sourceData))}
	block := BlockState{Offset: 0, Length: int64(len(sourceData))}

	digest, err := transferBlockProgress(context.Background(), source, target, failingReader{}, block, nil, func() ([]byte, error) {
		hash := sha256.Sum256(target.data)
		return hash[:], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(sourceData)
	if digest != hex.EncodeToString(want[:]) {
		t.Fatalf("digest = %s, want %x", digest, want)
	}
}

func TestPartitionBlocksKeepsEachWorkerRangeContiguous(t *testing.T) {
	blocks := make([]BlockState, 10)
	for index := range blocks {
		blocks[index] = BlockState{Index: index, Offset: int64(index) * defaultBlockSize, Length: defaultBlockSize}
	}
	partitions := partitionBlocks(blocks, 4)
	if len(partitions) != 4 {
		t.Fatalf("partition count = %d, want 4", len(partitions))
	}
	seen := 0
	for _, partition := range partitions {
		for index, block := range partition {
			if index > 0 && block.Index != partition[index-1].Index+1 {
				t.Fatalf("non-contiguous partition: %#v", partition)
			}
			seen++
		}
	}
	if seen != len(blocks) {
		t.Fatalf("partitioned blocks = %d, want %d", seen, len(blocks))
	}
}

func TestEngineCopiesLocalFileAndPersistsVerifiedTask(t *testing.T) {
	root := t.TempDir()
	sourceRoot, targetRoot := filepath.Join(root, "a"), filepath.Join(root, "b")
	source, _ := NewLocalFS("a", "A", sourceRoot)
	target, _ := NewLocalFS("b", "B", targetRoot)
	manager := NewManager()
	manager.Add(source)
	manager.Add(target)
	content := bytes.Repeat([]byte("verified-block\n"), 128*1024)
	if err := os.WriteFile(filepath.Join(sourceRoot, "source.bin"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	engine := NewTransferEngine(manager, filepath.Join(root, "tasks.json"))
	task, err := engine.Create(TransferRequest{
		SourceProvider: "a", SourcePath: "/source.bin", TargetProvider: "b", TargetPath: "/copied.bin", Concurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, current := range engine.List() {
			if current.ID == task.ID && current.Status == "completed" {
				got, err := os.ReadFile(filepath.Join(targetRoot, "copied.bin"))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, content) {
					t.Fatal("completed target differs from source")
				}
				if len(current.Blocks) != 1 || !current.Blocks[0].Verified || current.Blocks[0].SHA256 == "" {
					t.Fatal("block verification state was not persisted")
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("transfer did not complete before deadline")
}

func TestEngineCreatesPartFileWithoutPreallocatingFullSourceSize(t *testing.T) {
	root := t.TempDir()
	sourceRoot, targetRoot := filepath.Join(root, "source"), filepath.Join(root, "target")
	source, _ := NewLocalFS("source", "Source", sourceRoot)
	targetLocal, _ := NewLocalFS("target", "Target", targetRoot)
	target := &recordingWriteFS{FileSystem: targetLocal}
	manager := NewManager()
	manager.Add(source)
	manager.Add(target)
	if err := os.WriteFile(filepath.Join(sourceRoot, "large.bin"), bytes.Repeat([]byte("floe"), 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := NewTransferEngine(manager, filepath.Join(root, "tasks.json"))
	task, err := engine.Create(TransferRequest{
		SourceProvider: "source", SourcePath: "/large.bin", TargetProvider: "target", TargetPath: "/large.bin", Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(target.truncateValues) == 0 || target.truncateValues[0] != 0 {
		t.Fatalf("initial part-file truncate values = %v, want first value 0", target.truncateValues)
	}
	waitForTaskStatus(t, engine, task.ID, "completed")
}

func TestEnginePauseResumeAndDelete(t *testing.T) {
	root := t.TempDir()
	sourceRoot, targetRoot := filepath.Join(root, "a"), filepath.Join(root, "b")
	sourceLocal, _ := NewLocalFS("a", "A", sourceRoot)
	target, _ := NewLocalFS("b", "B", targetRoot)
	source := slowFileSystem{FileSystem: sourceLocal, delay: 12 * time.Millisecond}
	manager := NewManager()
	manager.Add(source)
	manager.Add(target)
	content := bytes.Repeat([]byte("floe-pause-resume\n"), 1024*1024)
	if err := os.WriteFile(filepath.Join(sourceRoot, "source.bin"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	engine := NewTransferEngine(manager, filepath.Join(root, "tasks.json"))

	task, err := engine.Create(TransferRequest{
		SourceProvider: "a", SourcePath: "/source.bin", TargetProvider: "b", TargetPath: "/resumed.bin", Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Pause(task.ID); err != nil {
		t.Fatal(err)
	}
	if status := taskStatus(engine, task.ID); status != "paused" {
		t.Fatalf("status after pause = %q, want paused", status)
	}
	if err := engine.Resume(task.ID); err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, engine, task.ID, "completed")
	got, err := os.ReadFile(filepath.Join(targetRoot, "resumed.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("resumed target differs from source")
	}

	deleted, err := engine.Create(TransferRequest{
		SourceProvider: "a", SourcePath: "/source.bin", TargetProvider: "b", TargetPath: "/deleted.bin", Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Delete(deleted.ID); err != nil {
		t.Fatal(err)
	}
	if status := taskStatus(engine, deleted.ID); status != "" {
		t.Fatalf("deleted task still listed with status %q", status)
	}
}

func TestEngineGetReturnsSnapshotAndResumeKeepsStatusWhenProviderDisconnected(t *testing.T) {
	root := t.TempDir()
	manager := NewManager()
	engine := NewTransferEngine(manager, filepath.Join(root, "tasks.json"))
	engine.tasks["resume-test"] = &TransferTask{
		ID: "resume-test", SourceProvider: "missing-source", TargetProvider: "missing-target",
		Status: "failed", Error: "previous network failure",
		Blocks: []BlockState{{Index: 0, Offset: 0, Length: 64, Verified: true}},
	}

	snapshot, ok := engine.Get("resume-test")
	if !ok {
		t.Fatal("Get did not return the task")
	}
	snapshot.Blocks[0].Verified = false
	if current, _ := engine.Get("resume-test"); !current.Blocks[0].Verified {
		t.Fatal("Get returned mutable task state")
	}
	if err := engine.Resume("resume-test"); err == nil || err.Error() != "source provider is disconnected" {
		t.Fatalf("Resume error = %v, want disconnected source", err)
	}
	current, _ := engine.Get("resume-test")
	if current.Status != "failed" || current.Error != "previous network failure" {
		t.Fatalf("disconnected resume changed task state: %#v", current)
	}
}

func TestEngineHonorsSourceConcurrentReadLimit(t *testing.T) {
	root := t.TempDir()
	sourceRoot, targetRoot := filepath.Join(root, "source"), filepath.Join(root, "target")
	sourceLocal, err := NewLocalFS("source", "Source", sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	target, err := NewLocalFS("target", "Target", targetRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "limited.bin"), bytes.Repeat([]byte("floe"), 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	manager.Add(readLimitedFS{FileSystem: sourceLocal, limit: 2})
	manager.Add(target)
	engine := NewTransferEngine(manager, filepath.Join(root, "tasks.json"))
	task, err := engine.Create(TransferRequest{
		SourceProvider: "source", SourcePath: "/limited.bin",
		TargetProvider: "target", TargetPath: "/limited.bin", Concurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Concurrency != 2 {
		t.Fatalf("task concurrency = %d, want source limit 2", task.Concurrency)
	}
	waitForTaskStatus(t, engine, task.ID, "completed")
}

func taskStatus(engine *TransferEngine, id string) string {
	for _, task := range engine.List() {
		if task.ID == id {
			return task.Status
		}
	}
	return ""
}

func waitForTaskStatus(t *testing.T, engine *TransferEngine, id, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if taskStatus(engine, id) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach %s (last status %s)", id, want, taskStatus(engine, id))
}

func TestLocalFSRejectsPathTraversal(t *testing.T) {
	provider, err := NewLocalFS("test", "test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.resolve("../../outside"); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestEngineClearOnlyRemovesRequestedHistory(t *testing.T) {
	root := t.TempDir()
	engine := NewTransferEngine(NewManager(), filepath.Join(root, "tasks.json"))
	engine.tasks["success"] = &TransferTask{ID: "success", Status: "completed"}
	engine.tasks["failed"] = &TransferTask{ID: "failed", Status: "failed"}
	engine.tasks["paused"] = &TransferTask{ID: "paused", Status: "paused"}
	removed, err := engine.Clear("completed")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || taskStatus(engine, "success") != "" || taskStatus(engine, "failed") != "failed" || taskStatus(engine, "paused") != "paused" {
		t.Fatalf("unexpected tasks after clearing completed: %#v", engine.List())
	}
	removed, err = engine.Clear("failed")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || taskStatus(engine, "failed") != "" || taskStatus(engine, "paused") != "paused" {
		t.Fatalf("unexpected tasks after clearing failed: %#v", engine.List())
	}
	if _, err := engine.Clear("running"); err == nil {
		t.Fatal("clearing running tasks should be rejected")
	}
}
