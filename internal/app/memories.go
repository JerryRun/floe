package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

var memorySecretPattern = regexp.MustCompile(`(?i)((密码|口令|password|passwd|pwd)[[:space:]]*[:=：][[:space:]]*)([^[:space:],，;；]+)`)

const (
	maxMemoryEntries = 10000
	maxMemoryBytes   = 1 << 20
)

type memoryEntry struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	Source     string    `json:"source"`
	SourcePath string    `json:"source_path,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
	UseCount   int       `json:"use_count,omitempty"`
}

type memorySearchHit struct {
	ID         string    `json:"id"`
	HitID      string    `json:"hit_id"`
	BlockID    string    `json:"block_id"`
	AnchorLine int       `json:"anchor_line"`
	Snippet    string    `json:"snippet"`
	Match      string    `json:"match"`
	Source     string    `json:"source"`
	SourcePath string    `json:"source_path,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type memoryBlock struct {
	ID            string    `json:"id"`
	MemoryID      string    `json:"memory_id"`
	Line          int       `json:"line"`
	Text          string    `json:"text"`
	Source        string    `json:"source"`
	SourcePath    string    `json:"source_path,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	DocumentStart bool      `json:"document_start,omitempty"`
	DocumentEnd   bool      `json:"document_end,omitempty"`
}

type memoryStreamPage struct {
	Blocks    []memoryBlock `json:"blocks"`
	Anchor    string        `json:"anchor,omitempty"`
	HasBefore bool          `json:"has_before"`
	HasAfter  bool          `json:"has_after"`
}

type savedMemory struct {
	ID         string    `json:"id"`
	Secret     string    `json:"secret"`
	Source     string    `json:"source"`
	SourcePath string    `json:"source_path,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
	UseCount   int       `json:"use_count,omitempty"`
}

type memoryStore struct {
	mu      sync.RWMutex
	path    string
	keyPath string
	key     []byte
	items   map[string]savedMemory
}

func (s *memoryStore) Directory() string { return filepath.Dir(s.path) }

