package core

import (
	"sort"
	"strings"
	"sync"
)

const (
	// linkResolveBudget caps how many symlinks one listing resolves inline.
	// Every resolution costs an extra protocol round trip, so a directory made
	// almost entirely of links (/etc/alternatives, /usr/lib) still answers
	// quickly: the leading entries are resolved for the rows the user sees and
	// the remainder is reported as unresolved for the browser to fill in.
	linkResolveBudget = 64
	// linkResolveWorkers bounds the concurrent stat calls spent on one listing.
	// Providers pass their control connection so browsing never eats a slot
	// reserved for transfers.
	linkResolveWorkers = 8
)

// resolveLinkEntries rewrites symlink entries so IsDir and Size describe the
// link target rather than the link itself. IsLink is left untouched, which is
// what lets the interface show a folder icon and a link marker at once.
//
// stat must follow symlinks; readLink may be nil when the provider cannot report
// the raw target text.
func resolveLinkEntries(entries []Entry, budget, workers int,
	stat func(string) (FileInfo, error),
	readLink func(string) (string, error)) {
	pending := make([]int, 0, len(entries))
	for index := range entries {
		if entries[index].IsLink {
			pending = append(pending, index)
		}
	}
	if len(pending) == 0 {
		return
	}
	if stat == nil {
		for _, index := range pending {
			entries[index].LinkUnresolved = true
		}
		return
	}
	if budget > 0 && len(pending) > budget {
		for _, index := range pending[budget:] {
			entries[index].LinkUnresolved = true
		}
		pending = pending[:budget]
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(pending) {
		workers = len(pending)
	}
	queue := make(chan int)
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			// Each worker owns a distinct index, so the shared slice is only
			// ever written element-wise and never needs a lock.
			for index := range queue {
				applyLinkTarget(&entries[index], stat, readLink)
			}
		}()
	}
	for _, index := range pending {
		queue <- index
	}
	close(queue)
	group.Wait()
}

func applyLinkTarget(entry *Entry, stat func(string) (FileInfo, error), readLink func(string) (string, error)) {
	if readLink != nil {
		if target, err := readLink(entry.Path); err == nil {
			entry.LinkTarget = target
		}
	}
	info, err := stat(entry.Path)
	if err != nil {
		// A dangling link is a normal state on a server, not a listing failure.
		entry.LinkBroken = true
		return
	}
	entry.IsDir = info.IsDir
	entry.LinkUnresolved = false
	if info.IsDir {
		return
	}
	// A symlink's own size is the length of its target text, which is useless
	// once the target is known.
	entry.Size = info.Size
}

// sortEntries keeps directories ahead of files and then orders names without
// regard to case. Providers call it after resolving links so a link pointing at
// a directory is grouped with the directories.
func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}
