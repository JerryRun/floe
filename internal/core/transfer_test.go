package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

type failingReplaceFS struct {
	FileSystem
	finalName string
}

func (f *failingReplaceFS) Replace(_, _ string) error {
	return errors.New("atomic replace unavailable")
}

func (f *failingReplaceFS) Rename(oldPath, newPath string) error {
	if strings.Contains(oldPath, ".floe-part-") && filepath.Base(newPath) == f.finalName {
		return errors.New("promotion deliberately failed")
	}
	return f.FileSystem.Rename(oldPath, newPath)
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

func TestTransferBlockFallsBackToReadBackWhenRemoteSHA256IsStale(t *testing.T) {
	sourceData := bytes.Repeat([]byte("remote-hash-fallback-"), 1024)
	source := &memoryFile{data: append([]byte(nil), sourceData...)}
	target := &memoryFile{data: make([]byte, len(sourceData))}
	block := BlockState{Offset: 0, Length: int64(len(sourceData))}
	stale := sha256.Sum256([]byte("stale remote view"))

	digest, err := transferBlockProgress(context.Background(), source, target, target, block, nil, func() ([]byte, error) {
		return stale[:], nil
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

func TestTransferWorkerSlotsAreIsolatedByTargetProvider(t *testing.T) {
	engine := NewTransferEngine(NewManager(), filepath.Join(t.TempDir(), "tasks.json"))
	ctx := context.Background()
	ftpReleases := make([]func(), 0, 8)
	for i := 0; i < 8; i++ {
		release, err := engine.acquireTransferSlot(ctx, "ftp")
		if err != nil {
			t.Fatal(err)
		}
		ftpReleases = append(ftpReleases, release)
	}
	defer func() {
		for _, release := range ftpReleases {
			release()
		}
	}()

	// A saturated FTP queue must not prevent an SFTP target from starting.
	sftpRelease, err := engine.acquireTransferSlot(ctx, "sftp")
	if err != nil {
		t.Fatalf("SFTP slot was blocked by FTP workers: %v", err)
	}
	sftpRelease()

	blockedCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := engine.acquireTransferSlot(blockedCtx, "ftp"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("FTP slot was not saturated, got %v", err)
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
				if current.BytesRead != int64(len(content)) || current.BytesWritten != int64(len(content)) {
					t.Fatalf("I/O counters = read %d, written %d; want %d each", current.BytesRead, current.BytesWritten, len(content))
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("transfer did not complete before deadline")
}

func TestTransferConflictPolicies(t *testing.T) {
	root := t.TempDir()
	sourceRoot, targetRoot := filepath.Join(root, "source"), filepath.Join(root, "target")
	source, _ := NewLocalFS("source", "source", sourceRoot)
	target, _ := NewLocalFS("target", "target", targetRoot)
	manager := NewManager()
	manager.Add(source)
	manager.Add(target)
	content := []byte("new content")
	if err := os.WriteFile(filepath.Join(sourceRoot, "file.txt"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetRoot, "file.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := NewTransferEngine(manager, filepath.Join(root, "tasks.json"))
	skipped, err := engine.Create(TransferRequest{SourceProvider: "source", SourcePath: "/file.txt", TargetProvider: "target", TargetPath: "/file.txt", ConflictPolicy: ConflictSkip})
	if err != nil || skipped.Status != "skipped" {
		t.Fatalf("skip policy task = %#v, err = %v", skipped, err)
	}
	kept, _ := os.ReadFile(filepath.Join(targetRoot, "file.txt"))
	if string(kept) != "keep" {
		t.Fatalf("skip policy changed target: %q", kept)
	}
	rename, err := engine.Create(TransferRequest{SourceProvider: "source", SourcePath: "/file.txt", TargetProvider: "target", TargetPath: "/file.txt", ConflictPolicy: ConflictRename})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := engine.Get(rename.ID)
		if ok && current.Status == "completed" {
			if current.TargetPath != "/file (1).txt" {
				t.Fatalf("renamed target = %q", current.TargetPath)
			}
			got, readErr := os.ReadFile(filepath.Join(targetRoot, "file (1).txt"))
			if readErr != nil || !bytes.Equal(got, content) {
				t.Fatalf("renamed content = %q, err = %v", got, readErr)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := engine.Get(rename.ID)
	if current.Status != "completed" {
		t.Fatalf("rename task did not complete: %s (%s)", current.Status, current.Error)
	}
	var conflict *TransferConflictError
	if _, err := engine.Create(TransferRequest{SourceProvider: "source", SourcePath: "/file.txt", TargetProvider: "target", TargetPath: "/file.txt", ConflictPolicy: ConflictAsk}); !errors.As(err, &conflict) {
		t.Fatalf("ask policy error = %v", err)
	}
}

func TestEngineConcurrentCreatesUseUniqueTaskIDs(t *testing.T) {
	root := t.TempDir()
	sourceRoot, targetRoot := filepath.Join(root, "source"), filepath.Join(root, "target")
	source, _ := NewLocalFS("source", "Source", sourceRoot)
	target, _ := NewLocalFS("target", "Target", targetRoot)
	manager := NewManager()
	manager.Add(source)
	manager.Add(target)
	for _, name := range []string{"one.bin", "two.bin"} {
		if err := os.WriteFile(filepath.Join(sourceRoot, name), bytes.Repeat([]byte(name), 1024), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	engine := NewTransferEngine(manager, filepath.Join(root, "tasks.json"))
	type result struct {
		task TransferTask
		err  error
	}
	results := make(chan result, 2)
	for _, name := range []string{"one.bin", "two.bin"} {
		go func(name string) {
			task, err := engine.Create(TransferRequest{
				SourceProvider: "source", SourcePath: "/" + name,
				TargetProvider: "target", TargetPath: "/" + name, Concurrency: 1,
			})
			results <- result{task: task, err: err}
		}(name)
	}
	first, second := <-results, <-results
	if first.err != nil {
		t.Fatal(first.err)
	}
	if second.err != nil {
		t.Fatal(second.err)
	}
	if first.task.ID == second.task.ID {
		t.Fatalf("concurrent creates reused task ID %q", first.task.ID)
	}
	waitForTaskStatus(t, engine, first.task.ID, "completed")
	waitForTaskStatus(t, engine, second.task.ID, "completed")
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

func TestEnginePreservesExistingTargetWhenPromotionFails(t *testing.T) {
	root := t.TempDir()
	sourceRoot, targetRoot := filepath.Join(root, "source"), filepath.Join(root, "target")
	source, _ := NewLocalFS("source", "Source", sourceRoot)
	targetLocal, _ := NewLocalFS("target", "Target", targetRoot)
	old := []byte("old complete target")
	if err := os.WriteFile(filepath.Join(targetRoot, "file.bin"), old, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "file.bin"), bytes.Repeat([]byte("new data"), 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	target := &failingReplaceFS{FileSystem: targetLocal, finalName: "file.bin"}
	manager := NewManager()
	manager.Add(source)
	manager.Add(target)
	engine := NewTransferEngine(manager, filepath.Join(root, "tasks.json"))
	task, err := engine.Create(TransferRequest{SourceProvider: "source", SourcePath: "/file.bin", TargetProvider: "target", TargetPath: "/file.bin", Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if task.PartPath == task.TargetPath || !strings.Contains(task.PartPath, ".floe-part-") {
		t.Fatalf("task did not use an isolated temporary path: %#v", task)
	}
	waitForTaskStatus(t, engine, task.ID, "failed")
	got, err := os.ReadFile(filepath.Join(targetRoot, "file.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, old) {
		t.Fatal("failed promotion replaced the previous complete target")
	}
}

func TestChooseBlockSizeKeepsEnoughResumeRanges(t *testing.T) {
	tests := []struct {
		size, want  int64
		concurrency int
	}{
		{size: 1 << 20, concurrency: 4, want: minBlockSize},
		{size: 256 << 20, concurrency: 4, want: 16 << 20},
		{size: 8 << 30, concurrency: 4, want: defaultBlockSize},
	}
	for _, test := range tests {
		if got := chooseBlockSize(test.size, test.concurrency); got != test.want {
			t.Fatalf("chooseBlockSize(%d, %d) = %d, want %d", test.size, test.concurrency, got, test.want)
		}
	}
}

func BenchmarkTransferBlockProgress(b *testing.B) {
	data := bytes.Repeat([]byte("floe-throughput\n"), 512*1024)
	source := &memoryFile{data: data}
	block := BlockState{Offset: 0, Length: int64(len(data))}
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		target := &memoryFile{data: make([]byte, len(data))}
		if _, err := transferBlock(context.Background(), source, target, target, block); err != nil {
			b.Fatal(err)
		}
	}
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
