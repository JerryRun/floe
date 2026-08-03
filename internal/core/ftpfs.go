package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"path"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	ftpclient "github.com/jlaffaye/ftp"
)

const (
	ftpDialTimeout    = 12 * time.Second
	ftpIOTimeout      = 20 * time.Second
	ftpKeepaliveEvery = 15 * time.Second
	ftpTransferSlots  = 3
)

type FTPConfig struct {
	Host     string
	Port     int
	User     string
	Password string
}

type FTPFS struct {
	id        string
	name      string
	group     string
	config    FTPConfig
	mu        sync.Mutex
	client    *ftpclient.ServerConn
	readSlots chan struct{}
	stop      chan struct{}
	closed    bool
	once      sync.Once
}

func NewFTPFS(id, name, group string, config FTPConfig) (*FTPFS, error) {
	client, err := dialFTP(config)
	if err != nil {
		return nil, err
	}
	provider := &FTPFS{
		id: id, name: name, group: group, config: config, client: client,
		readSlots: make(chan struct{}, ftpTransferSlots), stop: make(chan struct{}),
	}
	go provider.keepAlive()
	return provider, nil
}

func dialFTP(config FTPConfig) (*ftpclient.ServerConn, error) {
	address := fmt.Sprintf("%s:%d", config.Host, config.Port)
	dialer := &net.Dialer{Timeout: ftpDialTimeout, KeepAlive: ftpKeepaliveEvery}
	client, err := ftpclient.Dial(address, ftpclient.DialWithDialFunc(func(network, address string) (net.Conn, error) {
		connection, err := dialer.Dial(network, address)
		if err != nil {
			return nil, err
		}
		return &ftpTimeoutConn{Conn: connection}, nil
	}))
	if err != nil {
		return nil, err
	}
	if err := client.Login(config.User, config.Password); err != nil {
		_ = client.Quit()
		return nil, err
	}
	return client, nil
}

// ftpTimeoutConn applies an idle timeout to each individual socket read or
// write. Unlike ftp.DialWithShutTimeout it does not leave an expired absolute
// deadline on a control connection that is intended to be reused.
type ftpTimeoutConn struct{ net.Conn }

