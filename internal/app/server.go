package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"floe/internal/core"
)

//go:embed web/*
var webFiles embed.FS

type Server struct {
	dataDir         string
	manager         *core.Manager
	sessionStore    *sessionStore
	bookmarks       *bookmarkStore
	transfers       *core.TransferEngine
	connectProvider func(core.ConnectRequest) (core.ProviderInfo, error)
	activity        *activityLog
	askPassMu       sync.Mutex
	askPassTokens   map[string]askPassSecret
	listener        net.Listener
	httpServer      *http.Server
	origin          string
	monitorCancel   context.CancelFunc
	htmlPreviewMu   sync.Mutex
	htmlPreviews    map[string]htmlPreviewDocument
}

func New(dataDir string) (*Server, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	manager := core.NewManager()
	if err := createBaseProvider(manager, dataDir); err != nil {
		return nil, err
	}
	sessionStore, err := newSessionStore(dataDir)
	if err != nil {
		return nil, err
	}
	bookmarks, err := newBookmarkStore(dataDir)
	if err != nil {
		return nil, err
	}
	server := &Server{
		dataDir: dataDir, manager: manager, sessionStore: sessionStore,
		bookmarks:       bookmarks,
		transfers:       core.NewTransferEngine(manager, filepath.Join(dataDir, "tasks.json")),
		connectProvider: manager.Connect,
		activity:        newActivityLog(dataDir),
		askPassTokens:   make(map[string]askPassSecret),
		htmlPreviews:    make(map[string]htmlPreviewDocument),
	}
	server.activity.Add("info", "system", "Floe Core 已启动", "")
	return server, nil
}

func createBaseProvider(manager *core.Manager, dataDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		home = dataDir
	}
	local, err := core.NewLocalFSWithKind("local", "本地", home, "local", "本地")
	if err != nil {
		return err
	}
	manager.Add(local)
	return nil
}

func randomToken(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func (s *Server) Start(address string) (string, <-chan error, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return "", nil, err
	}
	s.listener = listener
	origin, originErr := friendlyLoopbackOrigin(listener.Addr().String())
	if originErr != nil {
		_ = listener.Close()
		return "", nil, originErr
	}
	// Keep the server bound to the numeric loopback address, but show a more
	// readable URL in the browser and Windows Terminal AskPass flow.
	s.origin = origin
	mux := http.NewServeMux()
	mux.HandleFunc("GET /askpass/{token}", s.askPass)
	mux.HandleFunc("/api/", s.api)
	mux.HandleFunc("/", s.static)
	s.httpServer = &http.Server{
		Handler: securityHeaders(s.requireLocalAccess(mux)), ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20,
	}
	done := make(chan error, 1)
	go func() { done <- s.httpServer.Serve(listener) }()
	monitorContext, cancelMonitor := context.WithCancel(context.Background())
	s.monitorCancel = cancelMonitor
	go s.monitorTransferActivity(monitorContext)
	return s.origin + "/", done, nil
}

func friendlyLoopbackOrigin(listenerAddress string) (string, error) {
	_, port, err := net.SplitHostPort(listenerAddress)
	if err != nil {
		return "", err
	}
	return "http://localhost:" + port, nil
}

