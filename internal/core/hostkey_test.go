package core

import (
	"strings"
	"testing"
)

func TestHostKeyChangedErrorContainsBothFingerprints(t *testing.T) {
	err := (&HostKeyChangedError{Expected: "SHA256:old", Received: "SHA256:new"}).Error()
	if !strings.Contains(err, "SHA256:old") || !strings.Contains(err, "SHA256:new") {
		t.Fatalf("host key changed error = %q", err)
	}
}
