package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type pathBookmark struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

type bookmarkStore struct {
	mu      sync.RWMutex
	path    string
	entries map[string][]pathBookmark
}

func newBookmarkStore(dataDir string) (*bookmarkStore, error) {
	store := &bookmarkStore{path: filepath.Join(dataDir, "bookmarks.json"), entries: make(map[string][]pathBookmark)}
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &store.entries); err != nil {
		return nil, err
	}
	for key, entries := range store.entries {
		store.entries[key] = validBookmarks(entries)
		if len(store.entries[key]) == 0 {
			delete(store.entries, key)
		}
	}
	return store, nil
}

func (s *bookmarkStore) List() map[string][]pathBookmark {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string][]pathBookmark, len(s.entries))
	for key, entries := range s.entries {
		result[key] = append([]pathBookmark(nil), entries...)
	}
	return result
}

func (s *bookmarkStore) Save(key string, entries []pathBookmark) error {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 200 {
		return errors.New("书签作用域无效")
	}
	if len(entries) > 500 {
		return errors.New("单个服务器最多保存 500 个书签")
	}
	entries = validBookmarks(entries)
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.entries[key]
	if len(entries) == 0 {
		delete(s.entries, key)
	} else {
		s.entries[key] = append([]pathBookmark(nil), entries...)
	}
	if err := s.persistLocked(); err != nil {
		if existed {
			s.entries[key] = previous
		} else {
			delete(s.entries, key)
		}
		return err
	}
	return nil
}

func validBookmarks(entries []pathBookmark) []pathBookmark {
	result := make([]pathBookmark, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		entry.Path = strings.TrimSpace(entry.Path)
		entry.Label = strings.TrimSpace(entry.Label)
		if entry.Path == "" || entry.Label == "" || len(entry.Path) > 32768 || len(entry.Label) > 32768 || seen[entry.Path] {
			continue
		}
		seen[entry.Path] = true
		result = append(result, entry)
	}
	return result
}

func (s *bookmarkStore) persistLocked() error {
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}
