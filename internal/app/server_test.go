package app

import (
	"testing"
)

func TestServerUsesFriendlyLocalhostURL(t *testing.T) {
	url, err := friendlyLoopbackOrigin("127.0.0.1:47667")
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://localhost:47667" {
		t.Fatalf("friendly URL = %q", url)
	}
}
