package app

import (
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const maxPlaylistSize = 8 << 20

var hlsURIAttribute = regexp.MustCompile(`URI="([^"]+)"`)

func (s *Server) mediaFile(w http.ResponseWriter, r *http.Request) {
	providerID := r.URL.Query().Get("provider")
	provider, ok := s.provider(w, providerID)
	if !ok {
		return
	}
	remotePath := r.URL.Query().Get("path")
	extension := strings.ToLower(path.Ext(remotePath))
	if extension == ".m3u8" || extension == ".m3u" {
		data, err := provider.ReadFile(remotePath, maxPlaylistSize)
		if err != nil {
			s.activity.Add("error", provider.Kind(), "读取播放列表失败", remotePath+" · "+err.Error())
			writeError(w, http.StatusBadGateway, "PLAYLIST_READ_FAILED", "无法读取 M3U8 播放列表", err.Error())
			return
		}
		content := rewriteHLSPlaylist(string(data), providerID, remotePath)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl; charset=utf-8")
		w.Header().Set("Cache-Control", "private, no-cache")
		_, _ = io.WriteString(w, content)
		return
	}

	info, err := provider.Stat(remotePath)
	if err != nil || info.IsDir {
		writeError(w, http.StatusBadRequest, "MEDIA_READ_FAILED", "无法读取媒体文件", errorDetail(err))
		return
	}
	reader, err := provider.OpenRead(remotePath)
	if err != nil {
		s.activity.Add("error", provider.Kind(), "打开媒体失败", remotePath+" · "+err.Error())
		writeError(w, http.StatusBadGateway, "MEDIA_READ_FAILED", "无法读取媒体文件", err.Error())
		return
	}
	defer reader.Close()
	contentType := mediaContentType(extension)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, path.Base(remotePath), info.Modified, io.NewSectionReader(reader, 0, info.Size))
}

func mediaContentType(extension string) string {
	switch extension {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".ts", ".mts":
		return "video/mp2t"
	case ".m4s":
		return "video/iso.segment"
	case ".aac":
		return "audio/aac"
	case ".mp3":
		return "audio/mpeg"
	case ".webm":
		return "video/webm"
	case ".vtt":
		return "text/vtt"
	case ".key":
		return "application/octet-stream"
	default:
		if detected := mime.TypeByExtension(extension); detected != "" {
			return detected
		}
		return "application/octet-stream"
	}
}

func rewriteHLSPlaylist(content, providerID, playlistPath string) string {
	base := path.Dir(playlistPath)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			lines[index] = hlsURIAttribute.ReplaceAllStringFunc(line, func(attribute string) string {
				matches := hlsURIAttribute.FindStringSubmatch(attribute)
				if len(matches) != 2 {
					return attribute
				}
				return `URI="` + mediaProxyURI(providerID, base, matches[1]) + `"`
			})
			continue
		}
		lines[index] = mediaProxyURI(providerID, base, trimmed)
	}
	return strings.Join(lines, "\n")
}

func mediaProxyURI(providerID, base, value string) string {
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "blob:") {
		return value
	}
	remotePath := value
	if !strings.HasPrefix(remotePath, "/") {
		remotePath = path.Join(base, remotePath)
	}
	return "/api/v1/files/media?provider=" + url.QueryEscape(providerID) + "&path=" + url.QueryEscape(remotePath)
}
