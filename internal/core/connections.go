package core

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// IsConnectionError reports failures that mean a provider object can no
// longer be reused. The application removes stale providers so reopening a tab
// performs a fresh login instead of repeatedly calling a dead client.
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"connection lost", "connection reset", "connection closed", "connection aborted", "broken pipe",
		"use of closed network connection", "unexpected eof", "i/o timeout", "wsasend", "forcibly closed",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

type UnknownHostKeyError struct {
	Fingerprint string
}

func (e *UnknownHostKeyError) Error() string { return "host key has not been trusted" }

type HostKeyChangedError struct {
	Expected string
	Received string
}

func (e *HostKeyChangedError) Error() string {
	return fmt.Sprintf("host key changed: expected %s, received %s", e.Expected, e.Received)
}

type ConnectRequest struct {
	ID                   string `json:"id"`
	Protocol             string `json:"protocol"`
	Name                 string `json:"name"`
	Host                 string `json:"host"`
	Port                 int    `json:"port"`
	User                 string `json:"user"`
	AuthMethod           string `json:"auth_method"`
	Password             string `json:"password"`
	ClearPassword        bool   `json:"clear_password"`
	PrivateKey           string `json:"private_key"`
	Fingerprint          string `json:"fingerprint"`
	Group                string `json:"group"`
	SSHKeepAlive         bool   `json:"ssh_keep_alive"`
	ServerAliveInterval  int    `json:"server_alive_interval"`
	ServerAliveCountMax  int    `json:"server_alive_count_max"`
	TerminalAutoPassword *bool  `json:"terminal_auto_password,omitempty"`
}

const (
	DefaultServerAliveInterval = 60
	DefaultServerAliveCountMax = 3
)

type Manager struct {
	mu        sync.RWMutex
	providers map[string]FileSystem
	targets   map[string]ConnectionTarget
}

type ConnectionTarget struct {
	Host string
	Port int
	User string
}

func NewManager() *Manager {
	return &Manager{providers: make(map[string]FileSystem), targets: make(map[string]ConnectionTarget)}
}

func (m *Manager) Add(fs FileSystem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old := m.providers[fs.ID()]; old != nil {
		_ = old.Close()
	}
	m.providers[fs.ID()] = fs
}

func (m *Manager) Get(id string) (FileSystem, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	fs, ok := m.providers[id]
	return fs, ok
}

func (m *Manager) Remove(id string) bool {
	m.mu.Lock()
	provider, ok := m.providers[id]
	if ok {
		delete(m.providers, id)
		delete(m.targets, id)
	}
	m.mu.Unlock()
	if ok {
		_ = provider.Close()
	}
	return ok
}

func (m *Manager) List() []ProviderInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ProviderInfo, 0, len(m.providers))
	for _, fs := range m.providers {
		result = append(result, ProviderInfo{ID: fs.ID(), Name: fs.Name(), Kind: fs.Kind(), Group: fs.Group(), Location: fs.Location(), Connected: true})
	}
	return result
}

func (m *Manager) Connect(req ConnectRequest) (ProviderInfo, error) {
	switch req.Protocol {
	case "ftp":
		return m.connectFTP(req)
	case "", "sftp":
		return m.connectSFTP(req)
	default:
		return ProviderInfo{}, fmt.Errorf("unsupported protocol %q", req.Protocol)
	}
}

