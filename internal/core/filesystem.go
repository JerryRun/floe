package core

import (
	"context"
	"io"
	"time"
)

type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`
	Modified time.Time `json:"modified"`
	IsDir    bool      `json:"is_dir"`
	IsLink   bool      `json:"is_link"`
}

type FileInfo struct {
	Size     int64
	Modified time.Time
	IsDir    bool
}

type ReadAtCloser interface {
	io.ReaderAt
	io.Closer
}

type WriteAtCloser interface {
	io.WriterAt
	io.Closer
}

// ConcurrentWriteLimiter is implemented by providers whose protocol cannot
// safely perform independent random writes to one file. The transfer engine
// still verifies every block and may use concurrent reads when that provider
// is the source.
type ConcurrentWriteLimiter interface {
	MaxConcurrentWrites() int
}

// ConcurrentReadLimiter lets a provider reserve protocol connections for
// interactive browsing instead of allowing one transfer to consume every
// available control session.
type ConcurrentReadLimiter interface {
	MaxConcurrentReads() int
}

// ReadSlotController coordinates the provider's transfer connections across
// all tasks. Waiting is context-aware so pausing a queued task remains prompt.
type ReadSlotController interface {
	AcquireReadSlot(context.Context) (release func(), err error)
}

// WriteSlotController coordinates writes across tasks for providers whose
// servers do not reliably keep multiple SFTP write handles alive at once.
type WriteSlotController interface {
	AcquireWriteSlot(context.Context) (release func(), err error)
}

// RangeSHA256Verifier lets a remote provider hash data next to its storage so
// verified transfers do not need to download every target byte a second time.
// The transfer engine falls back to ReadAt verification when unavailable.
type RangeSHA256Verifier interface {
	SHA256Range(path string, offset, length int64) ([]byte, error)
}

type FileSystem interface {
	ID() string
	Name() string
	Kind() string
	Group() string
	Location() string
	DisplayPath(path string) string
	List(path string) ([]Entry, error)
	Stat(path string) (FileInfo, error)
	OpenRead(path string) (ReadAtCloser, error)
	OpenWrite(path string, truncateTo *int64) (WriteAtCloser, error)
	ReadFile(path string, limit int64) ([]byte, error)
	WriteFileAtomic(path string, data []byte) error
	Mkdir(path string) error
	Rename(oldPath, newPath string) error
	Remove(path string) error
	Close() error
}

type ProviderInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Group     string `json:"group"`
	Location  string `json:"location,omitempty"`
	Connected bool   `json:"connected"`
}
