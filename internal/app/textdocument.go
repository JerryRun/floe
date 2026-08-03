package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"
)

const textPreviewLimit = int64(10 << 20)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

type textDocument struct {
	Content       string `json:"content"`
	ETag          string `json:"etag"`
	Encoding      string `json:"encoding"`
	BOM           bool   `json:"bom"`
	Newline       string `json:"newline"`
	MixedNewlines bool   `json:"mixed_newlines"`
	Size          int64  `json:"size"`
}

func decodeTextDocument(data []byte) (textDocument, error) {
	document := textDocument{Encoding: "utf-8", Size: int64(len(data))}
	payload := data
	if bytes.HasPrefix(payload, utf8BOM) {
		document.BOM = true
		payload = payload[len(utf8BOM):]
	}
	if !utf8.Valid(payload) {
		return textDocument{}, errors.New("该文件不是 UTF-8 文本")
	}
	document.Newline, document.MixedNewlines = detectNewlines(string(payload))
	document.Content = normalizeNewlines(string(payload))
	digest := sha256.Sum256(data)
	document.ETag = hex.EncodeToString(digest[:])
	return document, nil
}

func encodeTextDocument(content, encoding string, bom bool, newline string) ([]byte, error) {
	if encoding == "" {
		encoding = "utf-8"
	}
	if encoding != "utf-8" {
		return nil, errors.New("当前版本仅支持保存 UTF-8 文本")
	}
	content = normalizeNewlines(content)
	switch newline {
	case "", "lf", "none":
	case "crlf":
		content = strings.ReplaceAll(content, "\n", "\r\n")
	case "cr":
		content = strings.ReplaceAll(content, "\n", "\r")
	default:
		return nil, errors.New("不支持的换行格式")
	}
	data := []byte(content)
	if bom {
		data = append(append([]byte(nil), utf8BOM...), data...)
	}
	return data, nil
}

func detectNewlines(content string) (string, bool) {
	crlf, lf, cr := 0, 0, 0
	for index := 0; index < len(content); index++ {
		switch content[index] {
		case '\r':
			if index+1 < len(content) && content[index+1] == '\n' {
				crlf++
				index++
			} else {
				cr++
			}
		case '\n':
			lf++
		}
	}
	kinds := 0
	for _, count := range []int{crlf, lf, cr} {
		if count > 0 {
			kinds++
		}
	}
	newline := "none"
	if crlf >= lf && crlf >= cr && crlf > 0 {
		newline = "crlf"
	} else if lf >= cr && lf > 0 {
		newline = "lf"
	} else if cr > 0 {
		newline = "cr"
	}
	return newline, kinds > 1
}

func normalizeNewlines(content string) string {
	return strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
}
