package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// PathBookmark is a saved directory in a local or remote bookmark scope.
type PathBookmark struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

type bookmarkStore struct {
	mu      sync.RWMutex
	path    string
	entries map[string][]PathBookmark
}

func newBookmarkStore(dataDir string) (*bookmarkStore, error) {
	store := &bookmarkStore{path: filepath.Join(dataDir, "bookmarks.json"), entries: make(map[string][]PathBookmark)}
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

func (s *bookmarkStore) List() map[string][]PathBookmark {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string][]PathBookmark, len(s.entries))
	for key, entries := range s.entries {
		result[key] = append([]PathBookmark(nil), entries...)
	}
	return result
}

func (s *bookmarkStore) Save(key string, entries []PathBookmark) error {
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
	return s.saveLocked(key, entries)
}

// Update applies one change to the current scope under the same lock as Save.
func (s *bookmarkStore) Update(key string, entry PathBookmark, remove bool) ([]PathBookmark, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 200 {
		return nil, errors.New("书签作用域无效")
	}
	if remove {
		entry.Label = entry.Path
	}
	normalized := validBookmarks([]PathBookmark{entry})
	if len(normalized) != 1 {
		return nil, errors.New("书签路径或名称无效")
	}
	entry = normalized[0]

	s.mu.Lock()
	defer s.mu.Unlock()
	entries := slices.Clone(s.entries[key])
	normalizePath := func(value string) string {
		return strings.TrimRight(strings.ReplaceAll(value, "/", "\\"), "\\")
	}
	index := slices.IndexFunc(entries, func(existing PathBookmark) bool {
		if key == "local" {
			return strings.EqualFold(normalizePath(existing.Path), normalizePath(entry.Path))
		}
		return existing.Path == entry.Path
	})
	switch {
	case remove && index >= 0:
		entries = slices.Delete(entries, index, index+1)
	case !remove && index < 0:
		if len(entries) >= 500 {
			return nil, errors.New("单个服务器最多保存 500 个书签")
		}
		entries = append(entries, entry)
	default:
		return entries, nil
	}
	slices.SortStableFunc(entries, func(a, b PathBookmark) int {
		return strings.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label))
	})
	if err := s.saveLocked(key, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *bookmarkStore) saveLocked(key string, entries []PathBookmark) error {
	previous, existed := s.entries[key]
	if len(entries) == 0 {
		delete(s.entries, key)
	} else {
		s.entries[key] = append([]PathBookmark(nil), entries...)
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

func validBookmarks(entries []PathBookmark) []PathBookmark {
	result := make([]PathBookmark, 0, len(entries))
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
