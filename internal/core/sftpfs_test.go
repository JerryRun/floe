package core

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSSHKeepAliveDisconnectsAfterConsecutiveFailures(t *testing.T) {
	stop := make(chan struct{})
	disconnected := make(chan struct{}, 1)
	var sends atomic.Int32
	done := make(chan struct{})
	go func() {
		sshKeepAliveLoop(stop, time.Millisecond, 3, func() error {
			sends.Add(1)
			return errors.New("transport failed")
		}, func() { disconnected <- struct{}{} })
		close(done)
	}()
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("keepalive did not disconnect after consecutive failures")
	}
	<-done
	if got := sends.Load(); got != 3 {
		t.Fatalf("keepalive sends = %d, want 3", got)
	}
}

func TestSSHKeepAliveSuccessResetsFailureCount(t *testing.T) {
	stop := make(chan struct{})
	disconnected := make(chan struct{}, 1)
	var sends atomic.Int32
	done := make(chan struct{})
	go func() {
		sshKeepAliveLoop(stop, time.Millisecond, 2, func() error {
			switch sends.Add(1) {
			case 1, 3:
				return errors.New("temporary failure")
			case 4:
				return errors.New("second consecutive failure")
			default:
				return nil
			}
		}, func() { disconnected <- struct{}{} })
		close(done)
	}()
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		close(stop)
		t.Fatal("keepalive did not disconnect after reset and two later failures")
	}
	<-done
	if got := sends.Load(); got != 4 {
		t.Fatalf("keepalive sends = %d, want 4", got)
	}
}

func TestSFTPLocationUsesRemoteHome(t *testing.T) {
	provider := NewSFTPFS("id", "name", "group", "/home/floe", nil, nil, SSHKeepAliveConfig{})
	if got := provider.Location(); got != "/home/floe" {
		t.Fatalf("SFTP location = %q, want remote home", got)
	}
}
