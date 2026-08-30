package app

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestMemoryStorePersistsAndSearches(t *testing.T) {
	directory := t.TempDir()
	store, err := newMemoryStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	content := "测试数据库账号\n地址 192.168.8.22\n用户 root\n密码: very-secret-value"
	saved, err := store.Save("", content, "floe", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "memories.db")); err != nil {
		t.Fatal(err)
	}
	results, err := store.List("数据库 192.168", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != saved.ID || results[0].BlockID == "" {
		t.Fatalf("search results = %#v", results)
	}
	details, err := store.Get(saved.ID)
	if err != nil || details.Content != content {
		t.Fatalf("memory details = %#v, err = %v", details, err)
	}
	results, err = store.List("very-secret-value", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("password value should not be searchable: %#v", results)
	}
	reloaded, err := newMemoryStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	results, err = reloaded.List("测试数据库", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("reloaded results = %#v", results)
	}
	details, err = reloaded.Get(saved.ID)
	if err != nil || details.Content != content {
		t.Fatalf("reloaded details = %#v, err = %v", details, err)
	}
}

func TestMemorySearchHistoryPersistsRanksAndDeletes(t *testing.T) {
	directory := t.TempDir()
	store, err := newMemoryStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordSearchHistory("docker"); err != nil {
		t.Fatal(err)
	}
	item, err := store.RecordSearchHistory("docker")
	if err != nil {
		t.Fatal(err)
	}
	if item.Query != "docker" || item.UseCount != 2 || item.LastUsedAt.IsZero() {
		t.Fatalf("recorded history = %#v", item)
	}
	if _, err := store.RecordSearchHistory("ssh"); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListSearchHistory("", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Query != "docker" || items[0].UseCount != 2 {
		t.Fatalf("ranked history = %#v", items)
	}
	filtered, err := store.ListSearchHistory("OCK", 100)
	if err != nil || len(filtered) != 1 || filtered[0].Query != "docker" {
		t.Fatalf("filtered history = %#v, err = %v", filtered, err)
	}
	reloaded, err := newMemoryStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	items, err = reloaded.ListSearchHistory("", 100)
	if err != nil || len(items) != 2 || items[0].Query != "docker" {
		t.Fatalf("reloaded history = %#v, err = %v", items, err)
	}
	if err := reloaded.DeleteSearchHistory("docker"); err != nil {
		t.Fatal(err)
	}
	items, err = reloaded.ListSearchHistory("", 100)
	if err != nil || len(items) != 1 || items[0].Query != "ssh" {
		t.Fatalf("deleted history = %#v, err = %v", items, err)
	}
}

func TestMemorySearchHistoryKeepsOnlyTopHundred(t *testing.T) {
	store, err := newMemoryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxMemorySearchHistory+1; index++ {
		if _, err := store.RecordSearchHistory(fmt.Sprintf("query-%03d", index)); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListSearchHistory("", maxMemorySearchHistory)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != maxMemorySearchHistory {
		t.Fatalf("history length = %d, want %d", len(items), maxMemorySearchHistory)
	}
}

func TestMemoryStoreMigratesEncryptedJSON(t *testing.T) {
	directory := t.TempDir()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	content := "旧知识库内容\n迁移后仍可搜索"
	secret, err := encryptLegacyMemoryForTest(key, content)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data, err := json.Marshal([]savedMemory{{
		ID: "legacy-memory", Secret: secret, Source: "import", SourcePath: "legacy.md",
		CreatedAt: now, UpdatedAt: now,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "memory.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "memories.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := newMemoryStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	results, err := store.List("迁移", 20)
	if err != nil || len(results) != 1 || results[0].ID != "legacy-memory" {
		t.Fatalf("migrated results = %#v, err = %v", results, err)
	}
	if err := store.Delete("legacy-memory"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newMemoryStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Count() != 0 {
		t.Fatal("deleted migrated content should not be imported again")
	}
}

func TestMemoryStoreKeepsFTSIndexInSync(t *testing.T) {
	store, err := newMemoryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save("", "旧版本使用 legacy-search-marker", "floe", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(saved.ID, "新版本使用 sqlite-search-marker", "floe", ""); err != nil {
		t.Fatal(err)
	}
	oldResults, err := store.List("legacy-search-marker", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldResults) != 0 {
		t.Fatalf("stale FTS results = %#v", oldResults)
	}
	newResults, err := store.List("sqlite-search-marker", 20)
	if err != nil || len(newResults) != 1 || newResults[0].ID != saved.ID {
		t.Fatalf("updated FTS results = %#v, err = %v", newResults, err)
	}
	if err := store.MarkUsed(saved.ID); err != nil {
		t.Fatal(err)
	}
	details, err := store.Get(saved.ID)
	if err != nil || details.UseCount != 1 || details.LastUsedAt.IsZero() {
		t.Fatalf("used memory = %#v, err = %v", details, err)
	}
	if err := store.Delete(saved.ID); err != nil {
		t.Fatal(err)
	}
	deletedResults, err := store.List("sqlite-search-marker", 20)
	if err != nil || len(deletedResults) != 0 {
		t.Fatalf("deleted FTS results = %#v, err = %v", deletedResults, err)
	}
}

func encryptLegacyMemoryForTest(key []byte, value string) (string, error) {
	block, err := aes.NewCipher(key)
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

func TestMemoryStreamLoadsWindowAroundAnchor(t *testing.T) {
	store, err := newMemoryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 120)
	for index := range lines {
		lines[index] = fmt.Sprintf("知识流第 %03d 行", index)
	}
	saved, err := store.Save("", strings.Join(lines, "\n"), "import", "large.md")
	if err != nil {
		t.Fatal(err)
	}
	anchor := memoryBlockID(saved.ID, 60)
	page, err := store.Stream(anchor, "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Blocks) != 20 || !page.HasBefore || !page.HasAfter {
		t.Fatalf("anchor page = %#v", page)
	}
	found := false
	for _, block := range page.Blocks {
		if block.ID == anchor {
			found = true
		}
	}
	if !found {
		t.Fatalf("anchor %q missing from page", anchor)
	}
	previous, err := store.Stream("", page.Blocks[0].ID, "", 10)
	if err != nil || len(previous.Blocks) != 10 {
		t.Fatalf("previous page = %#v, err = %v", previous, err)
	}
	next, err := store.Stream("", "", page.Blocks[len(page.Blocks)-1].ID, 10)
	if err != nil || len(next.Blocks) != 10 {
		t.Fatalf("next page = %#v, err = %v", next, err)
	}
}

func TestMemoryStreamFlowsAcrossStoredDocuments(t *testing.T) {
	store, err := newMemoryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Save("", "第一份内容 A\n第一份内容 B", "floe", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Save("", "第二份内容 A\n第二份内容 B", "import", "second.md")
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.Stream("", "", memoryBlockID(first.ID, 1), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Blocks) != 2 || page.Blocks[0].MemoryID != second.ID || !page.Blocks[0].DocumentStart {
		t.Fatalf("cross-document page = %#v", page)
	}
}

func TestMemorySearchReturnsIndependentFragmentsWithAnchors(t *testing.T) {
	store, err := newMemoryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Join([]string{
		"Nginx 排障记录",
		"第一次出现 502 时先查看上游服务。",
		"执行 docker logs api 查看接口日志。",
		"确认恢复后继续观察。",
		"",
		"这是与命中无关的较长中间说明。",
		"继续补充一些无关上下文。",
		"再补充一行以拉开两个命中位置。",
		"",
		"第二次出现 502 时检查 nginx error.log。",
		"如果上游超时，再检查网络连接。",
	}, "\n")
	if _, err := store.Save("", content, "floe", ""); err != nil {
		t.Fatal(err)
	}
	results, err := store.List("502", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("fragment count = %d, want 2: %#v", len(results), results)
	}
	if results[0].HitID == results[1].HitID {
		t.Fatalf("fragment hit ids must differ: %#v", results)
	}
	anchors := []int{results[0].AnchorLine, results[1].AnchorLine}
	sort.Ints(anchors)
	if anchors[0] != 1 || anchors[1] != 9 {
		t.Fatalf("fragment anchors = %v, want [1 9]", anchors)
	}
	if !strings.Contains(results[0].Snippet+results[1].Snippet, "docker logs") || !strings.Contains(results[0].Snippet+results[1].Snippet, "error.log") {
		t.Fatalf("fragment snippets do not preserve local context: %#v", results)
	}
}

func TestMemorySearchNormalizesPunctuationAndRanksPartialQueries(t *testing.T) {
	store, err := newMemoryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save("", strings.Join([]string{
		"部署记录：",
		"docker compose up -d api",
		"服务启动后返回 502；检查 nginx error.log",
		"备用命令：docker logs api",
	}, "\n"), "floe", ""); err != nil {
		t.Fatal(err)
	}

	results, err := store.List("ＤＯＣＫＥＲ，502", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("normalized query should find a stored fragment")
	}
	if !strings.Contains(results[0].Snippet, "docker") || !strings.Contains(results[0].Snippet, "502") {
		t.Fatalf("snippet = %q, want both query terms nearby", results[0].Snippet)
	}

	results, err = store.List("nginx 不存在的词", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("a remembered term should remain searchable when another term is absent")
	}
}

func TestMemorySearchPrefersWholePhraseAndSupportsQuotedQueries(t *testing.T) {
	store, err := newMemoryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exact, err := store.Save("", "前端发布命令 npm run build 完成后上传 dist", "floe", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save("", "npm 脚本说明\nrun 用于执行脚本\nbuild 是构建任务", "floe", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save("", "npm install 用于安装依赖", "floe", ""); err != nil {
		t.Fatal(err)
	}

	results, err := store.List("npm run build", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 3 {
		t.Fatalf("smart search results = %#v, want phrase, all-word and partial matches", results)
	}
	if results[0].ID != exact.ID || results[0].Match != "phrase" {
		t.Fatalf("first smart result = %#v, want exact phrase first", results[0])
	}
	if results[len(results)-1].Match != "partial" {
		t.Fatalf("last smart result = %#v, want partial fallback", results[len(results)-1])
	}

	results, err = store.List(`"npm run build"`, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != exact.ID || results[0].Match != "exact" {
		t.Fatalf("quoted results = %#v, want only exact phrase", results)
	}
}

func TestMemorySearchCombinesWordsWithQuotedPhraseInOneFragment(t *testing.T) {
	store, err := newMemoryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wanted, err := store.Save("", "docker 容器发布\n执行 npm run build\n复制构建产物", "floe", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save("", "本地执行 npm run build\n不使用容器", "floe", ""); err != nil {
		t.Fatal(err)
	}

	results, err := store.List(`docker "npm run build"`, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != wanted.ID || results[0].Match != "exact" {
		t.Fatalf("mixed quoted results = %#v", results)
	}
}

func TestMemorySettingsCopiesAndSwitchesStore(t *testing.T) {
	dataDir := t.TempDir()
	preferences, err := LoadPreferences(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewWithPreferences(dataDir, preferences)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.memoryStore().Save("", "需要迁移的速查内容", "floe", ""); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "knowledge")
	body, _ := json.Marshal(map[string]any{"path": target, "copy_existing": true})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/memory-settings", bytes.NewReader(body))
	response := httptest.NewRecorder()
	server.api(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("settings status = %d, body = %s", response.Code, response.Body.String())
	}
	if !samePath(server.memoryStore().Directory(), target) {
		t.Fatalf("memory directory = %q, want %q", server.memoryStore().Directory(), target)
	}
	results, err := server.memoryStore().List("迁移", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("copied results = %#v", results)
	}
	details, err := server.memoryStore().Get(results[0].ID)
	if err != nil || details.Content != "需要迁移的速查内容" {
		t.Fatalf("copied details = %#v, err = %v", details, err)
	}
	reloaded, err := LoadPreferences(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(reloaded.KnowledgeBaseDir(), target) {
		t.Fatalf("persisted knowledge base directory = %q", reloaded.KnowledgeBaseDir())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "memories.db")); err != nil {
		t.Fatalf("original knowledge base should remain as backup: %v", err)
	}
}

func TestCopyMemoryStoreRejectsExistingTarget(t *testing.T) {
	source, err := newMemoryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "memories.db"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = copyMemoryStoreFiles(source, target)
	if err == nil || !strings.Contains(err.Error(), "已经包含知识库") {
		t.Fatalf("copy error = %v", err)
	}
}
