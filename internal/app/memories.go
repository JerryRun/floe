package app

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
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

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

var memorySecretPattern = regexp.MustCompile(`(?i)((密码|口令|password|passwd|pwd)[[:space:]]*[:=：][[:space:]]*)([^[:space:],，;；]+)`)

const (
	maxMemoryEntries       = 10000
	maxMemoryBytes         = 1 << 20
	maxMemorySearchHistory = 100
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

type memorySearchHistoryEntry struct {
	Query      string    `json:"query"`
	LastUsedAt time.Time `json:"last_used_at"`
	UseCount   int       `json:"use_count"`
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
	mu   sync.RWMutex
	path string
	db   *sql.DB
}

func (s *memoryStore) Directory() string { return filepath.Dir(s.path) }

func (s *memoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

func (s *memoryStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&count); err != nil {
		return 0
	}
	return count
}

func newMemoryStore(dataDir string) (*memoryStore, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "memories.db")
	database, err := driver.Open(path, func(connection *sqlite3.Conn) error {
		if err := connection.BusyTimeout(5 * time.Second); err != nil {
			return err
		}
		for _, statement := range []string{
			`PRAGMA foreign_keys = ON`,
			`PRAGMA journal_mode = WAL`,
			`PRAGMA synchronous = NORMAL`,
		} {
			if err := connection.Exec(statement); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(4)
	database.SetMaxIdleConns(4)
	store := &memoryStore{path: path, db: database}
	if err := store.initialize(); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := store.migrateLegacy(dataDir); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (s *memoryStore) initialize() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			search_text TEXT NOT NULL,
			source TEXT NOT NULL,
			source_path TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			last_used_at INTEGER,
			use_count INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS memory_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS memory_search_history (
			query TEXT PRIMARY KEY COLLATE NOCASE,
			last_used_at INTEGER NOT NULL,
			use_count INTEGER NOT NULL DEFAULT 1
		);
		CREATE INDEX IF NOT EXISTS memory_search_history_rank ON memory_search_history(use_count DESC, last_used_at DESC);
		CREATE INDEX IF NOT EXISTS memories_created_at ON memories(created_at, id);
		CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
			search_text,
			content='memories',
			content_rowid='rowid',
			tokenize='trigram'
		);
		CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memory_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
		END;
		CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memory_fts(memory_fts, rowid, search_text) VALUES ('delete', old.rowid, old.search_text);
		END;
		CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE OF search_text ON memories BEGIN
			INSERT INTO memory_fts(memory_fts, rowid, search_text) VALUES ('delete', old.rowid, old.search_text);
			INSERT INTO memory_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
		END;
	`)
	if err != nil {
		return fmt.Errorf("initialize memory database: %w", err)
	}
	return nil
}

func (s *memoryStore) migrateLegacy(dataDir string) error {
	var migrated string
	err := s.db.QueryRow(`SELECT value FROM memory_meta WHERE key = 'legacy_json_migrated'`).Scan(&migrated)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		_, err := s.db.Exec(`INSERT INTO memory_meta(key, value) VALUES ('legacy_json_migrated', '1')`)
		return err
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "memories.json"))
	if errors.Is(err, os.ErrNotExist) {
		_, err := s.db.Exec(`INSERT INTO memory_meta(key, value) VALUES ('legacy_json_migrated', '1')`)
		return err
	}
	if err != nil {
		return err
	}
	key, err := os.ReadFile(filepath.Join(dataDir, "memory.key"))
	if err != nil {
		return err
	}
	if len(key) != 32 {
		return errors.New("invalid memory encryption key")
	}
	var items []savedMemory
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	transaction, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	for _, item := range items {
		if item.ID == "" || item.Secret == "" {
			continue
		}
		content, err := decryptLegacyMemory(key, item.Secret)
		if err != nil {
			return err
		}
		if _, err := transaction.Exec(`
			INSERT OR IGNORE INTO memories
				(id, content, search_text, source, source_path, created_at, updated_at, last_used_at, use_count)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, content, memorySearchIndexText(content), item.Source, item.SourcePath,
			memoryTimeUnix(item.CreatedAt), memoryTimeUnix(item.UpdatedAt), nullableMemoryTime(item.LastUsedAt), item.UseCount,
		); err != nil {
			return err
		}
	}
	if _, err := transaction.Exec(`INSERT INTO memory_meta(key, value) VALUES ('legacy_json_migrated', '1')`); err != nil {
		return err
	}
	return transaction.Commit()
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
	documents, err := s.searchDocumentsLocked(search)
	if err != nil {
		return nil, err
	}
	ranked := make([]rankedEntry, 0, len(documents))
	for _, document := range documents {
		searchableContent := maskMemorySecrets(document.Content)
		hits := memoryFragments(searchableContent, search)
		for _, hit := range hits {
			blockID := memoryBlockID(document.ID, hit.AnchorLine)
			ranked = append(ranked, rankedEntry{entry: memorySearchHit{
				ID: document.ID, HitID: blockID, BlockID: blockID, AnchorLine: hit.AnchorLine,
				Snippet: hit.Snippet, Match: hit.Match, Source: document.Source, SourcePath: document.SourcePath, UpdatedAt: document.UpdatedAt,
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

type storedMemory struct {
	ID         string
	Content    string
	Source     string
	SourcePath string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastUsedAt time.Time
	UseCount   int
}

func (s *memoryStore) searchDocumentsLocked(query memorySearchQuery) ([]storedMemory, error) {
	ftsQuery, indexed := memoryFTSQuery(query)
	statement := `SELECT id, content, source, source_path, created_at, updated_at, last_used_at, use_count FROM memories`
	var arguments []any
	if indexed {
		statement = `
			SELECT memories.id, memories.content, memories.source, memories.source_path,
				memories.created_at, memories.updated_at, memories.last_used_at, memories.use_count
			FROM memory_fts
			JOIN memories ON memories.rowid = memory_fts.rowid
			WHERE memory_fts MATCH ?`
		arguments = append(arguments, ftsQuery)
	} else {
		filter, filterArguments := memorySQLSearchFilter(query)
		statement += " WHERE " + filter
		arguments = append(arguments, filterArguments...)
	}
	return s.queryDocuments(statement, arguments...)
}

func (s *memoryStore) documentsLocked() ([]storedMemory, error) {
	return s.queryDocuments(`
		SELECT id, content, source, source_path, created_at, updated_at, last_used_at, use_count
		FROM memories
		ORDER BY created_at, id`)
}

func (s *memoryStore) queryDocuments(statement string, arguments ...any) ([]storedMemory, error) {
	rows, err := s.db.Query(statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	documents := make([]storedMemory, 0)
	for rows.Next() {
		var document storedMemory
		var createdAt, updatedAt int64
		var lastUsedAt sql.NullInt64
		if err := rows.Scan(
			&document.ID, &document.Content, &document.Source, &document.SourcePath,
			&createdAt, &updatedAt, &lastUsedAt, &document.UseCount,
		); err != nil {
			return nil, err
		}
		document.CreatedAt = memoryTimeFromUnix(createdAt)
		document.UpdatedAt = memoryTimeFromUnix(updatedAt)
		if lastUsedAt.Valid {
			document.LastUsedAt = memoryTimeFromUnix(lastUsedAt.Int64)
		}
		documents = append(documents, document)
	}
	return documents, rows.Err()
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
		lines := strings.Split(strings.ReplaceAll(document.Content, "\r\n", "\n"), "\n")
		for line, text := range lines {
			blocks = append(blocks, memoryBlock{
				ID: memoryBlockID(document.ID, line), MemoryID: document.ID, Line: line,
				Text: text, Source: document.Source, SourcePath: document.SourcePath,
				CreatedAt: document.CreatedAt, UpdatedAt: document.UpdatedAt,
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
	compact := compactMemorySearchText(haystack)
	compactTerm := compactMemorySearchText(term)
	if compactTerm == "" {
		return 0
	}
	return strings.Count(compact, compactTerm)
}

func compactMemorySearchText(value string) string {
	return strings.Map(func(char rune) rune {
		if unicode.IsSpace(char) || unicode.IsPunct(char) {
			return -1
		}
		return char
	}, value)
}

func memorySearchIndexText(content string) string {
	normalized := normalizeMemorySearchText(maskMemorySecrets(content))
	compact := compactMemorySearchText(normalized)
	if compact == "" || compact == normalized {
		return normalized
	}
	return normalized + "\n" + compact
}

func memoryFTSQuery(query memorySearchQuery) (string, bool) {
	groups := make([][]string, 0, len(query.Phrases)+len(query.Terms))
	for _, phrase := range query.Phrases {
		if utf8.RuneCountInString(phrase) < 3 {
			return "", false
		}
		groups = append(groups, []string{phrase})
	}
	for _, term := range query.Terms {
		values := []string{term}
		compact := compactMemorySearchText(term)
		if compact != "" && compact != term {
			values = append(values, compact)
		}
		for _, value := range values {
			if utf8.RuneCountInString(value) < 3 {
				return "", false
			}
		}
		groups = append(groups, values)
	}
	if len(groups) == 0 {
		return "", false
	}
	formatted := make([]string, 0, len(groups))
	for _, values := range groups {
		alternatives := make([]string, len(values))
		for index, value := range values {
			alternatives[index] = `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
		}
		group := strings.Join(alternatives, " OR ")
		if len(alternatives) > 1 {
			group = "(" + group + ")"
		}
		formatted = append(formatted, group)
	}
	operator := " OR "
	if query.Strict {
		operator = " AND "
	}
	return strings.Join(formatted, operator), true
}

func memorySQLSearchFilter(query memorySearchQuery) (string, []any) {
	groups := make([]string, 0, len(query.Phrases)+len(query.Terms))
	arguments := make([]any, 0, len(query.Phrases)+len(query.Terms)*2)
	for _, phrase := range query.Phrases {
		groups = append(groups, `instr(search_text, ?) > 0`)
		arguments = append(arguments, phrase)
	}
	for _, term := range query.Terms {
		values := []string{term}
		compact := compactMemorySearchText(term)
		if compact != "" && compact != term {
			values = append(values, compact)
		}
		checks := make([]string, len(values))
		for index, value := range values {
			checks[index] = `instr(search_text, ?) > 0`
			arguments = append(arguments, value)
		}
		group := strings.Join(checks, " OR ")
		if len(checks) > 1 {
			group = "(" + group + ")"
		}
		groups = append(groups, group)
	}
	operator := " OR "
	if query.Strict {
		operator = " AND "
	}
	return strings.Join(groups, operator), arguments
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
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	transaction, err := s.db.Begin()
	if err != nil {
		return memoryEntry{}, err
	}
	defer transaction.Rollback()
	var previous storedMemory
	var createdAtUnix, updatedAtUnix int64
	var lastUsedAt sql.NullInt64
	err = transaction.QueryRow(`
		SELECT id, content, source, source_path, created_at, updated_at, last_used_at, use_count
		FROM memories WHERE id = ?`, id,
	).Scan(
		&previous.ID, &previous.Content, &previous.Source, &previous.SourcePath,
		&createdAtUnix, &updatedAtUnix, &lastUsedAt, &previous.UseCount,
	)
	existed := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return memoryEntry{}, err
	}
	if existed {
		previous.CreatedAt = memoryTimeFromUnix(createdAtUnix)
		previous.UpdatedAt = memoryTimeFromUnix(updatedAtUnix)
		if lastUsedAt.Valid {
			previous.LastUsedAt = memoryTimeFromUnix(lastUsedAt.Int64)
		}
	}
	if id == "" {
		var count int
		if err := transaction.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&count); err != nil {
			return memoryEntry{}, err
		}
		if count >= maxMemoryEntries {
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
	if _, err := transaction.Exec(`
		INSERT INTO memories
			(id, content, search_text, source, source_path, created_at, updated_at, last_used_at, use_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			search_text = excluded.search_text,
			source = excluded.source,
			source_path = excluded.source_path,
			updated_at = excluded.updated_at`,
		id, content, memorySearchIndexText(content), source, sourcePath,
		memoryTimeUnix(createdAt), memoryTimeUnix(now), nullableMemoryTime(previous.LastUsedAt), previous.UseCount,
	); err != nil {
		return memoryEntry{}, err
	}
	if err := transaction.Commit(); err != nil {
		return memoryEntry{}, err
	}
	return memoryEntry{
		ID: id, Title: memoryTitle(content), Content: content,
		Source: source, SourcePath: sourcePath, CreatedAt: createdAt, UpdatedAt: now,
		LastUsedAt: previous.LastUsedAt, UseCount: previous.UseCount,
	}, nil
}

func (s *memoryStore) Get(id string) (memoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var item memoryEntry
	var createdAt, updatedAt int64
	var lastUsedAt sql.NullInt64
	err := s.db.QueryRow(`
		SELECT id, content, source, source_path, created_at, updated_at, last_used_at, use_count
		FROM memories WHERE id = ?`, id,
	).Scan(
		&item.ID, &item.Content, &item.Source, &item.SourcePath,
		&createdAt, &updatedAt, &lastUsedAt, &item.UseCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return memoryEntry{}, os.ErrNotExist
	}
	if err != nil {
		return memoryEntry{}, err
	}
	item.Title = memoryTitle(item.Content)
	item.CreatedAt = memoryTimeFromUnix(createdAt)
	item.UpdatedAt = memoryTimeFromUnix(updatedAt)
	if lastUsedAt.Valid {
		item.LastUsedAt = memoryTimeFromUnix(lastUsedAt.Int64)
	}
	return item, nil
}

func (s *memoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if removed == 0 {
		return os.ErrNotExist
	}
	return nil
}

func (s *memoryStore) MarkUsed(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`
		UPDATE memories
		SET last_used_at = ?, use_count = use_count + 1
		WHERE id = ?`, memoryTimeUnix(time.Now().UTC()), id,
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return os.ErrNotExist
	}
	return nil
}

func (s *memoryStore) ListSearchHistory(filter string, limit int) ([]memorySearchHistoryEntry, error) {
	if limit <= 0 || limit > maxMemorySearchHistory {
		limit = maxMemorySearchHistory
	}
	filter = strings.TrimSpace(filter)
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := `
		SELECT query, last_used_at, use_count
		FROM memory_search_history`
	arguments := []any{}
	if filter != "" {
		query += ` WHERE instr(lower(query), lower(?)) > 0`
		arguments = append(arguments, filter)
	}
	query += ` ORDER BY use_count DESC, last_used_at DESC, query COLLATE NOCASE LIMIT ?`
	arguments = append(arguments, limit)
	rows, err := s.db.Query(query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]memorySearchHistoryEntry, 0)
	for rows.Next() {
		var item memorySearchHistoryEntry
		var lastUsedAt int64
		if err := rows.Scan(&item.Query, &lastUsedAt, &item.UseCount); err != nil {
			return nil, err
		}
		item.LastUsedAt = memoryTimeFromUnix(lastUsedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *memoryStore) RecordSearchHistory(query string) (memorySearchHistoryEntry, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return memorySearchHistoryEntry{}, errors.New("搜索词不能为空")
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`
		INSERT INTO memory_search_history (query, last_used_at, use_count)
		VALUES (?, ?, 1)
		ON CONFLICT(query) DO UPDATE SET
			last_used_at = excluded.last_used_at,
			use_count = memory_search_history.use_count + 1`, query, memoryTimeUnix(now))
	if err != nil {
		return memorySearchHistoryEntry{}, err
	}
	_, err = s.db.Exec(`
		DELETE FROM memory_search_history
		WHERE query NOT IN (
			SELECT query FROM memory_search_history
			ORDER BY last_used_at DESC, query COLLATE NOCASE
			LIMIT ?
		)`, maxMemorySearchHistory)
	if err != nil {
		return memorySearchHistoryEntry{}, err
	}
	var item memorySearchHistoryEntry
	var lastUsedAt int64
	if err := s.db.QueryRow(`SELECT query, last_used_at, use_count FROM memory_search_history WHERE query = ?`, query).
		Scan(&item.Query, &lastUsedAt, &item.UseCount); err != nil {
		return memorySearchHistoryEntry{}, err
	}
	item.LastUsedAt = memoryTimeFromUnix(lastUsedAt)
	return item, nil
}

func (s *memoryStore) DeleteSearchHistory(query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return errors.New("搜索词不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`DELETE FROM memory_search_history WHERE query = ?`, query)
	if err != nil {
		return err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if removed == 0 {
		return os.ErrNotExist
	}
	return nil
}

func memoryTimeUnix(value time.Time) int64 {
	return value.UTC().UnixNano()
}

func nullableMemoryTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return memoryTimeUnix(value)
}

func memoryTimeFromUnix(value int64) time.Time {
	return time.Unix(0, value).UTC()
}

func decryptLegacyMemory(key []byte, value string) (string, error) {
	data, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
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