// LaunchURL returns the stable local application URL.
func (s *Server) LaunchURL() string {
	return s.origin + "/"
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.monitorCancel != nil {
		s.monitorCancel()
	}
	_ = s.manager.Close()
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireLocalAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isLocalHost(r.Host) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
				http.Error(w, "invalid origin", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) isLocalHost(requestHost string) bool {
	host, port, err := net.SplitHostPort(requestHost)
	if err != nil {
		return false
	}
	_, expectedPort, err := net.SplitHostPort(strings.TrimPrefix(s.origin, "http://"))
	if err != nil || port != expectedPort {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	file := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if file == "" || file == "." {
		file = "index.html"
	}
	data, err := webFiles.ReadFile("web/" + file)
	if err != nil {
		data, err = webFiles.ReadFile("web/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		file = "index.html"
	}
	if contentType := mime.TypeByExtension(path.Ext(file)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/session":
		// Kept for compatibility with pages loaded from pre-v0.2.3 builds.
		writeJSON(w, http.StatusOK, map[string]string{"csrf": "", "name": "Floe"})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/providers":
		writeJSON(w, http.StatusOK, s.providers())
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/bookmarks":
		writeJSON(w, http.StatusOK, s.bookmarks.List())
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/bookmarks":
		s.saveBookmarks(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
		s.saveSession(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/private-key":
		s.uploadPrivateKey(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/sessions/") && strings.HasSuffix(r.URL.Path, "/copy"):
		s.copySession(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/sessions/"):
		s.sessionDetails(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/sessions/"):
		s.deleteSession(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/connections/"):
		s.connectSaved(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/files":
		s.listFiles(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/files/thumbnail":
		s.thumbnail(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/files/raw":
		s.rawFile(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/files/media":
		s.mediaFile(w, r)
	case r.URL.Path == "/api/v1/files/content" && r.Method == http.MethodGet:
		s.readContent(w, r)
	case r.URL.Path == "/api/v1/files/content" && r.Method == http.MethodPut:
		s.writeContent(w, r)
	case r.URL.Path == "/api/v1/files/html-preview" && r.Method == http.MethodPost:
		s.createHTMLPreview(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/files/html-preview/") && r.Method == http.MethodGet:
		s.serveHTMLPreview(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/files/html-preview/") && r.Method == http.MethodDelete:
		s.deleteHTMLPreview(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/files/html-resource/") && r.Method == http.MethodGet:
		s.serveHTMLPreviewResource(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/files/mkdir":
		s.mkdir(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/files/create":
		s.createFile(w, r)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/files":
		s.deleteFile(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/local/directories":
		writeJSON(w, http.StatusOK, localDirectories())
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/local/tree":
		s.localTree(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/local/tabs":
		s.createLocalTab(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/transfers":
		s.createTransfer(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/transfers":
		s.listTransfers(w)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/transfers":
		s.clearTransfers(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/transfers/"):
		s.transferAction(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/transfers/"):
		s.deleteTransfer(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/events":
		s.events(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/logs":
		writeJSON(w, http.StatusOK, s.activity.List(500))
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/logs":
		s.addClientLog(w, r)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/logs":
		writeJSON(w, http.StatusOK, map[string]int{"removed": s.activity.Clear()})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/terminal/tabs":
		s.openTerminal(w, r)
	default:
		writeError(w, http.StatusNotFound, "API_NOT_FOUND", "API endpoint not found", "")
	}
}

func (s *Server) saveBookmarks(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Key     string         `json:"key"`
		Entries []pathBookmark `json:"entries"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.bookmarks.Save(request.Key, request.Entries); err != nil {
		writeError(w, http.StatusBadRequest, "BOOKMARK_SAVE_FAILED", "保存书签失败", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) addClientLog(w http.ResponseWriter, r *http.Request) {
	var entry struct{ Level, Category, Message, Detail string }
	if !decodeJSON(w, r, &entry) {
		return
	}
	if entry.Level != "info" && entry.Level != "warning" && entry.Level != "error" {
		entry.Level = "error"
	}
	entry.Category = strings.TrimSpace(entry.Category)
	entry.Message = strings.TrimSpace(entry.Message)
	if entry.Category == "" {
		entry.Category = "browser"
	}
	if entry.Message == "" {
		writeError(w, http.StatusBadRequest, "LOG_INVALID", "日志内容不能为空", "")
		return
	}
	entry.Category = truncateText(entry.Category, 40)
	entry.Message = truncateText(entry.Message, 240)
	entry.Detail = truncateText(entry.Detail, 2000)
	s.activity.Add(entry.Level, entry.Category, entry.Message, entry.Detail)
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func truncateText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func (s *Server) providers() []core.ProviderInfo {
	connected := s.manager.List()
	connectedIDs := make(map[string]bool, len(connected))
	for _, provider := range connected {
		connectedIDs[provider.ID] = true
	}
	for _, saved := range s.sessionStore.List() {
		if !connectedIDs[saved.ID] {
			connected = append(connected, saved)
		}
	}
	return connected
}

func (s *Server) saveSession(w http.ResponseWriter, r *http.Request) {
	var req core.ConnectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	provider, err := s.sessionStore.Save(req)
	if err != nil {
		s.activity.Add("error", "session", "保存会话失败", err.Error())
		writeError(w, http.StatusBadRequest, "SESSION_SAVE_FAILED", "保存会话失败", err.Error())
		return
	}
	if req.ID != "" {
		s.manager.Remove(req.ID)
	}
	s.activity.Add("info", "session", "已保存会话 "+provider.Name, provider.Location)
	writeJSON(w, http.StatusCreated, provider)
}

func (s *Server) uploadPrivateKey(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PRIVATE_KEY_READ_FAILED", "无法读取 SSH 私钥文件", err.Error())
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "PRIVATE_KEY_EMPTY", "SSH 私钥文件不能为空", "")
		return
	}
	if !strings.Contains(string(data), "PRIVATE KEY") {
		writeError(w, http.StatusBadRequest, "PRIVATE_KEY_INVALID", "请选择 OpenSSH 或 PEM 格式的私钥文件", "公钥认证需要客户端私钥，不能选择 .pub 公钥文件")
		return
	}
	directory := filepath.Join(s.dataDir, "keys")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "PRIVATE_KEY_SAVE_FAILED", "无法保存 SSH 私钥文件", err.Error())
		return
	}
	keyPath := filepath.Join(directory, "identity-"+randomToken(16))
	if err := os.WriteFile(keyPath, data, 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, "PRIVATE_KEY_SAVE_FAILED", "无法保存 SSH 私钥文件", err.Error())
		return
	}
	s.activity.Add("info", "session", "已导入 SSH 私钥文件", filepath.Base(keyPath))
	writeJSON(w, http.StatusCreated, map[string]string{"path": keyPath})
}

func sessionIDFromPath(value string) (string, bool) {
	id := strings.TrimPrefix(value, "/api/v1/sessions/")
	return id, id != "" && !strings.Contains(id, "/")
}

func sessionCopyIDFromPath(value string) (string, bool) {
	value = strings.TrimPrefix(value, "/api/v1/sessions/")
	id := strings.TrimSuffix(value, "/copy")
	return id, id != "" && id != value && !strings.Contains(id, "/")
}

func (s *Server) copySession(w http.ResponseWriter, r *http.Request) {
	id, valid := sessionCopyIDFromPath(r.URL.Path)
	if !valid {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "会话不存在", "")
		return
	}
	provider, err := s.sessionStore.Copy(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "SESSION_COPY_FAILED", "复制会话失败", err.Error())
		return
	}
	s.activity.Add("info", "session", "已复制会话 "+provider.Name, provider.Location)
	writeJSON(w, http.StatusCreated, provider)
}

func (s *Server) sessionDetails(w http.ResponseWriter, r *http.Request) {
	id, valid := sessionIDFromPath(r.URL.Path)
	if !valid {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "会话不存在", "")
		return
	}
	details, err := s.sessionStore.Details(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "会话不存在", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, details)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	id, valid := sessionIDFromPath(r.URL.Path)
	if !valid {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "会话不存在", "")
		return
	}
	details, err := s.sessionStore.Details(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "会话不存在", err.Error())
		return
	}
	if err := s.sessionStore.Delete(id); err != nil {
		writeError(w, http.StatusBadRequest, "SESSION_DELETE_FAILED", "删除会话失败", err.Error())
		return
	}
	s.manager.Remove(id)
	s.activity.Add("info", "session", "已删除会话 "+details.Name, details.Host)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) connectSaved(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/connections/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "会话不存在", "")
		return
	}
	if provider, ok := s.manager.Get(id); ok {
		writeJSON(w, http.StatusOK, core.ProviderInfo{
			ID: provider.ID(), Name: provider.Name(), Kind: provider.Kind(), Group: provider.Group(),
			Location: provider.Location(), Connected: true,
		})
		return
	}
	var confirmation struct{ Fingerprint string }
	if !decodeJSON(w, r, &confirmation) {
		return
	}
	req, err := s.sessionStore.Request(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "SESSION_NOT_FOUND", "会话不存在", err.Error())
		return
	}
	if confirmation.Fingerprint != "" {
		req.Fingerprint = confirmation.Fingerprint
	}
	provider, err := s.connectProvider(req)
	if err != nil {
		if conflict, ok := hostKeyConflict(err); ok {
			if conflict["code"] == "HOST_KEY_CHANGED" {
				s.activity.Add("warning", req.Protocol, "服务器主机密钥已变化 "+req.Name,
					fmt.Sprintf("旧指纹 %s · 新指纹 %s", conflict["expected"], conflict["received"]))
			}
			writeJSON(w, http.StatusConflict, conflict)
			return
		}
		s.activity.Add("error", req.Protocol, "连接失败 "+req.Name, fmt.Sprintf("%s:%d · %v", req.Host, req.Port, err))
		writeError(w, http.StatusBadGateway, "CONNECT_FAILED", strings.ToUpper(req.Protocol)+" 连接失败", err.Error())
		return
	}
	if confirmation.Fingerprint != "" {
		_ = s.sessionStore.TrustFingerprint(id, confirmation.Fingerprint)
	}
	s.activity.Add("info", req.Protocol, "连接成功 "+req.Name, fmt.Sprintf("%s:%d", req.Host, req.Port))
	writeJSON(w, http.StatusCreated, provider)
}

func hostKeyConflict(err error) (map[string]any, bool) {
	var changed *core.HostKeyChangedError
	if errors.As(err, &changed) {
		return map[string]any{
			"code": "HOST_KEY_CHANGED", "message": "服务器主机密钥已变化",
			"expected": changed.Expected, "received": changed.Received,
		}, true
	}
	var unknown *core.UnknownHostKeyError
	if errors.As(err, &unknown) {
		return map[string]any{
			"code": "HOST_KEY_UNKNOWN", "message": "请核对服务器主机指纹", "fingerprint": unknown.Fingerprint,
		}, true
	}
	return nil, false
}

func (s *Server) provider(w http.ResponseWriter, id string) (core.FileSystem, bool) {
	provider, ok := s.manager.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "PROVIDER_NOT_FOUND", "连接不存在或已经断开", "")
		return nil, false
	}
	return provider, true
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.provider(w, r.URL.Query().Get("provider"))
	if !ok {
		return
	}
	remotePath := r.URL.Query().Get("path")
	if remotePath == "" {
		remotePath = "/"
	}
	entries, err := provider.List(remotePath)
	if err != nil {
		s.activity.Add("error", provider.Kind(), "读取目录失败", fmt.Sprintf("%s %s · %v", provider.Name(), remotePath, err))
		if provider.Kind() != "local" && core.IsConnectionError(err) {
			s.manager.Remove(provider.ID())
			writeError(w, http.StatusBadGateway, "CONNECTION_LOST", "连接已断开，正在重新连接", err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "LIST_FAILED", "无法读取目录", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": remotePath, "display_path": provider.DisplayPath(remotePath), "entries": entries})
}

func (s *Server) rawFile(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.provider(w, r.URL.Query().Get("provider"))
	if !ok {
		return
	}
	remotePath := r.URL.Query().Get("path")
	contentType := mime.TypeByExtension(strings.ToLower(path.Ext(remotePath)))
	if !strings.HasPrefix(contentType, "image/") {
		writeError(w, http.StatusUnsupportedMediaType, "IMAGE_UNSUPPORTED", "该文件不是浏览器支持的图片", "")
		return
	}
	info, err := provider.Stat(remotePath)
	if err != nil || info.IsDir {
		writeError(w, http.StatusBadRequest, "IMAGE_READ_FAILED", "无法读取图片", errorDetail(err))
		return
	}
	reader, err := provider.OpenRead(remotePath)
	if err != nil {
		writeError(w, http.StatusBadGateway, "IMAGE_READ_FAILED", "无法读取图片", err.Error())
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, path.Base(remotePath), info.Modified, io.NewSectionReader(reader, 0, info.Size))
}

func errorDetail(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) {
	var req struct{ Provider, Path string }
	if !decodeJSON(w, r, &req) {
		return
	}
	provider, ok := s.provider(w, req.Provider)
	if !ok {
		return
	}
	if err := provider.Mkdir(req.Path); err != nil {
		writeError(w, http.StatusBadRequest, "MKDIR_FAILED", "新建目录失败", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (s *Server) createFile(w http.ResponseWriter, r *http.Request) {
	var req struct{ Provider, Path string }
	if !decodeJSON(w, r, &req) {
		return
	}
	provider, ok := s.provider(w, req.Provider)
	if !ok {
		return
	}
	if _, err := provider.Stat(req.Path); err == nil {
		writeError(w, http.StatusConflict, "FILE_EXISTS", "文件已经存在", "")
		return
	}
	if err := provider.WriteFileAtomic(req.Path, nil); err != nil {
		writeError(w, http.StatusBadRequest, "CREATE_FAILED", "新建文件失败", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	var req struct{ Provider, Path string }
	if !decodeJSON(w, r, &req) {
		return
	}
	provider, ok := s.provider(w, req.Provider)
	if !ok {
		return
	}
	if err := provider.Remove(req.Path); err != nil {
		writeError(w, http.StatusBadRequest, "DELETE_FAILED", "删除失败", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type localDirectory struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func localDirectories() []localDirectory {
	result := make([]localDirectory, 0, 12)
	seen := make(map[string]bool)
	add := func(name, value string) {
		if value == "" {
			return
		}
		clean := filepath.Clean(value)
		key := strings.ToLower(clean)
		if seen[key] {
			return
		}
		seen[key] = true
		result = append(result, localDirectory{Name: name, Path: clean})
	}
	for _, root := range logicalRoots() {
		add(root.Name, root.Path)
	}
	home, _ := os.UserHomeDir()
	for _, directory := range []localDirectory{
		{Name: "用户目录", Path: home},
		{Name: "桌面", Path: filepath.Join(home, "Desktop")},
		{Name: "下载", Path: filepath.Join(home, "Downloads")},
		{Name: "文档", Path: filepath.Join(home, "Documents")},
	} {
		if info, err := os.Stat(directory.Path); err == nil && info.IsDir() {
			add(directory.Name, directory.Path)
		}
	}
	return result
}

func (s *Server) localTree(w http.ResponseWriter, r *http.Request) {
	requested := r.URL.Query().Get("path")
	if requested == "" {
		writeJSON(w, http.StatusOK, logicalRoots())
		return
	}
	root, err := filepath.Abs(requested)
	if err != nil {
		writeError(w, http.StatusBadRequest, "LOCAL_PATH_INVALID", "本地路径无效", err.Error())
		return
	}
	items, err := os.ReadDir(root)
	if err != nil {
		writeError(w, http.StatusBadRequest, "LOCAL_TREE_FAILED", "无法读取本地目录", err.Error())
		return
	}
	result := make([]localDirectory, 0, len(items))
	for _, item := range items {
		if !item.IsDir() {
			continue
		}
		result = append(result, localDirectory{Name: item.Name(), Path: filepath.Join(root, item.Name())})
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) createLocalTab(w http.ResponseWriter, r *http.Request) {
	var req struct{ Path string }
	if !decodeJSON(w, r, &req) {
		return
	}
	root, err := filepath.Abs(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "LOCAL_PATH_INVALID", "本地路径无效", err.Error())
		return
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, "LOCAL_PATH_INVALID", "本地目录不存在", "")
		return
	}
	name := filepath.Base(root)
	if volume := filepath.VolumeName(root); filepath.Clean(root) == volume+string(filepath.Separator) {
		name = volume
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = root
	}
	id := fmt.Sprintf("local-%d", time.Now().UnixNano())
	provider, err := core.NewLocalFSWithKind(id, name, root, "local", "本地")
	if err != nil {
		writeError(w, http.StatusBadRequest, "LOCAL_TAB_FAILED", "无法创建本地标签", err.Error())
		return
	}
	s.manager.Add(provider)
	writeJSON(w, http.StatusCreated, core.ProviderInfo{ID: id, Name: name, Kind: "local", Group: "本地", Location: root, Connected: true})
}

func (s *Server) readContent(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.provider(w, r.URL.Query().Get("provider"))
	if !ok {
		return
	}
	data, err := provider.ReadFile(r.URL.Query().Get("path"), textPreviewLimit)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PREVIEW_FAILED", "无法预览文件", err.Error())
		return
	}
	document, err := decodeTextDocument(data)
	if err != nil {
		writeError(w, http.StatusUnsupportedMediaType, "BINARY_FILE", err.Error(), "")
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func (s *Server) writeContent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider, Path, Content, ETag string
		Encoding                      string `json:"encoding"`
		BOM                           bool   `json:"bom"`
		Newline                       string `json:"newline"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	provider, ok := s.provider(w, req.Provider)
	if !ok {
		return
	}
	current, err := provider.ReadFile(req.Path, textPreviewLimit)
	if err != nil {
		writeError(w, http.StatusBadRequest, "READ_FAILED", "保存前无法读取远程文件", err.Error())
		return
	}
	digest := sha256.Sum256(current)
	if req.ETag == "" || req.ETag != hex.EncodeToString(digest[:]) {
		writeError(w, http.StatusPreconditionFailed, "EDIT_CONFLICT", "远程文件已经改变，请重新加载", "")
		return
	}
	currentDocument, err := decodeTextDocument(current)
	if err != nil {
		writeError(w, http.StatusUnsupportedMediaType, "BINARY_FILE", err.Error(), "")
		return
	}
	if req.Encoding == "" {
		req.Encoding = currentDocument.Encoding
		req.BOM = currentDocument.BOM
	}
	if req.Newline == "" {
		req.Newline = currentDocument.Newline
	}
	encoded, err := encodeTextDocument(req.Content, req.Encoding, req.BOM, req.Newline)
	if err != nil {
		writeError(w, http.StatusBadRequest, "TEXT_FORMAT_INVALID", "无法按指定文本格式保存", err.Error())
		return
	}
	if err := provider.WriteFileAtomic(req.Path, encoded); err != nil {
		writeError(w, http.StatusBadGateway, "SAVE_FAILED", "保存远程文件失败", err.Error())
		return
	}
	document, _ := decodeTextDocument(encoded)
	writeJSON(w, http.StatusOK, document)
}

func (s *Server) createTransfer(w http.ResponseWriter, r *http.Request) {
	var req core.TransferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	source, ok := s.provider(w, req.SourceProvider)
	if !ok {
		return
	}
	target, ok := s.provider(w, req.TargetProvider)
	if !ok {
		return
	}
	info, err := source.Stat(req.SourcePath)
	if err != nil {
		s.activity.Add("error", "transfer", "无法创建传输任务", err.Error())
		writeError(w, http.StatusBadRequest, "TRANSFER_FAILED", "无法读取源文件", err.Error())
		return
	}
	if info.IsDir {
		tasks := make([]core.TransferTask, 0)
		if err := s.enqueueDirectory(source, target, req, &tasks, 2000); err != nil {
			s.activity.Add("error", "transfer", "目录传输创建失败", err.Error())
			writeError(w, http.StatusBadRequest, "TRANSFER_FAILED", "无法创建目录传输任务", err.Error())
			return
		}
		for i := range tasks {
			tasks[i].Blocks = nil
		}
		writeJSON(w, http.StatusCreated, map[string]any{"directory": true, "tasks": tasks})
		s.activity.Add("info", "transfer", "目录传输已加入队列", fmt.Sprintf("%s → %s · %d 个文件", req.SourcePath, req.TargetPath, len(tasks)))
		return
	}
	task, err := s.transfers.Create(req)
	if err != nil {
		s.activity.Add("error", "transfer", "传输任务创建失败", err.Error())
		writeError(w, http.StatusBadRequest, "TRANSFER_FAILED", "无法创建传输任务", err.Error())
		return
	}
	task.Blocks = nil
	s.activity.Add("info", "transfer", "传输已加入队列", req.SourcePath+" → "+req.TargetPath)
	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) enqueueDirectory(source, target core.FileSystem, req core.TransferRequest, tasks *[]core.TransferTask, limit int) error {
	if len(*tasks) >= limit {
		return fmt.Errorf("目录中文件超过 %d 个限制", limit)
	}
	if err := target.Mkdir(req.TargetPath); err != nil {
		return err
	}
	entries, err := source.List(req.SourcePath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsLink {
			continue
		}
		child := req
		child.SourcePath = path.Join(req.SourcePath, entry.Name)
		child.TargetPath = path.Join(req.TargetPath, entry.Name)
		if entry.IsDir {
			if err := s.enqueueDirectory(source, target, child, tasks, limit); err != nil {
				return err
			}
			continue
		}
		if len(*tasks) >= limit {
			return fmt.Errorf("目录中文件超过 %d 个限制", limit)
		}
		child.Concurrency = 1
		task, err := s.transfers.Create(child)
		if err != nil {
			return err
		}
		*tasks = append(*tasks, task)
	}
	return nil
}

func (s *Server) listTransfers(w http.ResponseWriter) {
	tasks := s.transfers.List()
	for i := range tasks {
		tasks[i].Blocks = nil
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) clearTransfers(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	removed, err := s.transfers.Clear(status)
	if err != nil {
		writeError(w, http.StatusBadRequest, "TASK_CLEAR_FAILED", "无法清空任务历史", err.Error())
		return
	}
	label := map[string]string{"completed": "成功", "failed": "失败"}[status]
	s.activity.Add("info", "transfer", "已清空"+label+"任务", fmt.Sprintf("%d 条", removed))
	writeJSON(w, http.StatusOK, map[string]int{"removed": removed})
}

func (s *Server) transferAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/transfers/"), "/")
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "ACTION_NOT_FOUND", "任务操作不存在", "")
		return
	}
	var err error
	switch parts[1] {
	case "pause":
		err = s.transfers.Pause(parts[0])
	case "resume":
		err = s.resumeTransfer(parts[0])
	default:
		writeError(w, http.StatusNotFound, "ACTION_NOT_FOUND", "任务操作不存在", "")
		return
	}
	if err != nil {
		if parts[1] == "resume" {
			if conflict, ok := hostKeyConflict(err); ok {
				writeJSON(w, http.StatusConflict, conflict)
				return
			}
		}
		writeError(w, http.StatusBadRequest, "ACTION_FAILED", "任务操作失败", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) resumeTransfer(id string) error {
	task, ok := s.transfers.Get(id)
	if !ok {
		return errors.New("任务不存在")
	}
	if err := s.ensureTransferProvider(task.SourceProvider, "源"); err != nil {
		return err
	}
	if err := s.ensureTransferProvider(task.TargetProvider, "目标"); err != nil {
		return err
	}
	return s.transfers.Resume(id)
}

func (s *Server) ensureTransferProvider(id, role string) error {
	if _, ok := s.manager.Get(id); ok {
		return nil
	}
	request, err := s.sessionStore.Request(id)
	if err != nil {
		if strings.HasPrefix(id, "local-") {
			return fmt.Errorf("%s本地标签已失效，请重新创建本地标签后新建任务", role)
		}
		return fmt.Errorf("%s会话不存在，无法恢复连接: %w", role, err)
	}
	if s.connectProvider == nil {
		s.connectProvider = s.manager.Connect
	}
	if _, err := s.connectProvider(request); err != nil {
		s.activity.Add("error", request.Protocol, "续传前连接失败 "+request.Name,
			fmt.Sprintf("%s端 · %s:%d · %v", role, request.Host, request.Port, err))
		return fmt.Errorf("%s会话“%s”连接失败: %w", role, request.Name, err)
	}
	s.activity.Add("info", request.Protocol, "续传前已连接 "+request.Name,
		fmt.Sprintf("%s端 · %s:%d", role, request.Host, request.Port))
	return nil
}

func (s *Server) deleteTransfer(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/transfers/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "TASK_NOT_FOUND", "任务不存在", "")
		return
	}
	if err := s.transfers.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, "TASK_NOT_FOUND", "任务不存在", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	updates, unsubscribe := s.transfers.Subscribe()
	defer unsubscribe()
	logUpdates, unsubscribeLogs := s.activity.Subscribe()
	defer unsubscribeLogs()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	sendTasks := func() {
		tasks := s.transfers.List()
		for i := range tasks {
			tasks[i].Blocks = nil
		}
		data, _ := json.Marshal(tasks)
		_, _ = fmt.Fprintf(w, "event: tasks\ndata: %s\n\n", data)
		flusher.Flush()
	}
	sendLogs := func() {
		data, _ := json.Marshal(s.activity.List(500))
		_, _ = fmt.Fprintf(w, "event: logs\ndata: %s\n\n", data)
		flusher.Flush()
	}
	sendTasks()
	sendLogs()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-updates:
			sendTasks()
		case <-logUpdates:
			sendLogs()
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "请求内容无效", err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message, detail string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message, "detail": detail})
}

func OpenBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	return command.Start()
}

func DefaultDataDir() string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "Floe")
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ".floe"
	}
	return filepath.Join(dir, "floe")
}

func LogServeError(err error) {
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("web server stopped", "error", err)
	}
}

var _ fs.FS = webFiles
