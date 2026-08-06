package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultBlockSize   = int64(64 << 20)
	transferBufferSize = 1 << 20
	progressStep       = int64(4 << 20)
)

type BlockState struct {
	Index    int    `json:"index"`
	Offset   int64  `json:"offset"`
	Length   int64  `json:"length"`
	SHA256   string `json:"sha256,omitempty"`
	Verified bool   `json:"verified"`
}

type TransferTask struct {
	ID               string       `json:"id"`
	SourceProvider   string       `json:"source_provider"`
	SourcePath       string       `json:"source_path"`
	TargetProvider   string       `json:"target_provider"`
	TargetPath       string       `json:"target_path"`
	PartPath         string       `json:"part_path"`
	Size             int64        `json:"size"`
	SourceModified   time.Time    `json:"source_modified"`
	BlockSize        int64        `json:"block_size"`
	Concurrency      int          `json:"concurrency"`
	BytesVerified    int64        `json:"bytes_verified"`
	BytesTransferred int64        `json:"bytes_transferred"`
	Status           string       `json:"status"`
	Error            string       `json:"error,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	Blocks           []BlockState `json:"blocks,omitempty"`
}

type TransferRequest struct {
	SourceProvider string `json:"source_provider"`
	SourcePath     string `json:"source_path"`
	TargetProvider string `json:"target_provider"`
	TargetPath     string `json:"target_path"`
	Concurrency    int    `json:"concurrency"`
}

type TransferEngine struct {
	mu          sync.RWMutex
	manager     *Manager
	tasks       map[string]*TransferTask
	cancels     map[string]context.CancelFunc
	storePath   string
	subscribers map[chan struct{}]struct{}
	slots       chan struct{}
	// finalizeMu prevents a burst of directory transfers from issuing
	// competing remove/rename requests on the same remote control channel.
	// Some SFTP/FTP servers otherwise acknowledge the write before the .part
	// entry is visible to the following rename request.
	finalizeMu sync.Mutex
}

func NewTransferEngine(manager *Manager, storePath string) *TransferEngine {
	e := &TransferEngine{
		manager: manager, tasks: make(map[string]*TransferTask), cancels: make(map[string]context.CancelFunc),
		storePath: storePath, subscribers: make(map[chan struct{}]struct{}),
		slots: make(chan struct{}, 8),
	}
	e.load()
	return e
}

func (e *TransferEngine) load() {
	data, err := os.ReadFile(e.storePath)
	if err != nil {
		return
	}
	var tasks []*TransferTask
	if json.Unmarshal(data, &tasks) != nil {
		return
	}
	for _, task := range tasks {
		if task.Status == "running" || task.Status == "verifying" {
			task.Status = "paused"
			task.Error = "application restarted; resume to continue"
		}
		if task.BytesTransferred < task.BytesVerified {
			task.BytesTransferred = task.BytesVerified
		}
		e.tasks[task.ID] = task
	}
}

func (e *TransferEngine) saveLocked() {
	tasks := make([]*TransferTask, 0, len(e.tasks))
	for _, task := range e.tasks {
		tasks = append(tasks, task)
	}
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(e.storePath), 0o755)
	tmp := e.storePath + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, e.storePath)
	}
}

func cloneTask(task *TransferTask) TransferTask {
	copy := *task
	copy.Blocks = append([]BlockState(nil), task.Blocks...)
	return copy
}

func (e *TransferEngine) List() []TransferTask {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]TransferTask, 0, len(e.tasks))
	for _, task := range e.tasks {
		result = append(result, cloneTask(task))
	}
	return result
}

// Get returns an immutable snapshot of a task. Callers use it to prepare
// provider connections before starting or resuming work.
func (e *TransferEngine) Get(id string) (TransferTask, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	task := e.tasks[id]
	if task == nil {
		return TransferTask{}, false
	}
	return cloneTask(task), true
}

func (e *TransferEngine) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	e.mu.Lock()
	e.subscribers[ch] = struct{}{}
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		delete(e.subscribers, ch)
		close(ch)
		e.mu.Unlock()
	}
}

func (e *TransferEngine) notifyLocked() {
	for ch := range e.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (e *TransferEngine) update(id string, fn func(*TransferTask)) {
	e.mu.Lock()
	if task := e.tasks[id]; task != nil {
		fn(task)
		task.UpdatedAt = time.Now()
		e.saveLocked()
		e.notifyLocked()
	}
	e.mu.Unlock()
}

func (e *TransferEngine) Create(req TransferRequest) (TransferTask, error) {
	source, ok := e.manager.Get(req.SourceProvider)
	if !ok {
		return TransferTask{}, errors.New("source provider is not connected")
	}
	target, ok := e.manager.Get(req.TargetProvider)
	if !ok {
		return TransferTask{}, errors.New("target provider is not connected")
	}
	info, err := source.Stat(req.SourcePath)
	if err != nil {
		return TransferTask{}, err
	}
	if req.Concurrency < 1 {
		req.Concurrency = 4
	}
	if req.Concurrency > 8 {
		req.Concurrency = 8
	}
	if limiter, ok := source.(ConcurrentReadLimiter); ok {
		if limit := limiter.MaxConcurrentReads(); limit > 0 && req.Concurrency > limit {
			req.Concurrency = limit
		}
	}
	if limiter, ok := target.(ConcurrentWriteLimiter); ok {
		if limit := limiter.MaxConcurrentWrites(); limit > 0 && req.Concurrency > limit {
			req.Concurrency = limit
		}
	}
	// SFTP servers in the field may remove or hide a temporary file as soon as
	// its write handle closes. Write SFTP targets directly to the final path;
	// the block hash verification still prevents a task from being completed
	// until every byte is correct. Other providers retain atomic temp files.
	now := time.Now()
	taskID := fmt.Sprintf("transfer-%d", now.UnixNano())
	partPath := req.TargetPath
	if target.Kind() != "sftp" {
		// Directory uploads can overlap with an explicitly selected child
		// file; never let tasks share a temporary path.
		partPath += ".part-" + taskID
	}
	zero := int64(0)
	// A directory task and an explicitly selected child can arrive at the
	// same time. Do not create two writers for one destination; return the
	// already queued task instead. The lock intentionally covers preparation
	// so two concurrent HTTP requests cannot pass this check together.
	e.mu.Lock()
	for _, existing := range e.tasks {
		if existing.TargetProvider == req.TargetProvider && existing.TargetPath == req.TargetPath &&
			existing.Status != "completed" && existing.Status != "failed" {
			snapshot := cloneTask(existing)
			e.mu.Unlock()
			return snapshot, nil
		}
	}
	w, err := target.OpenWrite(partPath, &zero)
	if err != nil {
		e.mu.Unlock()
		return TransferTask{}, fmt.Errorf("prepare target: %w", err)
	}
	if err := w.Close(); err != nil {
		e.mu.Unlock()
		return TransferTask{}, fmt.Errorf("prepare target close: %w", err)
	}

	blockCount := int((info.Size + defaultBlockSize - 1) / defaultBlockSize)
	blocks := make([]BlockState, blockCount)
	for i := range blocks {
		offset := int64(i) * defaultBlockSize
		length := min(defaultBlockSize, info.Size-offset)
		blocks[i] = BlockState{Index: i, Offset: offset, Length: length}
	}
	task := &TransferTask{
		ID: taskID, SourceProvider: req.SourceProvider,
		SourcePath: req.SourcePath, TargetProvider: req.TargetProvider, TargetPath: req.TargetPath,
		PartPath: partPath, Size: info.Size, SourceModified: info.Modified, BlockSize: defaultBlockSize,
		Concurrency: req.Concurrency, Status: "running", CreatedAt: now, UpdatedAt: now, Blocks: blocks,
	}
	e.tasks[task.ID] = task
	e.saveLocked()
	e.notifyLocked()
	e.mu.Unlock()
	e.start(task.ID)
	return cloneTask(task), nil
}

func (e *TransferEngine) start(id string) {
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	e.cancels[id] = cancel
	if task := e.tasks[id]; task != nil {
		task.Status = "running"
		task.Error = ""
		task.BytesTransferred = task.BytesVerified
		task.UpdatedAt = time.Now()
		e.saveLocked()
		e.notifyLocked()
	}
	e.mu.Unlock()
	go e.run(ctx, id)
}

func (e *TransferEngine) Pause(id string) error {
	e.mu.Lock()
	task := e.tasks[id]
	if task == nil {
		e.mu.Unlock()
		return errors.New("task not found")
	}
	cancel := e.cancels[id]
	if cancel == nil {
		e.mu.Unlock()
		return errors.New("task is not running")
	}
	delete(e.cancels, id)
	task.Status = "paused"
	task.UpdatedAt = time.Now()
	e.saveLocked()
	e.notifyLocked()
	e.mu.Unlock()
	cancel()
	return nil
}

func (e *TransferEngine) Resume(id string) error {
	e.mu.RLock()
	task := e.tasks[id]
	_, running := e.cancels[id]
	var snapshot TransferTask
	if task != nil {
		snapshot = cloneTask(task)
	}
	e.mu.RUnlock()
	if task == nil {
		return errors.New("task not found")
	}
	if running {
		return errors.New("task is already running")
	}
	if snapshot.Status == "completed" {
		return errors.New("task is already completed")
	}
	if _, ok := e.manager.Get(snapshot.SourceProvider); !ok {
		return errors.New("source provider is disconnected")
	}
	if _, ok := e.manager.Get(snapshot.TargetProvider); !ok {
		return errors.New("target provider is disconnected")
	}
	e.start(id)
	return nil
}

func (e *TransferEngine) Delete(id string) error {
	e.mu.Lock()
	task := e.tasks[id]
	if task == nil {
		e.mu.Unlock()
		return errors.New("task not found")
	}
	if cancel := e.cancels[id]; cancel != nil {
		cancel()
		delete(e.cancels, id)
	}
	delete(e.tasks, id)
	e.saveLocked()
	e.notifyLocked()
	e.mu.Unlock()
	return nil
}

func (e *TransferEngine) Clear(status string) (int, error) {
	if status != "completed" && status != "failed" {
		return 0, errors.New("only completed or failed tasks can be cleared")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	removed := 0
	for id, task := range e.tasks {
		if task.Status != status {
			continue
		}
		if cancel := e.cancels[id]; cancel != nil {
			cancel()
			delete(e.cancels, id)
		}
		delete(e.tasks, id)
		removed++
	}
	if removed > 0 {
		e.saveLocked()
		e.notifyLocked()
	}
	return removed, nil
}

func (e *TransferEngine) run(ctx context.Context, id string) {
	e.mu.RLock()
	if e.tasks[id] == nil {
		e.mu.RUnlock()
		return
	}
	task := cloneTask(e.tasks[id])
	e.mu.RUnlock()
	source, sourceOK := e.manager.Get(task.SourceProvider)
	target, targetOK := e.manager.Get(task.TargetProvider)
	if !sourceOK || !targetOK {
		e.fail(id, errors.New("source or target provider is disconnected"))
		return
	}
	if limiter, ok := source.(ConcurrentReadLimiter); ok {
		if limit := limiter.MaxConcurrentReads(); limit > 0 && task.Concurrency > limit {
			task.Concurrency = limit
			e.update(id, func(saved *TransferTask) { saved.Concurrency = limit })
		}
	}
	info, err := source.Stat(task.SourcePath)
	if err != nil || info.Size != task.Size || info.Modified.Unix() != task.SourceModified.Unix() {
		if err == nil {
			err = errors.New("source file changed since the task was created")
		}
		e.fail(id, err)
		return
	}

	// Previously verified blocks are re-read after restart/resume so the local
	// manifest cannot hide an externally modified .part file.
	hasVerifiedBlocks := false
	for _, block := range task.Blocks {
		if block.Verified && block.SHA256 != "" {
			hasVerifiedBlocks = true
			break
		}
	}
	if hasVerifiedBlocks {
		e.update(id, func(task *TransferTask) { task.Status = "verifying" })
	}
	for i, block := range task.Blocks {
		if !block.Verified || block.SHA256 == "" {
			continue
		}
		valid, err := verifyRange(ctx, target, task.PartPath, block)
		if err != nil || !valid {
			e.update(id, func(t *TransferTask) {
				t.Blocks[i].Verified = false
				t.BytesVerified -= block.Length
				if t.BytesVerified < 0 {
					t.BytesVerified = 0
				}
			})
		}
	}
	if hasVerifiedBlocks {
		e.update(id, func(task *TransferTask) { task.Status = "running" })
	}

	e.mu.RLock()
	stored := e.tasks[id]
	if stored == nil {
		e.mu.RUnlock()
		return
	}
	task = cloneTask(stored)
	e.mu.RUnlock()
	pending := make([]BlockState, 0, len(task.Blocks))
	for _, block := range task.Blocks {
		if !block.Verified {
			pending = append(pending, block)
		}
	}
	partitions := partitionBlocks(pending, task.Concurrency)
	errCh := make(chan error, task.Concurrency)
	var wg sync.WaitGroup
	for _, blocks := range partitions {
		jobs := make(chan BlockState, len(blocks))
		for _, block := range blocks {
			jobs <- block
		}
		close(jobs)
		wg.Add(1)
		go func(jobs <-chan BlockState) {
			defer wg.Done()
			if err := e.worker(ctx, id, source, target, task.SourcePath, task.PartPath, jobs); err != nil && !errors.Is(err, context.Canceled) {
				select {
				case errCh <- err:
				default:
				}
			}
		}(jobs)
	}
	wg.Wait()
	select {
	case err := <-errCh:
		e.fail(id, err)
		return
	default:
	}
	if ctx.Err() != nil {
		return
	}
	finalInfo, err := source.Stat(task.SourcePath)
	if err != nil || finalInfo.Size != task.Size || finalInfo.Modified.Unix() != task.SourceModified.Unix() {
		e.fail(id, errors.New("source file changed during transfer"))
		return
	}
	if task.PartPath != task.TargetPath {
		if err := e.finalize(ctx, target, task.PartPath, task.TargetPath, task.Size); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			e.fail(id, fmt.Errorf("finalize target (%s → %s): %w", task.PartPath, task.TargetPath, err))
			return
		}
	}
	e.update(id, func(task *TransferTask) {
		task.Status = "completed"
		task.BytesVerified = task.Size
		task.Error = ""
	})
	e.mu.Lock()
	delete(e.cancels, id)
	e.mu.Unlock()
}

func (e *TransferEngine) finalize(ctx context.Context, target FileSystem, partPath, targetPath string, expectedSize int64) error {
	e.finalizeMu.Lock()
	defer e.finalizeMu.Unlock()
	var lastErr error
	diagnostics := make([]string, 0, 8)
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Removing an existing target is best effort: a missing destination is
		// the normal case and must not prevent the atomic rename.
		if err := target.Remove(targetPath); err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("attempt %d remove target: %v", attempt+1, err))
			if _, statErr := target.Stat(targetPath); statErr == nil {
				lastErr = err
				if attempt < 3 {
					if !waitFinalizeRetry(ctx, time.Duration(attempt+1)*75*time.Millisecond) {
						return ctx.Err()
					}
					continue
				}
				return err
			}
		}
		partInfo, partStatErr := target.Stat(partPath)
		if partStatErr != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("attempt %d part stat: %v", attempt+1, partStatErr))
		} else {
			diagnostics = append(diagnostics, fmt.Sprintf("attempt %d part stat: size=%d dir=%t", attempt+1, partInfo.Size, partInfo.IsDir))
		}
		if err := target.Rename(partPath, targetPath); err == nil {
			return nil
		} else {
			lastErr = err
			diagnostics = append(diagnostics, fmt.Sprintf("attempt %d rename: %v", attempt+1, err))
			// A lost SFTP reply can report an error even though the server
			// completed the rename. Confirm the destination before retrying.
			if info, statErr := target.Stat(targetPath); statErr == nil {
				diagnostics = append(diagnostics, fmt.Sprintf("attempt %d target stat: size=%d dir=%t", attempt+1, info.Size, info.IsDir))
				if !info.IsDir && info.Size == expectedSize {
					return nil
				}
			} else {
				diagnostics = append(diagnostics, fmt.Sprintf("attempt %d target stat: %v", attempt+1, statErr))
			}
		}
		if attempt < 3 {
			if !waitFinalizeRetry(ctx, time.Duration(attempt+1)*75*time.Millisecond) {
				return ctx.Err()
			}
		}
	}
	if len(diagnostics) > 0 {
		return fmt.Errorf("%w; %s", lastErr, strings.Join(diagnostics, " | "))
	}
	return lastErr
}

func waitFinalizeRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// partitionBlocks gives each worker one contiguous range. In particular, an
// FTP reader can keep a single RETR stream open instead of seeking and opening
// a new data connection for every interleaved block.
func partitionBlocks(blocks []BlockState, concurrency int) [][]BlockState {
	if len(blocks) == 0 || concurrency < 1 {
		return nil
	}
	workers := min(concurrency, len(blocks))
	result := make([][]BlockState, workers)
	for index, block := range blocks {
		worker := index * workers / len(blocks)
		result[worker] = append(result[worker], block)
	}
	return result
}

func (e *TransferEngine) worker(ctx context.Context, id string, source, target FileSystem, sourcePath, partPath string, jobs <-chan BlockState) error {
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if controller, ok := target.(WriteSlotController); ok {
		release, err := controller.AcquireWriteSlot(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	if controller, ok := source.(ReadSlotController); ok {
		release, err := controller.AcquireReadSlot(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	src, err := source.OpenRead(sourcePath)
	if err != nil {
		return fmt.Errorf("open source for reading: %w", err)
	}
	defer src.Close()
	dst, err := target.OpenWrite(partPath, nil)
	if err != nil {
		return fmt.Errorf("open target for writing: %w", err)
	}
	defer func() {
		if dst != nil {
			_ = dst.Close()
		}
	}()
	verify, err := target.OpenRead(partPath)
	if err != nil {
		return fmt.Errorf("open target for verification: %w", err)
	}
	defer func() {
		if verify != nil {
			_ = verify.Close()
		}
	}()

	for block := range jobs {
		var digest string
		var transferErr error
		for attempt := 1; attempt <= 3; attempt++ {
			var sent, reported int64
			var remoteHash func() ([]byte, error)
			if verifier, ok := target.(RangeSHA256Verifier); ok {
				remoteHash = func() ([]byte, error) { return verifier.SHA256Range(partPath, block.Offset, block.Length) }
			}
			digest, transferErr = transferBlockProgress(ctx, src, dst, verify, block, func(count int64) {
				sent += count
				if sent-reported >= progressStep {
					e.addTransferred(id, sent-reported)
					reported = sent
				}
			}, remoteHash)
			if sent > reported {
				e.addTransferred(id, sent-reported)
			}
			if transferErr == nil {
				break
			}
			e.addTransferred(id, -sent)
		}
		if transferErr != nil {
			return fmt.Errorf("block %d failed after retries: %w", block.Index, transferErr)
		}
		e.update(id, func(task *TransferTask) {
			if !task.Blocks[block.Index].Verified {
				task.Blocks[block.Index].Verified = true
				task.Blocks[block.Index].SHA256 = digest
				task.BytesVerified += block.Length
			}
		})
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close target after writing: %w", err)
	}
	dst = nil
	if err := verify.Close(); err != nil {
		return fmt.Errorf("close target after verification: %w", err)
	}
	verify = nil
	return nil
}

func transferBlock(ctx context.Context, src io.ReaderAt, dst io.WriterAt, verify io.ReaderAt, block BlockState) (string, error) {
	return transferBlockProgress(ctx, src, dst, verify, block, nil, nil)
}

func transferBlockProgress(ctx context.Context, src io.ReaderAt, dst io.WriterAt, verify io.ReaderAt, block BlockState, progress func(int64), remoteHash func() ([]byte, error)) (string, error) {
	buf := make([]byte, transferBufferSize)
	sourceHash := sha256.New()
	remaining := block.Length
	offset := block.Offset
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		count := int64(len(buf))
		if remaining < count {
			count = remaining
		}
		chunk := buf[:count]
		if err := readFullAt(src, chunk, offset); err != nil {
			return "", err
		}
		_, _ = sourceHash.Write(chunk)
		if err := writeFullAt(dst, chunk, offset); err != nil {
			return "", err
		}
		if progress != nil {
			progress(count)
		}
		offset += count
		remaining -= count
	}
	if syncer, ok := dst.(interface{ FlushBlock() error }); ok {
		if err := syncer.FlushBlock(); err != nil {
			return "", err
		}
	}
	sourceDigest := sourceHash.Sum(nil)
	if remoteHash != nil {
		if targetDigest, err := remoteHash(); err == nil {
			if equalBytes(sourceDigest, targetDigest) {
				return hex.EncodeToString(sourceDigest), nil
			}
			// A remote side hash can race the server's write-back/cache flush.
			// Fall through to reading the target range before declaring the
			// transfer corrupt; the read-back is the authoritative check.
		}
	}

	targetHash := sha256.New()
	remaining = block.Length
	offset = block.Offset
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		count := int64(len(buf))
		if remaining < count {
			count = remaining
		}
		chunk := buf[:count]
		if err := readFullAt(verify, chunk, offset); err != nil {
			return "", err
		}
		_, _ = targetHash.Write(chunk)
		offset += count
		remaining -= count
	}
	if !equalBytes(sourceDigest, targetHash.Sum(nil)) {
		return "", errors.New("source and target SHA-256 differ")
	}
	return hex.EncodeToString(sourceDigest), nil
}

func (e *TransferEngine) addTransferred(id string, delta int64) {
	e.mu.Lock()
	if task := e.tasks[id]; task != nil {
		task.BytesTransferred += delta
		if task.BytesTransferred < task.BytesVerified {
			task.BytesTransferred = task.BytesVerified
		}
		if task.BytesTransferred > task.Size {
			task.BytesTransferred = task.Size
		}
		task.UpdatedAt = time.Now()
		e.notifyLocked()
	}
	e.mu.Unlock()
}

func verifyRange(ctx context.Context, fs FileSystem, filePath string, block BlockState) (bool, error) {
	if verifier, ok := fs.(RangeSHA256Verifier); ok {
		if digest, err := verifier.SHA256Range(filePath, block.Offset, block.Length); err == nil {
			return hex.EncodeToString(digest) == block.SHA256, nil
		}
	}
	f, err := fs.OpenRead(filePath)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, transferBufferSize)
	remaining, offset := block.Length, block.Offset
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		count := int64(len(buf))
		if remaining < count {
			count = remaining
		}
		if err := readFullAt(f, buf[:count], offset); err != nil {
			return false, err
		}
		_, _ = h.Write(buf[:count])
		offset += count
		remaining -= count
	}
	return hex.EncodeToString(h.Sum(nil)) == block.SHA256, nil
}

func readFullAt(r io.ReaderAt, buf []byte, offset int64) error {
	for len(buf) > 0 {
		n, err := r.ReadAt(buf, offset)
		if n > 0 {
			buf = buf[n:]
			offset += int64(n)
		}
		if err != nil && !(errors.Is(err, io.EOF) && len(buf) == 0) {
			return err
		}
		if n == 0 && err == nil {
			return io.ErrNoProgress
		}
	}
	return nil
}

func writeFullAt(w io.WriterAt, buf []byte, offset int64) error {
	for len(buf) > 0 {
		n, err := w.WriteAt(buf, offset)
		if n > 0 {
			buf = buf[n:]
			offset += int64(n)
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func (e *TransferEngine) fail(id string, err error) {
	e.update(id, func(task *TransferTask) {
		task.Status = "failed"
		if err != nil {
			task.Error = err.Error()
		}
	})
	e.mu.Lock()
	delete(e.cancels, id)
	e.mu.Unlock()
}
