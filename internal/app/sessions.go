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
	"sort"
	"strings"
	"sync"

	"floe/internal/core"
)

type savedSession struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Protocol             string `json:"protocol"`
	Host                 string `json:"host"`
	Port                 int    `json:"port"`
	User                 string `json:"user"`
	AuthMethod           string `json:"auth_method,omitempty"`
	Secret               string `json:"secret,omitempty"`
	PrivateKey           string `json:"private_key,omitempty"`
	Fingerprint          string `json:"fingerprint,omitempty"`
	Group                string `json:"group"`
	SSHKeepAlive         bool   `json:"ssh_keep_alive,omitempty"`
	ServerAliveInterval  int    `json:"server_alive_interval,omitempty"`
	ServerAliveCountMax  int    `json:"server_alive_count_max,omitempty"`
	TerminalAutoPassword *bool  `json:"terminal_auto_password,omitempty"`
}

type sessionDetails struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Protocol             string `json:"protocol"`
	Host                 string `json:"host"`
	Port                 int    `json:"port"`
	User                 string `json:"user"`
	AuthMethod           string `json:"auth_method"`
	HasPassword          bool   `json:"has_password"`
	PrivateKey           string `json:"private_key,omitempty"`
	Fingerprint          string `json:"fingerprint,omitempty"`
	Group                string `json:"group"`
	SSHKeepAlive         bool   `json:"ssh_keep_alive"`
	ServerAliveInterval  int    `json:"server_alive_interval"`
	ServerAliveCountMax  int    `json:"server_alive_count_max"`
	TerminalAutoPassword bool   `json:"terminal_auto_password"`
}

type sessionStore struct {
	mu      sync.RWMutex
	path    string
	keyPath string
	key     []byte
	items   map[string]savedSession
}

func newSessionStore(dataDir string) (*sessionStore, error) {
	store := &sessionStore{
		path: filepath.Join(dataDir, "sessions.json"), keyPath: filepath.Join(dataDir, "session.key"),
		items: make(map[string]savedSession),
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
		return nil, errors.New("invalid session encryption key")
	}
	store.key = key
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var items []savedSession
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID != "" {
			item = normalizeSavedSession(item)
			store.items[item.ID] = item
		}
	}
	return store, nil
}

