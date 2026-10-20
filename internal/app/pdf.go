package app

import (
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
)

// PDF.js requests byte ranges; stream them from the provider without buffering
// a whole remote document in the application process.
func (s *Server) pdfFile(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.provider(w, r.URL.Query().Get("provider"))
	if !ok {
		return
	}
	filePath := r.URL.Query().Get("path")
	if !strings.EqualFold(path.Ext(filePath), ".pdf") {
		writeError(w, http.StatusUnsupportedMediaType, "PDF_UNSUPPORTED", "请选择 PDF 文件", "")
		return
	}
	info, err := provider.Stat(filePath)
	if err != nil || info.IsDir {
		writeError(w, http.StatusBadRequest, "PDF_READ_FAILED", "无法读取 PDF 文件", errorDetail(err))
		return
	}
	reader, err := provider.OpenRead(filePath)
	if err != nil {
		writeError(w, http.StatusBadGateway, "PDF_READ_FAILED", "无法读取 PDF 文件", err.Error())
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": path.Base(filePath)}))
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, path.Base(filePath), info.Modified, io.NewSectionReader(reader, 0, info.Size))
}
