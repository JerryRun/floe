package core

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SFTPFS struct {
	id            string
	name          string
	group         string
	home          string
	ssh           *ssh.Client
	client        *sftp.Client
	dataOnce      sync.Once
	dataClient    *sftp.Client
	keepAliveStop chan struct{}
	keepAliveDone chan struct{}
	closeOnce     sync.Once
	closeErr      error
}

type SSHKeepAliveConfig struct {
	Enabled  bool
	Interval time.Duration
	CountMax int
}

func NewSFTPFS(id, name, group, home string, sshClient *ssh.Client, client *sftp.Client, keepAlive SSHKeepAliveConfig) *SFTPFS {
	fs := &SFTPFS{id: id, name: name, group: group, home: cleanRemote(home), ssh: sshClient, client: client}
	if keepAlive.Enabled && keepAlive.Interval > 0 && keepAlive.CountMax > 0 {
		fs.keepAliveStop = make(chan struct{})
		fs.keepAliveDone = make(chan struct{})
		go func() {
			defer close(fs.keepAliveDone)
			sshKeepAliveLoop(fs.keepAliveStop, keepAlive.Interval, keepAlive.CountMax,
				func() error {
					_, _, err := sshClient.SendRequest("keepalive@openssh.com", true, nil)
					return err
				},
				func() { _ = sshClient.Close() },
			)
		}()
	}
	return fs
}

func sshKeepAliveLoop(stop <-chan struct{}, interval time.Duration, countMax int, send func() error, disconnect func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if err := send(); err != nil {
				failures++
				if failures >= countMax {
					disconnect()
					return
				}
				continue
			}
			failures = 0
		}
	}
}

func (s *SFTPFS) ID() string                           { return s.id }
func (s *SFTPFS) Name() string                         { return s.name }
func (s *SFTPFS) Kind() string                         { return "sftp" }
func (s *SFTPFS) Group() string                        { return s.group }
func (s *SFTPFS) Location() string                     { return s.home }
func (s *SFTPFS) DisplayPath(remotePath string) string { return cleanRemote(remotePath) }

func cleanRemote(p string) string {
	if p == "" {
		return "/"
	}
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}