func (m *Manager) connectSFTP(req ConnectRequest) (ProviderInfo, error) {
	if req.Host == "" || req.User == "" {
		return ProviderInfo{}, errors.New("host and user are required")
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.Name == "" {
		req.Name = req.User + "@" + req.Host
	}
	if req.Group == "" {
		req.Group = "我的会话"
	}
	if req.ID == "" {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s@%s:%d", req.User, req.Host, req.Port)))
		req.ID = fmt.Sprintf("sftp-%x", digest[:8])
	}
	if req.SSHKeepAlive {
		if req.ServerAliveInterval == 0 {
			req.ServerAliveInterval = DefaultServerAliveInterval
		}
		if req.ServerAliveCountMax == 0 {
			req.ServerAliveCountMax = DefaultServerAliveCountMax
		}
		if req.ServerAliveInterval < 5 || req.ServerAliveInterval > 3600 {
			return ProviderInfo{}, errors.New("SSH keepalive interval must be between 5 and 3600 seconds")
		}
		if req.ServerAliveCountMax < 1 || req.ServerAliveCountMax > 20 {
			return ProviderInfo{}, errors.New("SSH keepalive failure count must be between 1 and 20")
		}
	}

	auth := make([]ssh.AuthMethod, 0, 2)
	authMethod := strings.ToLower(strings.TrimSpace(req.AuthMethod))
	if authMethod == "" {
		if req.PrivateKey != "" {
			authMethod = "key"
		} else {
			authMethod = "password"
		}
	}
	if authMethod == "password" && req.Password != "" {
		auth = append(auth, ssh.Password(req.Password))
	}
	if authMethod == "key" {
		if req.PrivateKey == "" {
			return ProviderInfo{}, errors.New("private key file is required")
		}
		key, err := os.ReadFile(req.PrivateKey)
		if err != nil {
			return ProviderInfo{}, fmt.Errorf("read private key: %w", err)
		}
		signer, err := parsePrivateKey(key, req.Password)
		if err != nil {
			return ProviderInfo{}, fmt.Errorf("parse private key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if authMethod != "password" && authMethod != "key" {
		return ProviderInfo{}, fmt.Errorf("unsupported SFTP authentication method %q", authMethod)
	}
	if len(auth) == 0 {
		return ProviderInfo{}, errors.New("password is required")
	}

	var seenFingerprint string
	config := &ssh.ClientConfig{
		User: req.User, Auth: auth, Timeout: 12 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seenFingerprint = ssh.FingerprintSHA256(key)
			if req.Fingerprint == "" {
				return &UnknownHostKeyError{Fingerprint: seenFingerprint}
			}
			if req.Fingerprint != seenFingerprint {
				return &HostKeyChangedError{Expected: req.Fingerprint, Received: seenFingerprint}
			}
			return nil
		},
	}
	address := net.JoinHostPort(req.Host, fmt.Sprintf("%d", req.Port))
	netConn, err := net.DialTimeout("tcp", address, config.Timeout)
	if err != nil {
		return ProviderInfo{}, err
	}
	conn, chans, reqs, err := ssh.NewClientConn(netConn, address, config)
	if err != nil {
		_ = netConn.Close()
		if req.Fingerprint == "" && seenFingerprint != "" {
			return ProviderInfo{}, &UnknownHostKeyError{Fingerprint: seenFingerprint}
		}
		if req.Fingerprint != "" && seenFingerprint != "" && req.Fingerprint != seenFingerprint {
			return ProviderInfo{}, &HostKeyChangedError{Expected: req.Fingerprint, Received: seenFingerprint}
		}
		return ProviderInfo{}, err
	}
	sshClient := ssh.NewClient(conn, chans, reqs)
	sftpClient, err := sftp.NewClient(sshClient, sftp.UseConcurrentReads(true), sftp.UseConcurrentWrites(true))
	if err != nil {
		_ = sshClient.Close()
		return ProviderInfo{}, err
	}
	home, err := sftpClient.Getwd()
	if err != nil || home == "" {
		home = "/"
	}
	home = cleanRemote(home)
	provider := NewSFTPFS(req.ID, req.Name, req.Group, home, sshClient, sftpClient, SSHKeepAliveConfig{
		Enabled:  req.SSHKeepAlive,
		Interval: time.Duration(req.ServerAliveInterval) * time.Second,
		CountMax: req.ServerAliveCountMax,
	})
	m.Add(provider)
	m.mu.Lock()
	m.targets[req.ID] = ConnectionTarget{Host: req.Host, Port: req.Port, User: req.User}
	m.mu.Unlock()
	return ProviderInfo{ID: req.ID, Name: req.Name, Kind: "sftp", Group: req.Group, Location: home, Connected: true}, nil
}

func parsePrivateKey(key []byte, passphrase string) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(key)
	if err == nil || passphrase == "" {
		return signer, err
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, err
	}
	return ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
}

func (m *Manager) connectFTP(req ConnectRequest) (ProviderInfo, error) {
	if req.Host == "" {
		return ProviderInfo{}, errors.New("host is required")
	}
	if req.Port == 0 {
		req.Port = 21
	}
	if req.User == "" {
		req.User = "anonymous"
	}
	if req.Password == "" && req.User == "anonymous" {
		req.Password = "anonymous@"
	}
	if req.Name == "" {
		req.Name = req.User + "@" + req.Host
	}
	if req.Group == "" {
		req.Group = "我的会话"
	}
	if req.ID == "" {
		digest := sha256.Sum256([]byte(fmt.Sprintf("ftp:%s@%s:%d", req.User, req.Host, req.Port)))
		req.ID = fmt.Sprintf("ftp-%x", digest[:8])
	}
	provider, err := NewFTPFS(req.ID, req.Name, req.Group, FTPConfig{
		Host: req.Host, Port: req.Port, User: req.User, Password: req.Password,
	})
	if err != nil {
		return ProviderInfo{}, err
	}
	m.Add(provider)
	return ProviderInfo{ID: req.ID, Name: req.Name, Kind: "ftp", Group: req.Group, Connected: true}, nil
}

func (m *Manager) Target(id string) (ConnectionTarget, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	target, ok := m.targets[id]
	return target, ok
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for _, fs := range m.providers {
		errs = append(errs, fs.Close())
	}
	return errors.Join(errs...)
}
