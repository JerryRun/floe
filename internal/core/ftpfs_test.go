package core

import (
	"context"
	"errors"
	"net/textproto"
	"testing"
)

func TestIsFTPConnectionError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "timeout text", err: errors.New("write tcp: i/o timeout"), want: true},
		{name: "closed service", err: &textproto.Error{Code: 421, Msg: "control connection timed out"}, want: true},
		{name: "missing path", err: &textproto.Error{Code: 550, Msg: "file unavailable"}, want: false},
		{name: "permission", err: errors.New("permission denied"), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isFTPConnectionError(test.err); got != test.want {
				t.Fatalf("isFTPConnectionError(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestFTPReadSlotsReserveBrowsingConnectionAndHonorCancellation(t *testing.T) {
	provider := &FTPFS{readSlots: make(chan struct{}, ftpTransferSlots)}
	releases := make([]func(), 0, ftpTransferSlots)
	for range ftpTransferSlots {
		release, err := provider.AcquireReadSlot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.AcquireReadSlot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting slot error = %v, want context canceled", err)
	}
	for _, release := range releases {
		release()
	}
}
