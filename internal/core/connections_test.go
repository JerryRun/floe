package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestIsConnectionErrorRecognizesLostTransports(t *testing.T) {
	for _, err := range []error{io.EOF, errors.New("sftp: connection lost"), errors.New("wsasend: connection forcibly closed")} {
		if !IsConnectionError(err) {
			t.Fatalf("IsConnectionError(%q) = false", err)
		}
	}
	if IsConnectionError(errors.New("permission denied")) {
		t.Fatal("permission error was classified as a lost connection")
	}
}

func TestParsePrivateKeySupportsPassphrase(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(privateKey, "floe-test", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(block)
	if _, err := parsePrivateKey(data, "secret"); err != nil {
		t.Fatalf("parse encrypted private key: %v", err)
	}
	if _, err := parsePrivateKey(data, "wrong"); err == nil {
		t.Fatal("encrypted private key accepted the wrong passphrase")
	}
	if _, err := parsePrivateKey(data, ""); err == nil {
		t.Fatal("encrypted private key was accepted without a passphrase")
	}
}