func (s *sessionStore) Save(request core.ConnectRequest) (core.ProviderInfo, error) {
	request.Protocol = strings.ToLower(strings.TrimSpace(request.Protocol))
	if request.Protocol == "" {
		request.Protocol = "sftp"
	}
	if request.Protocol != "sftp" && request.Protocol != "ftp" {
		return core.ProviderInfo{}, errors.New("协议必须是 SFTP 或 FTP")
	}
	request.Host = strings.TrimSpace(request.Host)
	request.User = strings.TrimSpace(request.User)
	request.PrivateKey = strings.TrimSpace(request.PrivateKey)
	if request.Host == "" {
		return core.ProviderInfo{}, errors.New("主机不能为空")
	}
	if request.Protocol == "sftp" && request.User == "" {
		return core.ProviderInfo{}, errors.New("用户名不能为空")
	}
	if request.Protocol == "ftp" && request.User == "" {
		request.User = "anonymous"
	}
	if request.Port == 0 {
		if request.Protocol == "ftp" {
			request.Port = 21
		} else {
			request.Port = 22
		}
	}
	if request.Port < 1 || request.Port > 65535 {
		return core.ProviderInfo{}, errors.New("端口范围必须是 1–65535")
	}
	if request.Protocol == "ftp" {
		request.AuthMethod = "password"
		request.SSHKeepAlive = false
		request.ServerAliveInterval = 0
		request.ServerAliveCountMax = 0
		request.TerminalAutoPassword = nil
	} else {
		request.AuthMethod = strings.ToLower(strings.TrimSpace(request.AuthMethod))
		if request.AuthMethod == "" {
			if request.PrivateKey != "" {
				request.AuthMethod = "key"
			} else {
				request.AuthMethod = "password"
			}
		}
		if request.AuthMethod != "password" && request.AuthMethod != "key" {
			return core.ProviderInfo{}, errors.New("SFTP 登录方式必须是密码或 SSH 密钥文件")
		}
		if request.AuthMethod == "password" {
			request.PrivateKey = ""
		}
		if request.ServerAliveInterval == 0 {
			request.ServerAliveInterval = core.DefaultServerAliveInterval
		}
		if request.ServerAliveCountMax == 0 {
			request.ServerAliveCountMax = core.DefaultServerAliveCountMax
		}
		if request.SSHKeepAlive && (request.ServerAliveInterval < 5 || request.ServerAliveInterval > 3600) {
			return core.ProviderInfo{}, errors.New("SSH 心跳间隔必须在 5–3600 秒之间")
		}
		if request.SSHKeepAlive && (request.ServerAliveCountMax < 1 || request.ServerAliveCountMax > 20) {
			return core.ProviderInfo{}, errors.New("SSH 心跳失败次数必须在 1–20 之间")
		}
		if !request.SSHKeepAlive && (request.ServerAliveInterval < 5 || request.ServerAliveInterval > 3600) {
			request.ServerAliveInterval = core.DefaultServerAliveInterval
		}
		if !request.SSHKeepAlive && (request.ServerAliveCountMax < 1 || request.ServerAliveCountMax > 20) {
			request.ServerAliveCountMax = core.DefaultServerAliveCountMax
		}
		if request.TerminalAutoPassword == nil {
			enabled := true
			request.TerminalAutoPassword = &enabled
		}
	}
	if strings.TrimSpace(request.Name) == "" {
		request.Name = request.User + "@" + request.Host
	}
	if strings.TrimSpace(request.Group) == "" {
		request.Group = "我的会话"
	}
	if request.ID == "" {
		request.ID = "session-" + randomToken(10)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.items[request.ID]
	if existing.ID != "" && (existing.Protocol != request.Protocol || existing.Host != request.Host || existing.Port != request.Port) {
		request.Fingerprint = ""
	}
	secret := existing.Secret
	if request.ClearPassword {
		secret = ""
	}
	if existing.ID != "" && existing.AuthMethod != request.AuthMethod && request.Password == "" {
		secret = ""
	}
	if request.Password != "" {
		var err error
		secret, err = s.encrypt(request.Password)
		if err != nil {
			return core.ProviderInfo{}, err
		}
	}
	item := savedSession{
		ID: request.ID, Name: strings.TrimSpace(request.Name), Protocol: request.Protocol,
		Host: request.Host, Port: request.Port, User: request.User, AuthMethod: request.AuthMethod, Secret: secret,
		PrivateKey: request.PrivateKey, Fingerprint: request.Fingerprint,
		Group: strings.TrimSpace(request.Group), SSHKeepAlive: request.SSHKeepAlive,
		ServerAliveInterval: request.ServerAliveInterval, ServerAliveCountMax: request.ServerAliveCountMax,
		TerminalAutoPassword: cloneBool(request.TerminalAutoPassword),
	}
	s.items[item.ID] = item
	if err := s.persistLocked(); err != nil {
		return core.ProviderInfo{}, err
	}
	return item.providerInfo(false), nil
}

func (s *sessionStore) List() []core.ProviderInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]savedSession, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Group != items[j].Group {
			return items[i].Group < items[j].Group
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	result := make([]core.ProviderInfo, 0, len(items))
	for _, item := range items {
		result = append(result, item.providerInfo(false))
	}
	return result
}

func (s *sessionStore) Copy(id string) (core.ProviderInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original, ok := s.items[id]
	if !ok {
		return core.ProviderInfo{}, errors.New("会话不存在")
	}
	duplicate := original
	duplicate.Name = nextSessionCopyName(original.Name, s.items)
	for {
		duplicate.ID = "session-" + randomToken(10)
		if _, exists := s.items[duplicate.ID]; !exists {
			break
		}
	}
	s.items[duplicate.ID] = duplicate
	if err := s.persistLocked(); err != nil {
		delete(s.items, duplicate.ID)
		return core.ProviderInfo{}, err
	}
	return duplicate.providerInfo(false), nil
}

