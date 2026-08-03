package app

import (
	"fmt"
	"testing"

	"floe/internal/core"
)

func TestHostKeyConflictPayloads(t *testing.T) {
	changed, ok := hostKeyConflict(fmt.Errorf("handshake: %w", &core.HostKeyChangedError{
		Expected: "SHA256:old", Received: "SHA256:new",
	}))
	if !ok || changed["code"] != "HOST_KEY_CHANGED" || changed["expected"] != "SHA256:old" || changed["received"] != "SHA256:new" {
		t.Fatalf("changed host key payload = %#v, ok=%v", changed, ok)
	}
	unknown, ok := hostKeyConflict(&core.UnknownHostKeyError{Fingerprint: "SHA256:first"})
	if !ok || unknown["code"] != "HOST_KEY_UNKNOWN" || unknown["fingerprint"] != "SHA256:first" {
		t.Fatalf("unknown host key payload = %#v, ok=%v", unknown, ok)
	}
	if payload, ok := hostKeyConflict(fmt.Errorf("other failure")); ok || payload != nil {
		t.Fatalf("ordinary error became host key conflict: %#v", payload)
	}
}
