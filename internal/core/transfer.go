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
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	minBlockSize       = int64(8 << 20)
	defaultBlockSize   = int64(64 << 20)
	transferBufferSize = 4 << 20
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
	ID               string    `json:"id"`
	SourceProvider   string    `json:"source_provider"`
	SourcePath       string    `json:"source_path"`
	TargetProvider   string    `json:"target_provider"`
	TargetPath       string    `json:"target_path"`
	PartPath         string    `json:"part_path"`
	Size             int64     `json:"size"`
	SourceModified   time.Time `json:"source_modified"`
	BlockSize        int64     `json:"block_size"`
	Concurrency      int       `json:"concurrency"`
	ConflictPolicy   string    `json:"conflict_policy"`
	Verify           bool      `json:"verify"`
	BytesVerified    int64     `json:"bytes_verified"`
	BytesTransferred int64     `json:"bytes_transferred"`
	// Cumulative physical I/O counters used for queue read/write throughput.
	// They intentionally include bytes read or written again during retries.
	BytesRead    int64        `json:"bytes_read"`
	BytesWritten int64        `json:"bytes_written"`
	Status       string       `json:"status"`
	Error        string       `json:"error,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	Blocks       []BlockState `json:"blocks,omitempty"`
}

type TransferRequest struct {
	SourceProvider    string `json:"source_provider"`
	SourcePath        string `json:"source_path"`
	TargetProvider    string `json:"target_provider"`
	TargetPath        string `json:"target_path"`
	Concurrency       int    `json:"concurrency"`
	ConflictPolicy    string `json:"conflict_policy"`
	Verify            *bool  `json:"verify,omitempty"`
	PreserveStructure *bool  `json:"preserve_structure,omitempty"`
	Filter            string `json:"filter,omitempty"`
}

const (
	ConflictOverwrite = "overwrite"
	ConflictSkip      = "skip"
	ConflictIfNewer   = "if-newer"
	ConflictRename    = "rename"
	ConflictAsk       = "ask"
)

// TransferConflictError is returned before any target bytes are written when
// the selected policy requires a user decision.
type TransferConflictError struct {
	SourcePath string
	TargetPath string
}

func (e *TransferConflictError) Error() string {
	return fmt.Sprintf("target already exists: %s", e.TargetPath)
}

func normalizeConflictPolicy(policy string) (string, error) {
	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy == "" {
		return ConflictOverwrite, nil
	}
	switch policy {
	case ConflictOverwrite, ConflictSkip, ConflictIfNewer, ConflictRename, ConflictAsk:
		return policy, nil
	default:
		return "", fmt.Errorf("unsupported conflict policy %q", policy)
	}
}

// NormalizeConflictPolicy validates a policy for API/template inputs and
// returns the default overwrite policy when omitted.
func NormalizeConflictPolicy(policy string) (string, error) {
	return normalizeConflictPolicy(policy)
}

type transferRun struct {
	cancel context.CancelFunc
	done   chan struct{}
	resume bool
}

type TransferEngine struct {
	mu          sync.RWMutex
	manager     *Manager
	tasks       map[string]*TransferTask
	runs        map[string]*transferRun
	revalidate  map[string]bool
	storePath   string
	subscribers map[chan struct{}]struct{}
	// slots limits active block workers per target provider. Keeping this
	// quota per provider is important: a burst of FTP tasks waiting for the
	// FTP server's single STOR slot must not consume the quota needed by an
	// unrelated SFTP or local target.
	slotsMu sync.Mutex
	slots   map[string]chan struct{}
	// sequence makes task IDs unique when concurrent HTTP requests observe
	// the same wall-clock timestamp (common on Windows timer resolution).
	sequence atomic.Uint64
	// finalizeMu prevents a burst of directory transfers from issuing
	// competing remove/rename requests on the same remote control channel.
	// Some SFTP/FTP servers otherwise acknowledge the write before the .part
	// entry is visible to the following rename request.
	finalizeMu sync.Mutex
}

func NewTransferEngine(manager *Manager, storePath string) *TransferEngine {
	e := &TransferEngine{
		manager: manager, tasks: make(map[string]*TransferTask), runs: make(map[string]*transferRun),
		revalidate: make(map[string]bool),
		storePath:  storePath, subscribers: make(map[chan struct{}]struct{}),
		slots: make(map[string]chan struct{}),
	}
	e.load()
	return e
}

func (e *TransferEngine) transferSlot(providerID string) chan struct{} {
	e.slotsMu.Lock()
	defer e.slotsMu.Unlock()
	if slots := e.slots[providerID]; slots != nil {
		return slots
	}
	slots := make(chan struct{}, 8)
	e.slots[providerID] = slots
	return slots
}

func (e *TransferEngine) acquireTransferSlot(ctx context.Context, providerID string) (func(), error) {
	slots := e.transferSlot(providerID)
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *TransferEngine) newTaskID() string {
	return fmt.Sprintf("transfer-%d-%d", time.Now().UnixNano(), e.sequence.Add(1))
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
		// Older task files predate the flag; retain the integrity-first default
		// when loading them (an explicitly disabled check may be rechecked after
		// an application restart).
		if !task.Verify {
			task.Verify = true
		}
		if task.Status == "running" || task.Status == "verifying" {
			task.Status = "paused"
			task.Error = "application restarted; resume to continue"
		}
		if task.BytesTransferred < task.BytesVerified {
			task.BytesTransferred = task.BytesVerified
		}
		if task.BytesWritten < task.BytesTransferred {
			task.BytesWritten = task.BytesTransferred
		}
		if task.BytesRead < task.BytesTransferred {
			task.BytesRead = task.BytesTransferred
		}
		e.tasks[task.ID] = task
		for _, block := range task.Blocks {
			if block.Verified && block.SHA256 != "" && task.Status != "completed" {
				e.revalidate[task.ID] = true
				break
			}
		}
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
	if info.IsDir {
		return TransferTask{}, errors.New("source path is a directory")
	}
	policy, err := normalizeConflictPolicy(req.ConflictPolicy)
	if err != nil {
		return TransferTask{}, err
	}
	actualTargetPath, skip, err := resolveConflict(target, info, req.SourcePath, req.TargetPath, policy)
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
	now := time.Now()
	taskID := e.newTaskID()
	// Some SFTP deployments hide a temporary path after its write handle closes
	// or reject a rename from the transfer channel. Keep their proven direct
	// path mode; local and FTP providers use isolated task temporary files.
	partPath := actualTargetPath
	if target.Kind() != "sftp" {
		partPath += ".floe-part-" + taskID
	}
	zero := int64(0)
	// A directory task and an explicitly selected child can arrive at the
	// same time. Do not create two writers for one destination; return the
	// already queued task instead. The lock intentionally covers preparation
	// so two concurrent HTTP requests cannot pass this check together.
	e.mu.Lock()
	for _, existing := range e.tasks {
		if existing.TargetProvider == req.TargetProvider && existing.TargetPath == actualTargetPath &&
			existing.Status != "completed" && existing.Status != "failed" {
			snapshot := cloneTask(existing)
			e.mu.Unlock()
			return snapshot, nil
		}
	}
	// FTP creates the STOR target from the worker while holding its global
	// write slot. Pre-opening it here would overlap another task's data
	// connection and causes EOF on servers with a low per-user connection cap.
	if !skip && target.Kind() != "ftp" {
		w, err := target.OpenWrite(partPath, &zero)
		if err != nil {
			e.mu.Unlock()
			return TransferTask{}, fmt.Errorf("prepare target: %w", err)
		}
		if err := w.Close(); err != nil {
			e.mu.Unlock()
			return TransferTask{}, fmt.Errorf("prepare target close: %w", err)
		}
	}

	blockSize := chooseBlockSize(info.Size, req.Concurrency)
	blockCount := int((info.Size + blockSize - 1) / blockSize)
	blocks := make([]BlockState, blockCount)
	for i := range blocks {
		offset := int64(i) * blockSize
		length := min(blockSize, info.Size-offset)
		blocks[i] = BlockState{Index: i, Offset: offset, Length: length}
	}
	task := &TransferTask{
		ID: taskID, SourceProvider: req.SourceProvider,
		SourcePath: req.SourcePath, TargetProvider: req.TargetProvider, TargetPath: actualTargetPath,
		PartPath: partPath, Size: info.Size, SourceModified: info.Modified, BlockSize: blockSize,
		Concurrency: req.Concurrency, ConflictPolicy: policy, Verify: req.Verify == nil || *req.Verify,
		Status: "running", CreatedAt: now, UpdatedAt: now, Blocks: blocks,
	}
	if skip {
		task.Status = "skipped"
		task.Error = "目标已存在，按冲突策略跳过"
		task.PartPath = ""
	}
	e.tasks[task.ID] = task
	e.saveLocked()
	e.notifyLocked()
	e.mu.Unlock()
	if !skip {
		e.start(task.ID)
	}
	return cloneTask(task), nil
}

func resolveConflict(target FileSystem, source FileInfo, sourcePath, targetPath, policy string) (string, bool, error) {
	info, err := target.Stat(targetPath)
	exists := err == nil
	if err != nil {
		missing, missingErr := pathMissing(target, targetPath)
		if missingErr != nil {
			return "", false, fmt.Errorf("inspect target: %w", err)
		}
		exists = !missing
	}
	if !exists {
		return targetPath, false, nil
	}
	switch policy {
	case ConflictOverwrite:
		return targetPath, false, nil
	case ConflictSkip:
		return targetPath, true, nil
	case ConflictIfNewer:
		if source.Modified.After(info.Modified) {
			return targetPath, false, nil
		}
		return targetPath, true, nil
	case ConflictRename:
		return uniqueTargetPath(target, targetPath)
	case ConflictAsk:
		return "", false, &TransferConflictError{SourcePath: sourcePath, TargetPath: targetPath}
	default:
		return "", false, fmt.Errorf("unsupported conflict policy %q", policy)
	}
}

// ResolveConflict applies the same policy used by file tasks to a directory
// destination before the server starts walking its children.
func ResolveConflict(target FileSystem, source FileInfo, sourcePath, targetPath, policy string) (string, bool, error) {
	normalized, err := normalizeConflictPolicy(policy)
	if err != nil {
		return "", false, err
	}
	return resolveConflict(target, source, sourcePath, targetPath, normalized)
}

func uniqueTargetPath(target FileSystem, original string) (string, bool, error) {
	directory, filename := path.Dir(original), path.Base(original)
	extension := path.Ext(filename)
	stem := strings.TrimSuffix(filename, extension)
	for index := 1; index <= 10000; index++ {
		candidate := path.Join(directory, fmt.Sprintf("%s (%d)%s", stem, index, extension))
		_, err := target.Stat(candidate)
		if err != nil {
			missing, missingErr := pathMissing(target, candidate)
			if missingErr != nil {
				return "", false, fmt.Errorf("inspect renamed target: %w", err)
			}
			if missing {
				return candidate, false, nil
			}
		}
	}
	return "", false, errors.New("无法生成不冲突的目标文件名")
}

func (e *TransferEngine) start(id string) {
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	if e.tasks[id] == nil || e.runs[id] != nil {
		e.mu.Unlock()
		cancel()
		return
	}
	run := &transferRun{cancel: cancel, done: make(chan struct{})}
	e.runs[id] = run
	if task := e.tasks[id]; task != nil {
		task.Status = "running"
		task.Error = ""
		task.BytesTransferred = task.BytesVerified
		task.UpdatedAt = time.Now()
		e.saveLocked()
		e.notifyLocked()
	}
	e.mu.Unlock()
	go func() {
		defer e.finishRun(id, run)
		e.run(ctx, id)
	}()
}

func (e *TransferEngine) finishRun(id string, run *transferRun) {
	e.mu.Lock()
	if e.runs[id] != run {
		e.mu.Unlock()
		return
	}
	delete(e.runs, id)
	close(run.done)
	task := e.tasks[id]
	restart := run.resume && task != nil && task.Status != "completed"
	e.mu.Unlock()
	if restart {
		e.start(id)
	}
}

func (e *TransferEngine) Pause(id string) error {
	e.mu.Lock()
	task := e.tasks[id]
	if task == nil {
		e.mu.Unlock()
		return errors.New("task not found")
	}
	run := e.runs[id]
	if run == nil || (task.Status != "running" && task.Status != "verifying") {
		e.mu.Unlock()
		return errors.New("task is not running")
	}
	task.Status = "paused"
	task.UpdatedAt = time.Now()
	e.saveLocked()
	e.notifyLocked()
	e.mu.Unlock()
	run.cancel()
	return nil
}

func (e *TransferEngine) Resume(id string) error {
	e.mu.Lock()
	task := e.tasks[id]
	var snapshot TransferTask
	if task != nil {
		snapshot = cloneTask(task)
	}
	run := e.runs[id]
	e.mu.Unlock()
	if task == nil {
		return errors.New("task not found")
	}
	if run != nil && snapshot.Status != "paused" {
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
	if run != nil {
		e.mu.Lock()
		if current := e.runs[id]; current == run {
			if current.resume {
				e.mu.Unlock()
				return errors.New("task resume is already queued")
			}
			current.resume = true
			e.mu.Unlock()
			return nil
		}
		e.mu.Unlock()
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
	run := e.runs[id]
	if run != nil {
		run.resume = false
		run.cancel()
	}
	snapshot := cloneTask(task)
	delete(e.tasks, id)
	delete(e.revalidate, id)
	e.saveLocked()
	e.notifyLocked()
	e.mu.Unlock()
	e.cleanupPartialAfter(snapshot, run)
	return nil
}

func (e *TransferEngine) Clear(status string) (int, error) {
	if status != "completed" && status != "failed" {
		return 0, errors.New("only completed or failed tasks can be cleared")
	}
	e.mu.Lock()
	removed := 0
	type cleanup struct {
		task TransferTask
		run  *transferRun
	}
	cleanups := make([]cleanup, 0)
	for id, task := range e.tasks {
		if task.Status != status && !(status == "completed" && task.Status == "skipped") {
			continue
		}
		run := e.runs[id]
		if run != nil {
			run.resume = false
			run.cancel()
		}
		cleanups = append(cleanups, cleanup{task: cloneTask(task), run: run})
		delete(e.tasks, id)
		delete(e.revalidate, id)
		removed++
	}
	if removed > 0 {
		e.saveLocked()
		e.notifyLocked()
	}
	e.mu.Unlock()
	for _, item := range cleanups {
		e.cleanupPartialAfter(item.task, item.run)
	}
	return removed, nil
}

func (e *TransferEngine) cleanupPartialAfter(task TransferTask, run *transferRun) {
	if task.PartPath == "" || task.PartPath == task.TargetPath {
		return
	}
	cleanup := func() {
		if target, ok := e.manager.Get(task.TargetProvider); ok {
			_ = target.Remove(task.PartPath)
		}
	}
	if run == nil {
		go cleanup()
		return
	}
	go func() {
		<-run.done
		cleanup()
	}()
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
	if err != nil || !sameSourceVersion(info, task) {
		if err == nil {
			err = errors.New("source file changed since the task was created")
		}
		e.fail(id, err)
		return
	}

	// A persisted manifest is untrusted after a restart, so verify its blocks
	// once. In-process pause/resume keeps the already verified durable blocks
	// and does not download them again.
	e.mu.RLock()
	needsValidation := e.revalidate[id]
	e.mu.RUnlock()
	if needsValidation {
		e.update(id, func(task *TransferTask) { task.Status = "verifying" })
		for i, block := range task.Blocks {
			if !block.Verified || block.SHA256 == "" {
				continue
			}
			valid, verifyErr := verifyRange(ctx, target, task.PartPath, block)
			if errors.Is(verifyErr, context.Canceled) {
				return
			}
			if verifyErr != nil || !valid {
				e.update(id, func(t *TransferTask) {
					t.Blocks[i].Verified = false
					t.Blocks[i].SHA256 = ""
					t.BytesVerified -= block.Length
					if t.BytesVerified < 0 {
						t.BytesVerified = 0
					}
				})
			}
		}
		e.mu.Lock()
		delete(e.revalidate, id)
		e.mu.Unlock()
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
	if task.Size == 0 && target.Kind() == "ftp" {
		if controller, ok := target.(WriteSlotController); ok {
			release, acquireErr := controller.AcquireWriteSlot(ctx)
			if acquireErr != nil {
				e.fail(id, acquireErr)
				return
			}
			defer release()
		}
		zero := int64(0)
		w, openErr := target.OpenWrite(task.PartPath, &zero)
		if openErr != nil {
			e.fail(id, fmt.Errorf("open empty FTP target: %w", openErr))
			return
		}
		if closeErr := w.Close(); closeErr != nil {
			e.fail(id, fmt.Errorf("close empty FTP target: %w", closeErr))
			return
		}
	}
	partitions := partitionBlocks(pending, task.Concurrency)
	errCh := make(chan error, task.Concurrency)
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
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
			if err := e.worker(workerCtx, id, source, target, task.SourcePath, task.PartPath, task.Verify, jobs); err != nil && !errors.Is(err, context.Canceled) {
				select {
				case errCh <- err:
					cancelWorkers()
				default:
				}
			}
		}(jobs)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	select {
	case err := <-errCh:
		e.fail(id, err)
		return
	default:
	}
	finalInfo, err := source.Stat(task.SourcePath)
	if err != nil || !sameSourceVersion(finalInfo, task) {
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
}

func (e *TransferEngine) finalize(ctx context.Context, target FileSystem, partPath, targetPath string, expectedSize int64) error {
	e.finalizeMu.Lock()
	defer e.finalizeMu.Unlock()
	var lastErr error
	diagnostics := make([]string, 0, 8)
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		partInfo, err := target.Stat(partPath)
		if err != nil {
			lastErr = fmt.Errorf("stat temporary file: %w", err)
			diagnostics = append(diagnostics, fmt.Sprintf("attempt %d: %v", attempt+1, lastErr))
		} else if partInfo.IsDir || partInfo.Size != expectedSize {
			return fmt.Errorf("temporary file has size %d, expected %d", partInfo.Size, expectedSize)
		} else {
			lastErr = replaceVerifiedTarget(target, partPath, targetPath, expectedSize)
			if lastErr == nil || finalizedTarget(target, partPath, targetPath, expectedSize) {
				return nil
			}
			diagnostics = append(diagnostics, fmt.Sprintf("attempt %d: %v", attempt+1, lastErr))
		}
		if attempt < 2 {
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

func replaceVerifiedTarget(target FileSystem, partPath, targetPath string, expectedSize int64) error {
	if replacer, ok := target.(AtomicReplacer); ok {
		if err := replacer.Replace(partPath, targetPath); err == nil {
			return validateFinalTarget(target, targetPath, expectedSize)
		}
		if finalizedTarget(target, partPath, targetPath, expectedSize) {
			return nil
		}
	}

	// Protocols without atomic overwrite first get a direct rename for servers
	// that overwrite by default. If that is rejected, move the old target to a
	// backup and restore it if promoting the verified file fails.
	if err := target.Rename(partPath, targetPath); err == nil {
		return validateFinalTarget(target, targetPath, expectedSize)
	} else if finalizedTarget(target, partPath, targetPath, expectedSize) {
		return nil
	}
	if _, err := target.Stat(targetPath); err != nil {
		return fmt.Errorf("rename temporary file: %w", err)
	}
	backupPath := fmt.Sprintf("%s.floe-backup-%d", targetPath, time.Now().UnixNano())
	if err := target.Rename(targetPath, backupPath); err != nil {
		return fmt.Errorf("preserve existing target: %w", err)
	}
	if err := target.Rename(partPath, targetPath); err != nil {
		if finalizedTarget(target, partPath, targetPath, expectedSize) {
			_ = target.Remove(backupPath)
			return nil
		}
		if restoreErr := target.Rename(backupPath, targetPath); restoreErr != nil {
			return fmt.Errorf("promote temporary file: %v; restore old target: %w", err, restoreErr)
		}
		return fmt.Errorf("promote temporary file: %w", err)
	}
	if err := validateFinalTarget(target, targetPath, expectedSize); err != nil {
		_ = target.Remove(targetPath)
		if restoreErr := target.Rename(backupPath, targetPath); restoreErr != nil {
			return fmt.Errorf("final target validation: %v; restore old target: %w", err, restoreErr)
		}
		return err
	}
	_ = target.Remove(backupPath)
	return nil
}

func validateFinalTarget(target FileSystem, targetPath string, expectedSize int64) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		info, err := target.Stat(targetPath)
		if err == nil {
			if info.IsDir || info.Size != expectedSize {
				return fmt.Errorf("final target has size %d, expected %d", info.Size, expectedSize)
			}
			return nil
		}
		lastErr = err
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
		}
	}
	return fmt.Errorf("stat final target: %w", lastErr)
}

func finalizedTarget(target FileSystem, partPath, targetPath string, expectedSize int64) bool {
	missing, err := pathMissing(target, partPath)
	if err != nil || !missing {
		return false
	}
	return validateFinalTarget(target, targetPath, expectedSize) == nil
}

func pathMissing(target FileSystem, filePath string) (bool, error) {
	if _, err := target.Stat(filePath); err == nil {
		return false, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return true, nil
	} else {
		message := strings.ToLower(err.Error())
		for _, fragment := range []string{"not found", "no such file", "no such file or directory", "550 ", "remote path not found"} {
			if strings.Contains(message, fragment) {
				return true, nil
			}
		}
		return false, err
	}
}

func sameSourceVersion(info FileInfo, task TransferTask) bool {
	if info.IsDir || info.Size != task.Size {
		return false
	}
	return info.Modified.Equal(task.SourceModified)
}

func chooseBlockSize(size int64, concurrency int) int64 {
	if size <= 0 {
		return minBlockSize
	}
	workers := max(1, concurrency)
	desiredBlocks := int64(workers * 4)
	blockSize := (size + desiredBlocks - 1) / desiredBlocks
	const alignment = int64(1 << 20)
	blockSize = ((blockSize + alignment - 1) / alignment) * alignment
	return max(minBlockSize, min(defaultBlockSize, blockSize))
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

type lazyReadAtCloser struct {
	open   func() (ReadAtCloser, error)
	reader ReadAtCloser
	err    error
}

func (r *lazyReadAtCloser) ReadAt(buffer []byte, offset int64) (int, error) {
	if r.reader == nil && r.err == nil {
		r.reader, r.err = r.open()
	}
	if r.err != nil {
		return 0, r.err
	}
	return r.reader.ReadAt(buffer, offset)
}

func (r *lazyReadAtCloser) Close() error {
	if r.reader == nil {
		return nil
	}
	return r.reader.Close()
}

func (e *TransferEngine) worker(ctx context.Context, id string, source, target FileSystem, sourcePath, partPath string, verifyBlocks bool, jobs <-chan BlockState) error {
	releaseSlot, err := e.acquireTransferSlot(ctx, target.ID())
	if err != nil {
		return err
	}
	defer releaseSlot()
	if controller, ok := target.(WriteSlotController); ok {
		release, acquireErr := controller.AcquireWriteSlot(ctx)
		if acquireErr != nil {
			return acquireErr
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
	written := make([]BlockState, 0)
	for block := range jobs {
		var digest string
		var transferErr error
		for attempt := 1; attempt <= 3; attempt++ {
			var sent, reported, read, written, reportedRead, reportedWritten int64
			digest, transferErr = writeBlockProgressStats(ctx, src, dst, block, func(readDelta, writtenDelta int64) {
				read += readDelta
				written += writtenDelta
				sent += writtenDelta
				if read-reportedRead >= progressStep || written-reportedWritten >= progressStep {
					e.addProgress(id, sent-reported, read-reportedRead, written-reportedWritten)
					reported = sent
					reportedRead = read
					reportedWritten = written
				}
			})
			if sent > reported || read > reportedRead || written > reportedWritten {
				e.addProgress(id, sent-reported, read-reportedRead, written-reportedWritten)
			}
			if transferErr == nil {
				break
			}
			e.addTransferred(id, -sent)
		}
		if transferErr != nil {
			return fmt.Errorf("block %d failed after retries: %w", block.Index, transferErr)
		}
		block.SHA256 = digest
		written = append(written, block)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close target after writing: %w", err)
	}
	dst = nil
	for _, block := range written {
		e.update(id, func(task *TransferTask) {
			if !task.Blocks[block.Index].Verified {
				task.Blocks[block.Index].SHA256 = block.SHA256
			}
		})
	}
	if !verifyBlocks {
		for _, block := range written {
			e.update(id, func(task *TransferTask) {
				if !task.Blocks[block.Index].Verified {
					task.Blocks[block.Index].Verified = true
					task.BytesVerified += block.Length
				}
			})
		}
		return nil
	}

	verify := newLazyVerifier(target, partPath)
	defer func() {
		if verify != nil {
			_ = verify.Close()
		}
	}()
	for _, block := range written {
		var verifyErr error
		for attempt := 1; attempt <= 3; attempt++ {
			valid, err := verifyRangeWithReader(ctx, target, partPath, verify, block)
			if err == nil && valid {
				verifyErr = nil
				break
			}
			if err == nil {
				verifyErr = errors.New("source and target SHA-256 differ")
				if rewriteErr := e.rewriteBlock(ctx, id, src, target, partPath, block); rewriteErr != nil {
					verifyErr = rewriteErr
				}
			} else {
				verifyErr = err
			}
			_ = verify.Close()
			verify = newLazyVerifier(target, partPath)
		}
		if verifyErr != nil {
			return fmt.Errorf("verify block %d after retries: %w", block.Index, verifyErr)
		}
		e.update(id, func(task *TransferTask) {
			if !task.Blocks[block.Index].Verified {
				task.Blocks[block.Index].Verified = true
				task.Blocks[block.Index].SHA256 = block.SHA256
				task.BytesVerified += block.Length
			}
		})
	}
	if err := verify.Close(); err != nil {
		return fmt.Errorf("close target after verification: %w", err)
	}
	verify = nil
	return nil
}

func newLazyVerifier(target FileSystem, partPath string) *lazyReadAtCloser {
	return &lazyReadAtCloser{open: func() (ReadAtCloser, error) {
		reader, err := target.OpenRead(partPath)
		if err != nil {
			return nil, fmt.Errorf("open target for verification: %w", err)
		}
		return reader, nil
	}}
}

func (e *TransferEngine) rewriteBlock(ctx context.Context, id string, source io.ReaderAt, target FileSystem, partPath string, block BlockState) error {
	destination, err := target.OpenWrite(partPath, nil)
	if err != nil {
		return fmt.Errorf("reopen target for repair: %w", err)
	}
	var sent int64
	digest, writeErr := writeBlockProgressStats(ctx, source, destination, block, func(read, written int64) {
		sent += written
		e.addProgress(id, written, read, written)
	})
	closeErr := destination.Close()
	if writeErr != nil || closeErr != nil {
		e.addTransferred(id, -sent)
		return errors.Join(writeErr, closeErr)
	}
	if digest != block.SHA256 {
		return errors.New("source changed while repairing target block")
	}
	return nil
}

func writeBlockProgress(ctx context.Context, src io.ReaderAt, dst io.WriterAt, block BlockState, progress func(int64)) (string, error) {
	return writeBlockProgressStats(ctx, src, dst, block, func(_, written int64) {
		if progress != nil {
			progress(written)
		}
	})
}

func writeBlockProgressStats(ctx context.Context, src io.ReaderAt, dst io.WriterAt, block BlockState, progress func(read, written int64)) (string, error) {
	buf := transferBuffer(block.Length)
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
		if progress != nil {
			progress(count, 0)
		}
		_, _ = sourceHash.Write(chunk)
		if err := writeFullAt(dst, chunk, offset); err != nil {
			return "", err
		}
		if progress != nil {
			progress(0, count)
		}
		offset += count
		remaining -= count
	}
	return hex.EncodeToString(sourceHash.Sum(nil)), nil
}

func transferBlock(ctx context.Context, src io.ReaderAt, dst io.WriterAt, verify io.ReaderAt, block BlockState) (string, error) {
	return transferBlockProgress(ctx, src, dst, verify, block, nil, nil)
}

func transferBlockProgress(ctx context.Context, src io.ReaderAt, dst io.WriterAt, verify io.ReaderAt, block BlockState, progress func(int64), remoteHash func() ([]byte, error)) (string, error) {
	buf := transferBuffer(block.Length)
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
	e.addProgress(id, delta, 0, 0)
}

func (e *TransferEngine) addProgress(id string, transferredDelta, readDelta, writtenDelta int64) {
	e.mu.Lock()
	if task := e.tasks[id]; task != nil {
		task.BytesTransferred += transferredDelta
		if task.BytesTransferred < task.BytesVerified {
			task.BytesTransferred = task.BytesVerified
		}
		if task.BytesTransferred > task.Size {
			task.BytesTransferred = task.Size
		}
		task.BytesRead += readDelta
		task.BytesWritten += writtenDelta
		task.UpdatedAt = time.Now()
		e.notifyLocked()
	}
	e.mu.Unlock()
}

func verifyRange(ctx context.Context, fs FileSystem, filePath string, block BlockState) (bool, error) {
	return verifyRangeWithReader(ctx, fs, filePath, nil, block)
}

func verifyRangeWithReader(ctx context.Context, fs FileSystem, filePath string, reader io.ReaderAt, block BlockState) (bool, error) {
	if verifier, ok := fs.(RangeSHA256Verifier); ok {
		if digest, err := verifier.SHA256Range(filePath, block.Offset, block.Length); err == nil {
			return hex.EncodeToString(digest) == block.SHA256, nil
		}
	}
	var closer io.Closer
	if reader == nil {
		file, err := fs.OpenRead(filePath)
		if err != nil {
			return false, err
		}
		reader = file
		closer = file
	}
	if closer != nil {
		defer closer.Close()
	}
	h := sha256.New()
	buf := transferBuffer(block.Length)
	remaining, offset := block.Length, block.Offset
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		count := int64(len(buf))
		if remaining < count {
			count = remaining
		}
		if err := readFullAt(reader, buf[:count], offset); err != nil {
			return false, err
		}
		_, _ = h.Write(buf[:count])
		offset += count
		remaining -= count
	}
	return hex.EncodeToString(h.Sum(nil)) == block.SHA256, nil
}

func transferBuffer(length int64) []byte {
	size := int64(transferBufferSize)
	if length > 0 && length < size {
		size = length
	}
	if size < 1 {
		size = 1
	}
	return make([]byte, int(size))
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
}