func (c *ftpTimeoutConn) Read(buffer []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(ftpIOTimeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(buffer)
}

func (c *ftpTimeoutConn) Write(buffer []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(ftpIOTimeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(buffer)
}

func (f *FTPFS) ID() string                           { return f.id }
func (f *FTPFS) Name() string                         { return f.name }
func (f *FTPFS) Kind() string                         { return "ftp" }
func (f *FTPFS) Group() string                        { return f.group }
func (f *FTPFS) Location() string                     { return fmt.Sprintf("%s:%d", f.config.Host, f.config.Port) }
func (f *FTPFS) DisplayPath(remotePath string) string { return cleanRemote(remotePath) }
func (f *FTPFS) MaxConcurrentWrites() int             { return 1 }
func (f *FTPFS) MaxConcurrentReads() int              { return ftpTransferSlots }

func (f *FTPFS) AcquireReadSlot(ctx context.Context) (func(), error) {
	select {
	case f.readSlots <- struct{}{}:
		return func() { <-f.readSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *FTPFS) List(remotePath string) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listLocked(remotePath)
}

func (f *FTPFS) listLocked(remotePath string) ([]Entry, error) {
	remotePath = cleanRemote(remotePath)
	var items []*ftpclient.Entry
	err := f.retryLocked(func(client *ftpclient.ServerConn) error {
		var err error
		items, err = client.List(remotePath)
		return err
	})
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		if item.Name == "." || item.Name == ".." {
			continue
		}
		entries = append(entries, Entry{
			Name: item.Name, Path: path.Join(remotePath, item.Name), Size: int64(item.Size),
			Mode: ftpMode(item.Type), Modified: item.Time,
			IsDir: item.Type == ftpclient.EntryTypeFolder, IsLink: item.Type == ftpclient.EntryTypeLink,
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

func ftpMode(kind ftpclient.EntryType) string {
	if kind == ftpclient.EntryTypeFolder {
		return "drwxr-xr-x"
	}
	if kind == ftpclient.EntryTypeLink {
		return "lrwxrwxrwx"
	}
	return "-rw-r--r--"
}

func (f *FTPFS) Stat(remotePath string) (FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	remotePath = cleanRemote(remotePath)
	if remotePath == "/" {
		return FileInfo{IsDir: true}, nil
	}
	entries, err := f.listLocked(path.Dir(remotePath))
	if err != nil {
		return FileInfo{}, err
	}
	name := path.Base(remotePath)
	for _, entry := range entries {
		if entry.Name == name {
			return FileInfo{Size: entry.Size, Modified: entry.Modified, IsDir: entry.IsDir}, nil
		}
	}
	return FileInfo{}, fmt.Errorf("remote path not found: %s", remotePath)
}

func (f *FTPFS) OpenRead(remotePath string) (ReadAtCloser, error) {
	client, err := dialFTP(f.config)
	if err != nil {
		return nil, err
	}
	return &ftpReaderAt{client: client, path: cleanRemote(remotePath), config: f.config}, nil
}

func (f *FTPFS) OpenWrite(remotePath string, truncateTo *int64) (WriteAtCloser, error) {
	remotePath = cleanRemote(remotePath)
	if truncateTo != nil {
		f.mu.Lock()
		err := f.retryLocked(func(client *ftpclient.ServerConn) error {
			return client.Stor(remotePath, bytes.NewReader(nil))
		})
		f.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	client, err := dialFTP(f.config)
	if err != nil {
		return nil, err
	}
	return &ftpWriterAt{client: client, path: remotePath, config: f.config}, nil
}

func (f *FTPFS) ReadFile(remotePath string, limit int64) ([]byte, error) {
	info, err := f.Stat(remotePath)
	if err != nil {
		return nil, err
	}
	if info.Size > limit {
		return nil, fmt.Errorf("file is %d bytes; preview limit is %d", info.Size, limit)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var data []byte
	err = f.retryLocked(func(client *ftpclient.ServerConn) error {
		response, err := client.Retr(cleanRemote(remotePath))
		if err != nil {
			return err
		}
		data, err = io.ReadAll(io.LimitReader(response, limit+1))
		return errors.Join(err, response.Close())
	})
	return data, err
}

func (f *FTPFS) WriteFileAtomic(remotePath string, data []byte) error {
	remotePath = cleanRemote(remotePath)
	temporary := remotePath + ".remote-transfer-save"
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.retryLocked(func(client *ftpclient.ServerConn) error {
		if err := client.Stor(temporary, bytes.NewReader(data)); err != nil {
			return err
		}
		_ = client.Delete(remotePath)
		if err := client.Rename(temporary, remotePath); err != nil {
			_ = client.Delete(temporary)
			return err
		}
		return nil
	})
}

func (f *FTPFS) Mkdir(remotePath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.retryLocked(func(client *ftpclient.ServerConn) error {
		current := ""
		for _, component := range strings.Split(strings.Trim(cleanRemote(remotePath), "/"), "/") {
			if component == "" {
				continue
			}
			current += "/" + component
			if err := client.MakeDir(current); err != nil {
				if _, listErr := client.List(current); listErr != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (f *FTPFS) Rename(oldPath, newPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.retryLocked(func(client *ftpclient.ServerConn) error {
		return client.Rename(cleanRemote(oldPath), cleanRemote(newPath))
	})
}

func (f *FTPFS) Remove(remotePath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	remotePath = cleanRemote(remotePath)
	entries, err := f.listLocked(path.Dir(remotePath))
	if err == nil {
		for _, entry := range entries {
			if entry.Name == path.Base(remotePath) && entry.IsDir {
				return f.retryLocked(func(client *ftpclient.ServerConn) error { return client.RemoveDirRecur(remotePath) })
			}
		}
	}
	return f.retryLocked(func(client *ftpclient.ServerConn) error { return client.Delete(remotePath) })
}

func (f *FTPFS) Close() error {
	f.once.Do(func() { close(f.stop) })
	f.mu.Lock()
	f.closed = true
	client := f.client
	f.client = nil
	f.mu.Unlock()
	if client == nil {
		return nil
	}
	return client.Quit()
}

func (f *FTPFS) keepAlive() {
	ticker := time.NewTicker(ftpKeepaliveEvery)
	defer ticker.Stop()
	for {
		select {
		case <-f.stop:
			return
		case <-ticker.C:
			f.mu.Lock()
			if !f.closed {
				_ = f.retryLocked(func(client *ftpclient.ServerConn) error { return client.NoOp() })
			}
			f.mu.Unlock()
		}
	}
}

func (f *FTPFS) retryLocked(operation func(*ftpclient.ServerConn) error) error {
	if f.closed {
		return net.ErrClosed
	}
	if err := f.ensureClientLocked(); err != nil {
		return err
	}
	firstErr := operation(f.client)
	if firstErr == nil || !isFTPConnectionError(firstErr) {
		return firstErr
	}
	f.invalidateClientLocked()
	if err := f.ensureClientLocked(); err != nil {
		return fmt.Errorf("FTP connection lost (%v); reconnect failed: %w", firstErr, err)
	}
	if err := operation(f.client); err != nil {
		return fmt.Errorf("FTP reconnected but operation failed: %w", err)
	}
	return nil
}

func (f *FTPFS) ensureClientLocked() error {
	if f.client != nil {
		return nil
	}
	client, err := dialFTP(f.config)
	if err != nil {
		return err
	}
	f.client = client
	return nil
}

func (f *FTPFS) invalidateClientLocked() {
	client := f.client
	f.client = nil
	if client != nil {
		go func() { _ = client.Quit() }()
	}
}

func isFTPConnectionError(err error) bool {
	if err == nil {
		return false
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return true
	}
	var protocolError *textproto.Error
	if errors.As(err, &protocolError) && (protocolError.Code == 421 || protocolError.Code == 425 || protocolError.Code == 426) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"i/o timeout", "broken pipe", "connection reset", "connection aborted", "connection closed",
		"closed by the remote host", "use of closed network connection", "unexpected eof", "421 ", "425 ", "426 ",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

type ftpReaderAt struct {
	mu       sync.Mutex
	client   *ftpclient.ServerConn
	response *ftpclient.Response
	path     string
	offset   int64
	config   FTPConfig
	closed   bool
}

func (r *ftpReaderAt) ReadAt(buffer []byte, offset int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.seekLocked(offset); err != nil {
		return 0, err
	}
	count, err := io.ReadFull(r.response, buffer)
	// An FTP data response can end exactly at a transfer block boundary. A
	// subsequent contiguous ReadAt then sees EOF on the exhausted response;
	// reopen it with REST at that boundary and read the next block.
	if count == 0 && errors.Is(err, io.EOF) {
		if seekErr := r.reopenLocked(offset); seekErr != nil {
			return 0, seekErr
		}
		count, err = io.ReadFull(r.response, buffer)
	}
	r.offset += int64(count)
	return count, err
}

func (r *ftpReaderAt) seekLocked(offset int64) error {
	if r.response != nil && r.offset == offset {
		return nil
	}
	return r.reopenLocked(offset)
}

func (r *ftpReaderAt) reopenLocked(offset int64) error {
	if r.response != nil {
		_ = r.response.Close()
	}
	response, err := r.client.RetrFrom(r.path, uint64(offset))
	if err != nil && isFTPConnectionError(err) {
		oldClient := r.client
		client, reconnectErr := dialFTP(r.config)
		if reconnectErr != nil {
			return fmt.Errorf("FTP download connection lost (%v); reconnect failed: %w", err, reconnectErr)
		}
		r.client = client
		go func() { _ = oldClient.Quit() }()
		response, err = r.client.RetrFrom(r.path, uint64(offset))
	}
	if err != nil {
		r.response = nil
		return err
	}
	r.response = response
	r.offset = offset
	return nil
}

func (r *ftpReaderAt) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var errs []error
	if r.response != nil {
		errs = append(errs, r.response.Close())
		r.response = nil
	}
	if r.client != nil {
		errs = append(errs, r.client.Quit())
		r.client = nil
	}
	return errors.Join(errs...)
}

type ftpWriterAt struct {
	mu     sync.Mutex
	client *ftpclient.ServerConn
	path   string
	pipe   *io.PipeWriter
	done   chan error
	next   int64
	config FTPConfig
}

func (w *ftpWriterAt) WriteAt(buffer []byte, offset int64) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pipe == nil || w.next != offset {
		if err := w.finishLocked(); err != nil {
			return 0, err
		}
		reader, writer := io.Pipe()
		w.pipe = writer
		w.done = make(chan error, 1)
		go func(start int64) {
			err := w.client.StorFrom(w.path, reader, uint64(start))
			_ = reader.CloseWithError(err)
			w.done <- err
		}(offset)
		w.next = offset
	}
	count, err := w.pipe.Write(buffer)
	w.next += int64(count)
	return count, err
}

func (w *ftpWriterAt) FlushBlock() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.finishLocked()
}

func (w *ftpWriterAt) finishLocked() error {
	if w.pipe == nil {
		return nil
	}
	closeErr := w.pipe.Close()
	transferErr := <-w.done
	w.pipe, w.done = nil, nil
	if isFTPConnectionError(transferErr) {
		oldClient := w.client
		if client, err := dialFTP(w.config); err == nil {
			w.client = client
			go func() { _ = oldClient.Quit() }()
		}
	}
	return errors.Join(closeErr, transferErr)
}

func (w *ftpWriterAt) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.finishLocked()
	if w.client != nil {
		err = errors.Join(err, w.client.Quit())
		w.client = nil
	}
	return err
}
