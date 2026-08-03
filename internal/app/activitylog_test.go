package app

import (
	"testing"
	"time"
)

func TestActivityLogPersistsAndNotifies(t *testing.T) {
	directory := t.TempDir()
	activity := newActivityLog(directory)
	updates, unsubscribe := activity.Subscribe()
	defer unsubscribe()
	activity.Add("error", "ftp", "连接失败", "connection refused")
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("activity subscriber was not notified")
	}
	reloaded := newActivityLog(directory)
	entries := reloaded.List(10)
	if len(entries) != 1 || entries[0].Detail != "connection refused" || entries[0].Category != "ftp" {
		t.Fatalf("reloaded entries = %#v", entries)
	}
	if removed := reloaded.Clear(); removed != 1 || len(reloaded.List(10)) != 0 {
		t.Fatalf("Clear removed %d entries; remaining %#v", removed, reloaded.List(10))
	}
	if afterClear := newActivityLog(directory).List(10); len(afterClear) != 0 {
		t.Fatalf("cleared entries reappeared after reload: %#v", afterClear)
	}
}
