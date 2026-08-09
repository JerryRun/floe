package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// TransferTemplate is deliberately limited to transfer configuration. It
// never contains passwords or private keys; provider IDs refer to the
// encrypted session store.
type TransferTemplate struct {
	ID                string                 `json:"id"`
	Name              string                 `json:"name"`
	SourceProvider    string                 `json:"source_provider"`
	TargetProvider    string                 `json:"target_provider"`
	SourcePath        string                 `json:"source_path"`
	TargetPath        string                 `json:"target_path"`
	ConflictPolicy    string                 `json:"conflict_policy"`
	Concurrency       int                    `json:"concurrency"`
	Verify            bool                   `json:"verify"`
	PreserveStructure bool                   `json:"preserve_structure"`
	Filter            string                 `json:"filter,omitempty"`
	Tasks             []TransferTemplateTask `json:"tasks,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

type TransferTemplateTask struct {
	SourceProvider    string `json:"source_provider"`
	SourcePath        string `json:"source_path"`
	TargetProvider    string `json:"target_provider"`
	TargetPath        string `json:"target_path"`
	ConflictPolicy    string `json:"conflict_policy"`
	Concurrency       int    `json:"concurrency"`
	Verify            bool   `json:"verify"`
	PreserveStructure bool   `json:"preserve_structure"`
	Filter            string `json:"filter,omitempty"`
}

type transferTemplateStore struct {
	mu    sync.RWMutex
	path  string
	items map[string]TransferTemplate
}

func newTransferTemplateStore(dataDir string) (*transferTemplateStore, error) {
	store := &transferTemplateStore{path: filepath.Join(dataDir, "transfer-templates.json"), items: make(map[string]TransferTemplate)}
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var items []TransferTemplate
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID != "" {
			store.items[item.ID] = normalizeTransferTemplate(item)
		}
	}
	return store, nil
}

func normalizeTransferTemplate(item TransferTemplate) TransferTemplate {
	item.Name = strings.TrimSpace(item.Name)
	if item.ConflictPolicy == "" {
		item.ConflictPolicy = "overwrite"
	}
	if item.Concurrency < 1 {
		item.Concurrency = 4
	}
	if item.Concurrency > 8 {
		item.Concurrency = 8
	}
	if len(item.Tasks) == 0 && item.SourceProvider != "" && item.TargetProvider != "" {
		item.Tasks = []TransferTemplateTask{{SourceProvider: item.SourceProvider, SourcePath: item.SourcePath, TargetProvider: item.TargetProvider, TargetPath: item.TargetPath, ConflictPolicy: item.ConflictPolicy, Concurrency: item.Concurrency, Verify: item.Verify, PreserveStructure: item.PreserveStructure, Filter: item.Filter}}
	}
	if len(item.Tasks) > 0 {
		first := item.Tasks[0]
		item.SourceProvider, item.SourcePath = first.SourceProvider, first.SourcePath
		item.TargetProvider, item.TargetPath = first.TargetProvider, first.TargetPath
		item.ConflictPolicy, item.Concurrency = first.ConflictPolicy, first.Concurrency
		item.Verify, item.PreserveStructure, item.Filter = first.Verify, first.PreserveStructure, first.Filter
	}
	return item
}

func (s *transferTemplateStore) Get(id string) (TransferTemplate, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[id]
	return item, ok
}

func (s *transferTemplateStore) List() []TransferTemplate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]TransferTemplate, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name) })
	return items
}

func (s *transferTemplateStore) Save(item TransferTemplate) (TransferTemplate, error) {
	item = normalizeTransferTemplate(item)
	if item.Name == "" {
		return TransferTemplate{}, errors.New("模板名称不能为空")
	}
	if item.ID == "" {
		item.ID = "template-" + randomToken(10)
		item.CreatedAt = time.Now()
	}
	item.UpdatedAt = time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.items[item.ID]; !old.CreatedAt.IsZero() {
		item.CreatedAt = old.CreatedAt
	}
	s.items[item.ID] = item
	if err := s.persistLocked(); err != nil {
		return TransferTemplate{}, err
	}
	return item, nil
}

func (s *transferTemplateStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return errors.New("模板不存在")
	}
	delete(s.items, id)
	return s.persistLocked()
}

func (s *transferTemplateStore) persistLocked() error {
	items := make([]TransferTemplate, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}