func nextSessionCopyName(name string, items map[string]savedSession) string {
	base := strings.TrimSpace(name)
	if base == "" {
		base = "会话"
	}
	start := 2
	if open := strings.LastIndex(base, " ("); open > 0 && strings.HasSuffix(base, ")") {
		var sequence int
		suffix := base[open:]
		if _, err := fmt.Sscanf(suffix, " (%d)", &sequence); err == nil && sequence >= 2 && suffix == fmt.Sprintf(" (%d)", sequence) {
			base = strings.TrimSpace(base[:open])
			start = sequence + 1
		}
	}
	names := make(map[string]bool, len(items))
	for _, item := range items {
		names[strings.ToLower(strings.TrimSpace(item.Name))] = true
	}
	for sequence := start; ; sequence++ {
		candidate := fmt.Sprintf("%s (%d)", base, sequence)
		if !names[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

func (s *sessionStore) Details(id string) (sessionDetails, error) {
	s.mu.RLock()
	item, ok := s.items[id]
	s.mu.RUnlock()
	if !ok {
		return sessionDetails{}, errors.New("会话不存在")
	}
	return sessionDetails{
		ID: item.ID, Name: item.Name, Protocol: item.Protocol, Host: item.Host, Port: item.Port,
		User: item.User, AuthMethod: item.AuthMethod, HasPassword: item.Secret != "", PrivateKey: item.PrivateKey,
		Fingerprint: item.Fingerprint, Group: item.Group, SSHKeepAlive: item.SSHKeepAlive,
		ServerAliveInterval: item.ServerAliveInterval, ServerAliveCountMax: item.ServerAliveCountMax,
		TerminalAutoPassword: boolValue(item.TerminalAutoPassword, true),
	}, nil
}

func (s *sessionStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return errors.New("会话不存在")
	}
	delete(s.items, id)
	return s.persistLocked()
}

func (s *sessionStore) Request(id string) (core.ConnectRequest, error) {
	s.mu.RLock()
	item, ok := s.items[id]
	s.mu.RUnlock()
	if !ok {
		return core.ConnectRequest{}, errors.New("会话不存在")
	}
	password, err := s.decrypt(item.Secret)
	if err != nil {
		return core.ConnectRequest{}, err
	}
	return core.ConnectRequest{
		ID: item.ID, Name: item.Name, Protocol: item.Protocol, Host: item.Host, Port: item.Port,
		User: item.User, AuthMethod: item.AuthMethod, Password: password, PrivateKey: item.PrivateKey,
		Fingerprint: item.Fingerprint, Group: item.Group, SSHKeepAlive: item.SSHKeepAlive,
		ServerAliveInterval: item.ServerAliveInterval, ServerAliveCountMax: item.ServerAliveCountMax,
		TerminalAutoPassword: cloneBool(item.TerminalAutoPassword),
	}, nil
}

func normalizeSavedSession(item savedSession) savedSession {
	if item.Protocol != "sftp" {
		item.AuthMethod = "password"
		item.SSHKeepAlive = false
		item.ServerAliveInterval = 0
		item.ServerAliveCountMax = 0
		item.TerminalAutoPassword = nil
		return item
	}
	item.AuthMethod = strings.ToLower(strings.TrimSpace(item.AuthMethod))
	if item.AuthMethod != "password" && item.AuthMethod != "key" {
		if item.PrivateKey != "" {
			item.AuthMethod = "key"
		} else {
			item.AuthMethod = "password"
		}
	}
	if item.ServerAliveInterval < 5 || item.ServerAliveInterval > 3600 {
		item.ServerAliveInterval = core.DefaultServerAliveInterval
	}
	if item.ServerAliveCountMax < 1 || item.ServerAliveCountMax > 20 {
		item.ServerAliveCountMax = core.DefaultServerAliveCountMax
	}
	if item.TerminalAutoPassword == nil {
		enabled := true
		item.TerminalAutoPassword = &enabled
	}
	return item
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func (s *sessionStore) TrustFingerprint(id, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return errors.New("会话不存在")
	}
	item.Fingerprint = fingerprint
	s.items[id] = item
	return s.persistLocked()
}

func (s *sessionStore) persistLocked() error {
	items := make([]savedSession, 0, len(s.items))
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

func (s *sessionStore) encrypt(value string) (string, error) {
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

func (s *sessionStore) decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
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
		return "", errors.New("invalid encrypted session secret")
	}
	nonce, encrypted := data[:aead.NonceSize()], data[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt session secret: %w", err)
	}
	return string(plain), nil
}

func (s savedSession) providerInfo(connected bool) core.ProviderInfo {
	return core.ProviderInfo{
		ID: s.ID, Name: s.Name, Kind: s.Protocol, Group: s.Group,
		Location: fmt.Sprintf("%s:%d", s.Host, s.Port), Connected: connected,
	}
}
