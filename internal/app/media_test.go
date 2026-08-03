package app

import (
	"strings"
	"testing"
)

func TestRewriteHLSPlaylistRoutesSegmentsAndKeysThroughProvider(t *testing.T) {
	input := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"keys/key.bin\"\n#EXTINF:4,\nsegment-01.ts\n#EXT-X-STREAM-INF:BANDWIDTH=800000\nlow/index.m3u8\n"
	result := rewriteHLSPlaylist(input, "session 1", "/video/master.m3u8")
	for _, want := range []string{
		"provider=session+1&path=%2Fvideo%2Fkeys%2Fkey.bin",
		"provider=session+1&path=%2Fvideo%2Fsegment-01.ts",
		"provider=session+1&path=%2Fvideo%2Flow%2Findex.m3u8",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("rewritten playlist missing %q:\n%s", want, result)
		}
	}
}

func TestRewriteHLSPlaylistKeepsExternalURI(t *testing.T) {
	input := "#EXTM3U\nhttps://cdn.example.test/segment.ts\n"
	if result := rewriteHLSPlaylist(input, "provider", "/master.m3u8"); !strings.Contains(result, "https://cdn.example.test/segment.ts") {
		t.Fatalf("external URI was changed: %s", result)
	}
}
