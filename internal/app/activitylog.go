package app

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const activityLogLimit = 1000

type activityEntry struct {
	ID       int64     `json:"id"`
	Time     time.Time `json:"time"`
	Level    string    `json:"level"`
	Category string    `json:"category"`
	Message  string    `json:"message"`
	Detail   string    `json:"detail,omitempty"`
}

type activityLog struct {
	mu          sync.RWMutex
	path        string
	entries     []activityEntry
	subscribers map[chan struct{}]struct{}
}

func newActivityLog(dataDir string) *activityLog {
	store := &activityLog{
		path: filepath.Join(dataDir, "activity.json"), subscribers: make(map[chan struct{}]struct{}),
	}
	data, err := os.ReadFile(store.path)
	if err == nil {
		_ = json.Unmarshal(data, &store.entries)
		if len(store.entries) > activityLogLimit {
			store.entries = append([]activityEntry(nil), store.entries[len(store.entries)-activityLogLimit:]...)
		}
	}
	return store
}

func (l *activityLog) Add(level, category, message, detail string) {
	entry := activityEntry{
		ID: time.Now().UnixNano(), Time: time.Now(), Level: level,
		Category: category, Message: message, Detail: detail,
	}
	l.mu.Lock()
	l.entries = append(l.entries, entry)
	if len(l.entries) > activityLogLimit {
		l.entries = append([]activityEntry(nil), l.entries[len(l.entries)-activityLogLimit:]...)
	}
	l.persistLocked()
	for subscriber := range l.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
	l.mu.Unlock()
	if detail == "" {
		log.Printf("[%s/%s] %s", level, category, message)
	} else {
		log.Printf("[%s/%s] %s: %s", level, category, message, detail)
	}
}

func (l *activityLog) List(limit int) []activityEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if limit < 1 || limit > len(l.entries) {
		limit = len(l.entries)
	}
	result := append([]activityEntry(nil), l.entries[len(l.entries)-limit:]...)
	sort.Slice(result, func(i, j int) bool { return result[i].Time.After(result[j].Time) })
	return result
}

func (l *activityLog) Clear() int {
	l.mu.Lock()
	removed := len(l.entries)
	l.entries = nil
	l.persistLocked()
	for subscriber := range l.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
	l.mu.Unlock()
	return removed
}

func (l *activityLog) Subscribe() (<-chan struct{}, func()) {
	updates := make(chan struct{}, 1)
	l.mu.Lock()
	l.subscribers[updates] = struct{}{}
	l.mu.Unlock()
	return updates, func() {
		l.mu.Lock()
		delete(l.subscribers, updates)
		close(updates)
		l.mu.Unlock()
	}
}

func (l *activityLog) persistLocked() {
	data, err := json.MarshalIndent(l.entries, "", "  ")
	if err != nil {
		return
	}
	temporary := l.path + ".tmp"
	if os.WriteFile(temporary, data, 0o600) == nil {
		_ = os.Rename(temporary, l.path)
	}
}

func (s *Server) monitorTransferActivity(ctx context.Context) {
	updates, unsubscribe := s.transfers.Subscribe()
	defer unsubscribe()
	known := make(map[string]string)
	for _, task := range s.transfers.List() {
		known[task.ID] = task.Status
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-updates:
			seen := make(map[string]bool)
			for _, task := range s.transfers.List() {
				seen[task.ID] = true
				previous := known[task.ID]
				known[task.ID] = task.Status
				if previous == task.Status {
					continue
				}
				switch task.Status {
				case "completed":
					s.activity.Add("info", "transfer", "传输完成", task.SourcePath+" → "+task.TargetPath)
				case "failed":
					s.activity.Add("error", "transfer", "传输失败", task.SourcePath+" → "+task.TargetPath+" · "+task.Error)
				case "paused":
					s.activity.Add("warning", "transfer", "传输已暂停", task.SourcePath+" → "+task.TargetPath)
				}
			}
			for id := range known {
				if !seen[id] {
					delete(known, id)
				}
			}
		}
	}
}