func (s *memoryStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

func newMemoryStore(dataDir string) (*memoryStore, error) {
	store := &memoryStore{
		path: filepath.Join(dataDir, "memories.json"), keyPath: filepath.Join(dataDir, "memory.key"),
		items: make(map[string]savedMemory),
	}
	key, err := os.ReadFile(store.keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.WriteFile(store.keyPath, key, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("invalid memory encryption key")
	}
	store.key = key
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var items []savedMemory
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID != "" && item.Secret != "" {
			store.items[item.ID] = item
		}
	}
	return store, nil
}

func (s *memoryStore) List(query string, limit int) ([]memorySearchHit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	search := parseMemorySearchQuery(query)
	if len(search.Terms) == 0 && len(search.Phrases) == 0 {
		return []memorySearchHit{}, nil
	}
	type rankedEntry struct {
		entry memorySearchHit
		score int
	}
	ranked := make([]rankedEntry, 0, len(s.items))
	documents, err := s.documentsLocked()
	if err != nil {
		return nil, err
	}
	for _, document := range documents {
		saved := document.saved
		content := document.content
		searchableContent := maskMemorySecrets(content)
		hits := memoryFragments(searchableContent, search)
		for _, hit := range hits {
			blockID := memoryBlockID(saved.ID, hit.AnchorLine)
			ranked = append(ranked, rankedEntry{entry: memorySearchHit{
				ID: saved.ID, HitID: blockID, BlockID: blockID, AnchorLine: hit.AnchorLine,
				Snippet: hit.Snippet, Match: hit.Match, Source: saved.Source, SourcePath: saved.SourcePath, UpdatedAt: saved.UpdatedAt,
			}, score: hit.Score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].entry.UpdatedAt.After(ranked[j].entry.UpdatedAt)
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	result := make([]memorySearchHit, len(ranked))
	for index := range ranked {
		result[index] = ranked[index].entry
	}
	return result, nil
}

type decryptedMemory struct {
	saved   savedMemory
	content string
}

func (s *memoryStore) documentsLocked() ([]decryptedMemory, error) {
	documents := make([]decryptedMemory, 0, len(s.items))
	for _, saved := range s.items {
		content, err := s.decrypt(saved.Secret)
		if err != nil {
			return nil, err
		}
		documents = append(documents, decryptedMemory{saved: saved, content: content})
	}
	sort.Slice(documents, func(i, j int) bool {
		if documents[i].saved.CreatedAt.Equal(documents[j].saved.CreatedAt) {
			return documents[i].saved.ID < documents[j].saved.ID
		}
		return documents[i].saved.CreatedAt.Before(documents[j].saved.CreatedAt)
	})
	return documents, nil
}

func (s *memoryStore) Stream(anchor, before, after string, limit int) (memoryStreamPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	blocks, err := s.blocksLocked()
	if err != nil {
		return memoryStreamPage{}, err
	}
	if len(blocks) == 0 {
		return memoryStreamPage{Blocks: []memoryBlock{}}, nil
	}
	find := func(id string) int {
		for index := range blocks {
			if blocks[index].ID == id {
				return index
			}
		}
		return -1
	}
	start, end := 0, 0
	pageAnchor := ""
	switch {
	case anchor != "":
		index := find(anchor)
		if index < 0 {
			return memoryStreamPage{}, os.ErrNotExist
		}
		beforeCount := limit / 2
		afterCount := limit - beforeCount - 1
		start = max(0, index-beforeCount)
		end = min(len(blocks), index+afterCount+1)
		pageAnchor = anchor
	case before != "":
		index := find(before)
		if index < 0 {
			return memoryStreamPage{}, os.ErrNotExist
		}
		end = index
		start = max(0, end-limit)
	case after != "":
		index := find(after)
		if index < 0 {
			return memoryStreamPage{}, os.ErrNotExist
		}
		start = index + 1
		end = min(len(blocks), start+limit)
	default:
		return memoryStreamPage{Blocks: []memoryBlock{}}, nil
	}
	page := memoryStreamPage{
		Blocks: append([]memoryBlock(nil), blocks[start:end]...), Anchor: pageAnchor,
		HasBefore: start > 0, HasAfter: end < len(blocks),
	}
	return page, nil
}

func (s *memoryStore) blocksLocked() ([]memoryBlock, error) {
	documents, err := s.documentsLocked()
	if err != nil {
		return nil, err
	}
	blocks := make([]memoryBlock, 0)
	for _, document := range documents {
		lines := strings.Split(strings.ReplaceAll(document.content, "\r\n", "\n"), "\n")
		for line, text := range lines {
			blocks = append(blocks, memoryBlock{
				ID: memoryBlockID(document.saved.ID, line), MemoryID: document.saved.ID, Line: line,
				Text: text, Source: document.saved.Source, SourcePath: document.saved.SourcePath,
				CreatedAt: document.saved.CreatedAt, UpdatedAt: document.saved.UpdatedAt,
				DocumentStart: line == 0, DocumentEnd: line == len(lines)-1,
			})
		}
	}
	return blocks, nil
}

func memoryBlockID(memoryID string, line int) string {
	return fmt.Sprintf("%s:%d", memoryID, line)
}

type memoryFragment struct {
	AnchorLine int
	Snippet    string
	Match      string
	Score      int
}

type memorySearchQuery struct {
	Terms           []string
	Phrases         []string
	PreferredPhrase string
	Strict          bool
}

func memoryFragments(content string, query memorySearchQuery) []memoryFragment {
	lines := strings.Split(content, "\n")
	matching := make([]int, 0)
	for index, line := range lines {
		if memoryTextScore(line, query) > 0 {
			matching = append(matching, index)
		}
	}
	if len(matching) == 0 {
		return nil
	}
	fragments := make([]memoryFragment, 0, len(matching))
	for cursor := 0; cursor < len(matching); {
		anchor := matching[cursor]
		lastMatch := anchor
		cursor++
		for cursor < len(matching) && matching[cursor]-lastMatch <= 3 {
			lastMatch = matching[cursor]
			cursor++
		}
		start := max(0, anchor-1)
		end := min(len(lines), lastMatch+2)
		parts := make([]string, 0, end-start)
		for index := start; index < end; index++ {
			line := strings.TrimSpace(lines[index])
			if line != "" {
				parts = append(parts, line)
			}
		}
		snippet := truncateRunes(strings.Join(parts, "\n"), 360)
		score, match, matched := memoryFragmentScore(strings.Join(lines[start:end], "\n"), query)
		if matched {
			fragments = append(fragments, memoryFragment{AnchorLine: anchor, Snippet: snippet, Match: match, Score: score})
		}
	}
	return fragments
}

func parseMemorySearchQuery(value string) memorySearchQuery {
	normalized := normalizeMemorySearchText(value)
	result := memorySearchQuery{}
	var outside strings.Builder
	var phrase strings.Builder
	inQuote := false
	flushPhrase := func() {
		value := strings.TrimSpace(phrase.String())
		if value != "" {
			result.Phrases = append(result.Phrases, value)
		}
		phrase.Reset()
	}
	for _, char := range normalized {
		switch char {
		case '"', '“', '”':
			if inQuote {
				flushPhrase()
			} else {
				outside.WriteRune(' ')
			}
			inQuote = !inQuote
		default:
			if inQuote {
				phrase.WriteRune(char)
			} else {
				outside.WriteRune(char)
			}
		}
	}
	if phrase.Len() > 0 {
		flushPhrase()
	}
	plain := strings.NewReplacer(
		",", " ", "，", " ", ";", " ", "；", " ",
		"。", " ", "、", " ", "！", " ", "!", " ",
		"？", " ", "?", " ", "\u00a0", " ",
	).Replace(outside.String())
	result.Terms = uniqueMemorySearchValues(strings.Fields(plain))
	result.Phrases = uniqueMemorySearchValues(result.Phrases)
	result.Strict = len(result.Phrases) > 0
	if !result.Strict && len(result.Terms) > 1 {
		result.PreferredPhrase = strings.Join(result.Terms, " ")
	}
	return result
}

func uniqueMemorySearchValues(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func normalizeMemorySearchText(value string) string {
	return strings.Join(strings.Fields(strings.Map(func(char rune) rune {
		switch {
		case char >= '！' && char <= '～':
			return char - ('！' - '!')
		case char == '　':
			return ' '
		case unicode.IsSpace(char):
			return ' '
		default:
			return unicode.ToLower(char)
		}
	}, value)), " ")
}

func memoryTextScore(text string, query memorySearchQuery) int {
	haystack := normalizeMemorySearchText(text)
	score := 0
	for _, phrase := range query.Phrases {
		score += strings.Count(haystack, phrase) * 1000
	}
	if query.PreferredPhrase != "" {
		score += strings.Count(haystack, query.PreferredPhrase) * 800
	}
	for _, term := range query.Terms {
		if term == "" {
			continue
		}
		count := strings.Count(haystack, term)
		if count == 0 {
			count = memoryCompactTermCount(haystack, term)
		}
		score += count * 10
	}
	return score
}

func memoryFragmentScore(text string, query memorySearchQuery) (int, string, bool) {
	haystack := normalizeMemorySearchText(text)
	phraseMatches := 0
	for _, phrase := range query.Phrases {
		if !strings.Contains(haystack, phrase) {
			return 0, "", false
		}
		phraseMatches++
	}
	termMatches := 0
	for _, term := range query.Terms {
		if strings.Contains(haystack, term) || memoryCompactTermCount(haystack, term) > 0 {
			termMatches++
		}
	}
	if query.Strict && termMatches != len(query.Terms) {
		return 0, "", false
	}
	if phraseMatches == 0 && termMatches == 0 {
		return 0, "", false
	}
	score := memoryTextScore(text, query)
	if termMatches == len(query.Terms) && len(query.Terms) > 1 {
		score += 200
	}
	if query.Strict {
		return score + 2000, "exact", true
	}
	if query.PreferredPhrase != "" && strings.Contains(haystack, query.PreferredPhrase) {
		return score + 1500, "phrase", true
	}
	if termMatches == len(query.Terms) {
		return score + 400, "all", true
	}
	return score, "partial", true
}

func memoryCompactTermCount(haystack, term string) int {
	compact := strings.Map(func(char rune) rune {
		if unicode.IsSpace(char) || unicode.IsPunct(char) {
			return -1
		}
		return char
	}, haystack)
	compactTerm := strings.Map(func(char rune) rune {
		if unicode.IsSpace(char) || unicode.IsPunct(char) {
			return -1
		}
		return char
	}, term)
	if compactTerm == "" {
		return 0
	}
	return strings.Count(compact, compactTerm)
}

func (s *memoryStore) Save(id, content, source, sourcePath string) (memoryEntry, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return memoryEntry{}, errors.New("内容不能为空")
	}
	if len(content) > maxMemoryBytes {
		return memoryEntry{}, errors.New("单条记录不能超过 1 MB")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "floe"
	}
	sourcePath = strings.TrimSpace(sourcePath)
	secret, err := s.encrypt(content)
	if err != nil {
		return memoryEntry{}, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.items[id]
	if id == "" {
		if len(s.items) >= maxMemoryEntries {
			return memoryEntry{}, errors.New("速查记录数量已达到上限")
		}
		id = randomToken(12)
	}
	createdAt := now
	if existed {
		createdAt = previous.CreatedAt
		if source == "floe" && previous.Source != "" {
			source = previous.Source
		}
		if sourcePath == "" {
			sourcePath = previous.SourcePath
		}
	}
	item := savedMemory{
		ID: id, Secret: secret, Source: source, SourcePath: sourcePath, CreatedAt: createdAt,
		UpdatedAt: now, LastUsedAt: previous.LastUsedAt, UseCount: previous.UseCount,
	}
	s.items[id] = item
	if err := s.persistLocked(); err != nil {
		if existed {
			s.items[id] = previous
		} else {
			delete(s.items, id)
		}
		return memoryEntry{}, err
	}
	return memoryEntry{
		ID: id, Title: memoryTitle(content), Content: content,
		Source: source, SourcePath: sourcePath, CreatedAt: createdAt, UpdatedAt: now,
		LastUsedAt: item.LastUsedAt, UseCount: item.UseCount,
	}, nil
}

func (s *memoryStore) Get(id string) (memoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	saved, existed := s.items[id]
	if !existed {
		return memoryEntry{}, os.ErrNotExist
	}
	content, err := s.decrypt(saved.Secret)
	if err != nil {
		return memoryEntry{}, err
	}
	return memoryEntry{
		ID: saved.ID, Title: memoryTitle(content), Content: content, Source: saved.Source,
		SourcePath: saved.SourcePath, CreatedAt: saved.CreatedAt, UpdatedAt: saved.UpdatedAt,
		LastUsedAt: saved.LastUsedAt, UseCount: saved.UseCount,
	}, nil
}

func (s *memoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.items[id]
	if !existed {
		return os.ErrNotExist
	}
	delete(s.items, id)
	if err := s.persistLocked(); err != nil {
		s.items[id] = previous
		return err
	}
	return nil
}

func (s *memoryStore) MarkUsed(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, existed := s.items[id]
	if !existed {
		return os.ErrNotExist
	}
	previous := item
	item.LastUsedAt = time.Now().UTC()
	item.UseCount++
	s.items[id] = item
	if err := s.persistLocked(); err != nil {
		s.items[id] = previous
		return err
	}
	return nil
}

func (s *memoryStore) persistLocked() error {
	items := make([]savedMemory, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}

func (s *memoryStore) encrypt(value string) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(value), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (s *memoryStore) decrypt(value string) (string, error) {
	data, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < aead.NonceSize() {
		return "", errors.New("invalid encrypted memory")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt memory: %w", err)
	}
	return string(plain), nil
}

func memoryTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return truncateRunes(line, 80)
		}
	}
	return "未命名记录"
}

func memorySnippet(content string, terms []string) string {
	compact := strings.Join(strings.Fields(content), " ")
	if compact == "" {
		return ""
	}
	lower := strings.ToLower(compact)
	position := -1
	for _, term := range terms {
		if found := strings.Index(lower, term); found >= 0 && (position < 0 || found < position) {
			position = found
		}
	}
	if position < 0 {
		position = 0
	}
	start := position - 70
	if start < 0 {
		start = 0
	}
	for start > 0 && !utf8.RuneStart(compact[start]) {
		start--
	}
	end := start + 220
	if end > len(compact) {
		end = len(compact)
	}
	for end < len(compact) && !utf8.RuneStart(compact[end]) {
		end--
	}
	snippet := compact[start:end]
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(compact) {
		snippet += "…"
	}
	return snippet
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func maskMemorySecrets(content string) string {
	return memorySecretPattern.ReplaceAllString(content, "$1••••••••")
}