func (s *SFTPFS) List(remotePath string) ([]Entry, error) {
	remotePath = cleanRemote(remotePath)
	items, err := s.client.ReadDir(remotePath)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		entries = append(entries, Entry{
			Name: item.Name(), Path: path.Join(remotePath, item.Name()), Size: item.Size(),
			Mode: item.Mode().String(), Modified: item.ModTime(), IsDir: item.IsDir(),
			IsLink: item.Mode()&os.ModeSymlink != 0,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

func (s *SFTPFS) Stat(remotePath string) (FileInfo, error) {
	info, err := s.client.Stat(cleanRemote(remotePath))
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Size: info.Size(), Modified: info.ModTime(), IsDir: info.IsDir()}, nil
}

func (s *SFTPFS) OpenRead(remotePath string) (ReadAtCloser, error) {
	return s.transferClient().Open(cleanRemote(remotePath))
}

func (s *SFTPFS) OpenWrite(remotePath string, truncateTo *int64) (WriteAtCloser, error) {
	remotePath = cleanRemote(remotePath)
	f, err := s.transferClient().OpenFile(remotePath, os.O_RDWR|os.O_CREATE)
	if err != nil {
		return nil, err
	}
	if truncateTo != nil {
		if err := f.Truncate(*truncateTo); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return f, nil
}

// transferClient keeps bulk file traffic off the SFTP request channel used by
// directory listings, Stat and text previews. SSH still protects both channels
// on the same connection, while a large transfer can no longer queue ordinary
// UI requests behind its pipelined reads and writes.
func (s *SFTPFS) transferClient() *sftp.Client {
	s.dataOnce.Do(func() {
		if s.ssh == nil {
			return
		}
		client, err := sftp.NewClient(s.ssh, sftp.UseConcurrentReads(true), sftp.UseConcurrentWrites(true))
		if err == nil {
			s.dataClient = client
		}
	})
	if s.dataClient != nil {
		return s.dataClient
	}
	return s.client
}

// SHA256Range hashes a block on the SFTP host and returns only its digest over
// SSH. Servers without a shell, dd, head or sha256sum are handled by the
// transfer engine's normal SFTP read-back fallback.
func (s *SFTPFS) SHA256Range(remotePath string, offset, length int64) ([]byte, error) {
	const unit = int64(1 << 20)
	if offset < 0 || length < 0 || offset%unit != 0 {
		return nil, errors.New("range is not MiB-aligned")
	}
	session, err := s.ssh.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	blocks := (length + unit - 1) / unit
	command := fmt.Sprintf(
		"LC_ALL=C dd if=%s bs=%d skip=%d count=%d 2>/dev/null | head -c %d | sha256sum",
		shellQuote(cleanRemote(remotePath)), unit, offset/unit, blocks, length,
	)
	output, err := session.Output(command)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 || len(fields[0]) != sha256HexLength {
		return nil, fmt.Errorf("unexpected sha256sum output: %q", strings.TrimSpace(string(output)))
	}
	digest, err := hex.DecodeString(fields[0])
	if err != nil || len(digest) != sha256HexLength/2 {
		return nil, fmt.Errorf("invalid sha256sum digest: %q", fields[0])
	}
	return digest, nil
}

const sha256HexLength = 64

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (s *SFTPFS) ReadFile(remotePath string, limit int64) ([]byte, error) {
	info, err := s.Stat(remotePath)
	if err != nil {
		return nil, err
	}
	if info.Size > limit {
		return nil, fmt.Errorf("file is %d bytes; preview limit is %d", info.Size, limit)
	}
	f, err := s.client.Open(cleanRemote(remotePath))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit+1))
}

func (s *SFTPFS) WriteFileAtomic(remotePath string, data []byte) error {
	remotePath = cleanRemote(remotePath)
	tmp := remotePath + ".remote-transfer-save"
	f, err := s.client.Create(tmp)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		_ = s.client.Remove(tmp)
		return err
	}
	if err = f.Close(); err != nil {
		_ = s.client.Remove(tmp)
		return err
	}
	if err = s.client.PosixRename(tmp, remotePath); err == nil {
		return nil
	}
	_ = s.client.Remove(remotePath)
	if err = s.client.Rename(tmp, remotePath); err != nil {
		_ = s.client.Remove(tmp)
		return err
	}
	return nil
}

func (s *SFTPFS) Mkdir(remotePath string) error {
	return s.client.MkdirAll(cleanRemote(remotePath))
}

func (s *SFTPFS) Rename(oldPath, newPath string) error {
	oldPath, newPath = cleanRemote(oldPath), cleanRemote(newPath)
	if err := s.client.PosixRename(oldPath, newPath); err == nil {
		return nil
	}
	return s.client.Rename(oldPath, newPath)
}

func (s *SFTPFS) Remove(remotePath string) error {
	remotePath = cleanRemote(remotePath)
	info, err := s.client.Lstat(remotePath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return s.client.Remove(remotePath)
	}
	return s.removeDir(remotePath)
}

func (s *SFTPFS) removeDir(remotePath string) error {
	items, err := s.client.ReadDir(remotePath)
	if err != nil {
		return err
	}
	for _, item := range items {
		child := path.Join(remotePath, item.Name())
		if item.IsDir() {
			if err := s.removeDir(child); err != nil {
				return err
			}
		} else if err := s.client.Remove(child); err != nil {
			return err
		}
	}
	return s.client.RemoveDirectory(remotePath)
}

func (s *SFTPFS) Close() error {
	s.closeOnce.Do(func() {
		if s.keepAliveStop != nil {
			close(s.keepAliveStop)
		}
		var errs []error
		if s.dataClient != nil && s.dataClient != s.client {
			errs = append(errs, s.dataClient.Close())
		}
		if s.client != nil {
			errs = append(errs, s.client.Close())
		}
		// Closing SSH also releases a keepalive request blocked in transport,
		// so Close cannot leak its background goroutine.
		if s.ssh != nil {
			errs = append(errs, s.ssh.Close())
		}
		if s.keepAliveDone != nil {
			<-s.keepAliveDone
		}
		s.closeErr = errors.Join(errs...)
	})
	return s.closeErr
}
