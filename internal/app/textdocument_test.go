package app

import (
	"bytes"
	"testing"
)

func TestTextDocumentPreservesUTF8BOMAndCRLF(t *testing.T) {
	original := append(append([]byte(nil), utf8BOM...), []byte("第一行\r\nsecond\r\n")...)
	document, err := decodeTextDocument(original)
	if err != nil {
		t.Fatal(err)
	}
	if !document.BOM || document.Encoding != "utf-8" || document.Newline != "crlf" || document.MixedNewlines {
		t.Fatalf("document metadata = %#v", document)
	}
	if document.Content != "第一行\nsecond\n" {
		t.Fatalf("normalized content = %q", document.Content)
	}
	encoded, err := encodeTextDocument(document.Content, document.Encoding, document.BOM, document.Newline)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, original) {
		t.Fatalf("round trip changed bytes: %q", encoded)
	}
}

func TestTextDocumentDetectsMixedNewlinesAndDominantStyle(t *testing.T) {
	document, err := decodeTextDocument([]byte("one\r\ntwo\r\nthree\nfour\r"))
	if err != nil {
		t.Fatal(err)
	}
	if !document.MixedNewlines || document.Newline != "crlf" {
		t.Fatalf("mixed metadata = %#v", document)
	}
	if document.Content != "one\ntwo\nthree\nfour\n" {
		t.Fatalf("normalized mixed content = %q", document.Content)
	}
}

func TestTextDocumentRejectsInvalidUTF8(t *testing.T) {
	if _, err := decodeTextDocument([]byte{0xff, 0xfe, 0xfd}); err == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}
}
