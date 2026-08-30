package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type LocalFS struct {
	id    string
	name  string
	root  string
	kind  string
	group string
}

type localWriteAt struct {
	*os.File
}

func (w *localWriteAt) Close() error {
	return errors.Join(w.File.Sync(), w.File.Close())
}

func NewLocalFS(id, name, root string) (*LocalFS, error) {
	return NewLocalFSWithKind(id, name, root, "demo", "演示")
}

func NewLocalFSWithKind(id, name, root, kind, group string) (*LocalFS, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &LocalFS{id: id, name: name, root: filepath.Clean(abs), kind: kind, group: group}, nil
}

func (l *LocalFS) ID() string       { return l.id }
func (l *LocalFS) Name() string     { return l.name }
func (l *LocalFS) Kind() string     { return l.kind }
func (l *LocalFS) Group() string    { return l.group }
func (l *LocalFS) Location() string { return l.root }
func (l *LocalFS) DisplayPath(path string) string {
	if l.kind != "local" {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash("/" + path)))
		if !strings.HasPrefix(clean, "/") {
			clean = "/" + clean
		}
		return clean
	}
	resolved, err := l.resolve(path)
	if err != nil {
		return path
	}
	return resolved
}
func (l *LocalFS) Close() error { return nil }

func (l *LocalFS) resolve(remotePath string) (string, error) {
	for _, component := range strings.Split(strings.ReplaceAll(remotePath, "\\", "/"), "/") {
		if component == ".." {
			return "", errors.New("path traversal is not allowed")
		}
	}
	clean := filepath.Clean(filepath.FromSlash("/" + remotePath))
	clean = strings.TrimPrefix(clean, string(filepath.Separator))
	full := filepath.Join(l.root, clean)
	rel, err := filepath.Rel(l.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes provider root")
	}
	return full, nil
}

func slashPath(parent, name string) string {
	parent = strings.TrimSuffix(parent, "/")
	if parent == "" || parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

func (l *LocalFS) List(path string) ([]Entry, error) {
	full, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	items, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			continue
		}
		entries = append(entries, Entry{
			Name: item.Name(), Path: slashPath(path, item.Name()), Size: info.Size(),
			Mode: info.Mode().String(), Modified: info.ModTime(), IsDir: item.IsDir(),
			IsLink: item.Type()&fs.ModeSymlink != 0,
		})
	}
	// os.ReadDir reports the link's own type, so resolve targets before sorting
	// to keep a symlinked directory grouped with the directories.
	resolveLinkEntries(entries, linkResolveBudget, linkResolveWorkers, l.Stat, l.readLink)
	sortEntries(entries)
	return entries, nil
}

func (l *LocalFS) readLink(path string) (string, error) {
	full, err := l.resolve(path)
	if err != nil {
		return "", err
	}
	return os.Readlink(full)
}

// ResolveLink reports one symlink's target, used for entries a large listing
// left unresolved.
func (l *LocalFS) ResolveLink(path string) (string, FileInfo, error) {
	target, _ := l.readLink(path)
	info, err := l.Stat(path)
	if err != nil {
		return target, FileInfo{}, err
	}
	return target, info, nil
}

func (l *LocalFS) Stat(path string) (FileInfo, error) {
	full, err := l.resolve(path)
	if err != nil {
		return FileInfo{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Size: info.Size(), Modified: info.ModTime(), IsDir: info.IsDir()}, nil
}

func (l *LocalFS) OpenRead(path string) (ReadAtCloser, error) {
	full, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.Open(full)
}

func (l *LocalFS) OpenWrite(path string, truncateTo *int64) (WriteAtCloser, error) {
	full, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if truncateTo != nil {
		if err := f.Truncate(*truncateTo); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return &localWriteAt{File: f}, nil
}

func (l *LocalFS) ReadFile(path string, limit int64) ([]byte, error) {
	info, err := l.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size > limit {
		return nil, fmt.Errorf("file is %d bytes; preview limit is %d", info.Size, limit)
	}
	full, _ := l.resolve(path)
	return os.ReadFile(full)
}

func (l *LocalFS) WriteFileAtomic(path string, data []byte) error {
	full, err := l.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp := full + ".remote-transfer-save"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, full); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (l *LocalFS) Mkdir(path string) error {
	full, err := l.resolve(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(full, 0o755)
}

func (l *LocalFS) Rename(oldPath, newPath string) error {
	oldFull, err := l.resolve(oldPath)
	if err != nil {
		return err
	}
	newFull, err := l.resolve(newPath)
	if err != nil {
		return err
	}
	return os.Rename(oldFull, newFull)
}

func (l *LocalFS) Replace(oldPath, newPath string) error {
	oldFull, err := l.resolve(oldPath)
	if err != nil {
		return err
	}
	newFull, err := l.resolve(newPath)
	if err != nil {
		return err
	}
	return replaceLocalFile(oldFull, newFull)
}

func (l *LocalFS) Remove(path string) error {
	full, err := l.resolve(path)
	if err != nil {
		return err
	}
	if full == l.root {
		return errors.New("cannot remove provider root")
	}
	return os.RemoveAll(full)
}
