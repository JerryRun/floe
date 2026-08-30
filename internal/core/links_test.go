package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLinkEntriesDescribesTargetType(t *testing.T) {
	entries := []Entry{
		{Name: "docs", Path: "/srv/docs", IsDir: true},
		{Name: "current", Path: "/srv/current", Size: 11, IsLink: true},
		{Name: "readme", Path: "/srv/readme", Size: 4, IsLink: true},
		{Name: "gone", Path: "/srv/gone", Size: 9, IsLink: true},
		{Name: "app.log", Path: "/srv/app.log", Size: 120},
	}
	stat := func(path string) (FileInfo, error) {
		switch path {
		case "/srv/current":
			return FileInfo{IsDir: true}, nil
		case "/srv/readme":
			return FileInfo{Size: 2048}, nil
		}
		return FileInfo{}, errors.New("no such file")
	}
	readLink := func(path string) (string, error) {
		if path == "/srv/gone" {
			return "../removed", nil
		}
		return "target-of-" + path, nil
	}
	resolveLinkEntries(entries, linkResolveBudget, linkResolveWorkers, stat, readLink)
	sortEntries(entries)

	byName := map[string]Entry{}
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	if link := byName["current"]; !link.IsDir || !link.IsLink || link.LinkTarget != "target-of-/srv/current" {
		t.Fatalf("link to directory = %#v", link)
	}
	if link := byName["readme"]; link.IsDir || link.Size != 2048 {
		t.Fatalf("link to file = %#v", link)
	}
	if link := byName["gone"]; !link.LinkBroken || link.IsDir || link.LinkTarget != "../removed" {
		t.Fatalf("broken link = %#v", link)
	}
	// A link to a directory must sort into the directory group, otherwise the
	// interface still shows it below the real folders.
	if entries[0].Name != "current" || entries[1].Name != "docs" {
		t.Fatalf("sorted order = %s, %s", entries[0].Name, entries[1].Name)
	}
}

func TestResolveLinkEntriesMarksEntriesBeyondBudget(t *testing.T) {
	entries := make([]Entry, 5)
	for index := range entries {
		entries[index] = Entry{Name: "link", Path: "/link", IsLink: true}
	}
	calls := 0
	stat := func(string) (FileInfo, error) {
		calls++
		return FileInfo{IsDir: true}, nil
	}
	resolveLinkEntries(entries, 2, 1, stat, nil)
	if calls != 2 {
		t.Fatalf("stat calls = %d, want 2", calls)
	}
	for index, entry := range entries {
		wantUnresolved := index >= 2
		if entry.LinkUnresolved != wantUnresolved {
			t.Fatalf("entry %d unresolved = %v, want %v", index, entry.LinkUnresolved, wantUnresolved)
		}
	}
}

func TestResolveLinkEntriesWithoutStatDefersEveryLink(t *testing.T) {
	entries := []Entry{{Name: "link", Path: "/link", IsLink: true}, {Name: "file", Path: "/file"}}
	resolveLinkEntries(entries, linkResolveBudget, linkResolveWorkers, nil, nil)
	if !entries[0].LinkUnresolved || entries[1].LinkUnresolved {
		t.Fatalf("deferred entries = %#v", entries)
	}
}

func TestLocalFSListReportsSymlinkedDirectoryAsDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "release-v3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("release-v3", filepath.Join(root, "current")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink("notes.txt", filepath.Join(root, "notes-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	fs, err := NewLocalFS("local", "本地", root)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.List("/")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Entry{}
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	link := byName["current"]
	if !link.IsDir || !link.IsLink || link.LinkTarget != "release-v3" || link.LinkBroken {
		t.Fatalf("symlinked directory = %#v", link)
	}
	if file := byName["notes-link"]; file.IsDir || !file.IsLink || file.Size != 5 {
		t.Fatalf("symlinked file = %#v", file)
	}
	if broken := byName["dangling"]; !broken.LinkBroken || broken.IsDir {
		t.Fatalf("dangling link = %#v", broken)
	}
	target, info, err := fs.ResolveLink("/current")
	if err != nil || !info.IsDir || target != "release-v3" {
		t.Fatalf("ResolveLink = %q, %#v, err = %v", target, info, err)
	}
}
