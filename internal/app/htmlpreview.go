package app

import (
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	maxHTMLPreviewSize = 5 << 20
	htmlPreviewTTL     = 30 * time.Minute
)

var (
	htmlHeadPattern    = regexp.MustCompile(`(?i)<head(?:\s[^>]*)?>`)
	htmlRootURLPattern = regexp.MustCompile(`(?i)(\b(?:src|href|poster)\s*=\s*["'])/([^/][^"']*)`)
)

type htmlPreviewDocument struct {
	Provider string
	Path     string
	Content  string
	Updated  time.Time
}

func (s *Server) createHTMLPreview(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Provider string `json:"provider"`
		Path     string `json:"path"`
		Content  string `json:"content"`
		Token    string `json:"token"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if _, ok := s.provider(w, request.Provider); !ok {
		return
	}
	if len(request.Content) > maxHTMLPreviewSize {
		writeError(w, http.StatusRequestEntityTooLarge, "HTML_PREVIEW_TOO_LARGE", "HTML 预览内容不能超过 5 MiB", "")
		return
	}
	extension := strings.ToLower(path.Ext(request.Path))
	if extension != ".html" && extension != ".htm" {
		writeError(w, http.StatusBadRequest, "HTML_PREVIEW_UNSUPPORTED", "只能预览 HTML 文件", "")
		return
	}
	now := time.Now()
	s.htmlPreviewMu.Lock()
	for token, preview := range s.htmlPreviews {
		if now.Sub(preview.Updated) > htmlPreviewTTL {
			delete(s.htmlPreviews, token)
		}
	}
	token := request.Token
	if _, exists := s.htmlPreviews[token]; token == "" || !exists {
		token = randomToken(18)
	}
	s.htmlPreviews[token] = htmlPreviewDocument{
		Provider: request.Provider, Path: request.Path, Content: request.Content, Updated: now,
	}
	s.htmlPreviewMu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]string{
		"token": token,
		"url":   "/api/v1/files/html-preview/" + token,
	})
}

func (s *Server) serveHTMLPreview(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/api/v1/files/html-preview/")
	preview, ok := s.htmlPreview(token)
	if !ok {
		writeError(w, http.StatusNotFound, "HTML_PREVIEW_EXPIRED", "HTML 预览已过期，请刷新", "")
		return
	}
	base := htmlResourceBase(token, path.Dir(preview.Path))
	content := injectHTMLPreviewBase(preview.Content, base, "/api/v1/files/html-resource/"+token+"/")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", htmlPreviewCSP)
	_, _ = io.WriteString(w, content)
}

func (s *Server) deleteHTMLPreview(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/api/v1/files/html-preview/")
	s.htmlPreviewMu.Lock()
	delete(s.htmlPreviews, token)
	s.htmlPreviewMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveHTMLPreviewResource(w http.ResponseWriter, r *http.Request) {
	value := strings.TrimPrefix(r.URL.Path, "/api/v1/files/html-resource/")
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "HTML_RESOURCE_INVALID", "HTML 资源路径无效", "")
		return
	}
	preview, ok := s.htmlPreview(parts[0])
	if !ok {
		writeError(w, http.StatusNotFound, "HTML_PREVIEW_EXPIRED", "HTML 预览已过期，请刷新", "")
		return
	}
	provider, ok := s.provider(w, preview.Provider)
	if !ok {
		return
	}
	resourcePath := "/" + strings.TrimPrefix(path.Clean("/"+parts[1]), "/")
	info, err := provider.Stat(resourcePath)
	if err != nil || info.IsDir {
		writeError(w, http.StatusNotFound, "HTML_RESOURCE_NOT_FOUND", "HTML 关联资源不存在", errorDetail(err))
		return
	}
	reader, err := provider.OpenRead(resourcePath)
	if err != nil {
		writeError(w, http.StatusBadGateway, "HTML_RESOURCE_READ_FAILED", "无法读取 HTML 关联资源", err.Error())
		return
	}
	defer reader.Close()
	contentType := previewContentType(resourcePath)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	if strings.HasPrefix(contentType, "text/html") {
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", htmlPreviewCSP)
	}
	http.ServeContent(w, r, path.Base(resourcePath), info.Modified, io.NewSectionReader(reader, 0, info.Size))
}

func (s *Server) htmlPreview(token string) (htmlPreviewDocument, bool) {
	s.htmlPreviewMu.Lock()
	defer s.htmlPreviewMu.Unlock()
	preview, ok := s.htmlPreviews[token]
	if !ok || time.Since(preview.Updated) > htmlPreviewTTL {
		delete(s.htmlPreviews, token)
		return htmlPreviewDocument{}, false
	}
	return preview, true
}

func htmlResourceBase(token, directory string) string {
	directory = strings.Trim(path.Clean("/"+directory), "/")
	segments := []string{"", "api", "v1", "files", "html-resource", url.PathEscape(token)}
	if directory != "" && directory != "." {
		for _, segment := range strings.Split(directory, "/") {
			segments = append(segments, url.PathEscape(segment))
		}
	}
	return strings.Join(segments, "/") + "/"
}

func injectHTMLPreviewBase(content, base, root string) string {
	content = htmlRootURLPattern.ReplaceAllString(content, `$1`+root+`$2`)
	baseTag := `<base href="` + html.EscapeString(base) + `">`
	if location := htmlHeadPattern.FindStringIndex(content); location != nil {
		content = content[:location[1]] + baseTag + content[location[1]:]
	} else {
		content = baseTag + content
	}
	return content
}

func previewContentType(filePath string) string {
	extension := strings.ToLower(path.Ext(filePath))
	if value := mime.TypeByExtension(extension); value != "" {
		return value
	}
	switch extension {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}

const htmlPreviewCSP = "default-src 'self' data: blob: http: https:; " +
	"script-src 'self' 'unsafe-inline' 'unsafe-eval' http: https:; " +
	"style-src 'self' 'unsafe-inline' http: https:; img-src 'self' data: blob: http: https:; " +
	"font-src 'self' data: http: https:; media-src 'self' data: blob: http: https:; " +
	"connect-src 'self' data: blob: http: https: ws: wss:; frame-src http: https:; " +
	"frame-ancestors 'self'; base-uri 'self'; form-action 'none'"
