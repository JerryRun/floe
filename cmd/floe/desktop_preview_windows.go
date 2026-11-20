package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"floe/internal/core"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/video"
)

type desktopPreview struct {
	provider, path, name string
	kind                 string
	busy                 bool
	err                  string
	document             desktopTextDocument
	code                 *editor.State
	markdown             markdown.Options
	render               bool
	image                ui.ImageSource
	scale                float64
	video                *ui.VideoPlayer
	localVideoPath       string

	imageEntries  []core.Entry
	imageIndex    int
	imageViewSize ui.Rect

	syntaxMode string
	pdf        *desktopPDF

	window *mygo.Window
}

type desktopTextDocument struct {
	Content  string `json:"content"`
	ETag     string `json:"etag"`
	Encoding string `json:"encoding"`
	BOM      bool   `json:"bom"`
	Newline  string `json:"newline"`
}

func (w *desktopWorkspace) openPreview(panel *desktopPanel, entry core.Entry) {
	for i, item := range w.previews {
		if item.provider == panel.location.provider && item.path == entry.Path {
			w.selectPreview(i)
			return
		}
	}
	item := &desktopPreview{provider: panel.location.provider, path: entry.Path, name: entry.Name, scale: 1}
	switch strings.ToLower(path.Ext(entry.Name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".avif":
		item.kind = "image"
		for _, candidate := range panel.entries {
			if desktopImageEntry(candidate) && !candidate.LinkBroken && !candidate.LinkUnresolved {
				item.imageEntries = append(item.imageEntries, candidate)
			}
		}
		if !slices.ContainsFunc(item.imageEntries, func(candidate core.Entry) bool { return candidate.Path == entry.Path }) {
			item.imageEntries = append(item.imageEntries, entry)
		}
		item.imageIndex = slices.IndexFunc(item.imageEntries, func(candidate core.Entry) bool { return candidate.Path == entry.Path })
	case ".mp4", ".m4v", ".m3u8", ".m3u":
		item.kind = "video"
		if extension := strings.ToLower(path.Ext(entry.Name)); (extension == ".mp4" || extension == ".m4v") &&
			isLocalProvider(item.provider) && filepath.IsAbs(panel.displayPath) {
			item.localVideoPath = filepath.Join(panel.displayPath, entry.Name)
		}
	case ".pdf":
		item.kind = "pdf"
	case ".md", ".markdown":
		item.kind = strings.ToLower(path.Ext(entry.Name))
		item.render = true
	default:
		item.kind = "text"
	}
	w.previews = append(w.previews, item)
	w.selectPreview(len(w.previews) - 1)
	w.reloadPreview(item)
}

func (w *desktopWorkspace) selectPreview(index int) {
	if index != w.previewIndex && w.previewIndex >= 0 && w.previewIndex < len(w.previews) {
		current := w.previews[w.previewIndex]
		if current.video != nil && current.window == nil {
			current.video.Pause()
		}
	}
	w.previewIndex = index
	item := w.previews[index]
	w.previewReveal = item
	if item.window != nil {
		item.window.Focus()
		w.previewOpen = false
		return
	}
	w.previewOpen = !w.previewPanel || w.queueFilter != queuePreview || w.queueCollapsed
	if w.previewOpen {
		w.previewPanel = false
	}
}

func (w *desktopWorkspace) reloadPreview(item *desktopPreview) {
	if item.busy {
		return
	}
	if item.code != nil && item.code.Buffer.Modified() {
		go func() {
			choice, err := w.ask(mygo.MessageOptions{Type: mygo.MessageWarning, Message: "重新加载会丢弃未保存的修改。", Buttons: []string{"取消", "重新加载"}})
			if err == nil && choice == 1 {
				w.window.Update(func() { w.loadPreview(item) })
			}
		}()
		return
	}
	w.loadPreview(item)
}

func (w *desktopWorkspace) loadPreview(item *desktopPreview) {
	item.busy, item.err = true, ""
	query := url.Values{"provider": {item.provider}, "path": {item.path}}.Encode()
	if item.kind == "video" {
		w.window.Update(func() { w.loadVideoPreview(item, query) })
		return
	}
	if item.kind == "pdf" {
		w.loadPDF(item, query)
		return
	}
	name := item.name
	go func() {
		var document desktopTextDocument
		var image ui.ImageSource
		var err error
		switch item.kind {
		case "image":
			var data []byte
			data, err = w.client.readBytes(w.ctx, "/api/v1/files/raw?"+query, 40<<20)
			if err == nil {
				if strings.EqualFold(path.Ext(name), ".svg") {
					image, err = ui.ParseSVG(data)
				} else {
					image, err = ui.DecodeBitmap(data)
				}
			}
		default:
			err = w.client.request(w.ctx, http.MethodGet, "/api/v1/files/content?"+query, nil, &document)
		}
		if w.ctx.Err() != nil {
			return
		}
		w.window.Update(func() {
			if !slices.Contains(w.previews, item) {
				return
			}
			item.busy = false
			if err != nil {
				item.err = err.Error()
			} else {
				item.image = image
				if item.kind == "image" {
					item.imageViewSize = ui.Rect{}
				}
				if document.ETag != "" {
					item.document = document
					buffer := editor.NewBuffer(document.Content)
					buffer.SetLanguage(editor.LanguageFor(item.name))
					theme := editor.DefaultTheme(desktopTheme)
					theme.Background = ui.Hex("#fbfcfc")
					item.code = &editor.State{Buffer: buffer, Theme: theme, Font: ui.Font{Family: "Cascadia Code", Size: 13},
						LineHeight: 1.55, NoActiveLine: true, NoFolding: true, NoIndentGuides: true, KeepScrollbars: true}
					item.syntaxMode = "自动识别"
				}
			}
			if item.window != nil {
				item.window.Invalidate()
			}
		})
	}()
}

func (w *desktopWorkspace) loadVideoPreview(item *desktopPreview, query string) {
	if w.ctx.Err() != nil || !slices.Contains(w.previews, item) {
		return
	}
	// Media Foundation's COM player belongs to the same thread that draws and
	// controls it. Keep creation and disposal in the window's event loop.
	var player *ui.VideoPlayer
	var err error
	if item.localVideoPath != "" {
		player, err = video.Open(item.localVideoPath)
	} else {
		player, err = video.OpenURL(w.client.origin + "/api/v1/files/media?" + query)
	}
	item.busy = false
	if err != nil {
		item.err = err.Error()
		return
	}
	item.closeVideo()
	item.video = player
	if item.window != nil {
		item.window.Invalidate()
	}
}

func (w *desktopWorkspace) savePreview(item *desktopPreview) {
	if item.busy || item.code == nil || !item.code.Buffer.Modified() {
		return
	}
	var body = struct {
		Provider string `json:"provider"`
		Path     string `json:"path"`
		desktopTextDocument
	}{item.provider, item.path, item.document}
	body.Content = item.code.Buffer.String()
	item.busy, item.err = true, ""
	go func() {
		var document desktopTextDocument
		err := w.client.request(w.ctx, http.MethodPut, "/api/v1/files/content", body, &document)
		w.window.Update(func() {
			item.busy = false
			if err != nil {
				item.err = err.Error()
			} else {
				item.document = document
				item.code.Buffer.MarkSaved()
			}
			if item.window != nil {
				item.window.Invalidate()
			}
		})
	}()
}

func (w *desktopWorkspace) previewView(c *ui.Context) {
	if w.previewIndex < 0 || w.previewIndex >= len(w.previews) {
		ui.Text(c, "双击文件打开预览。").TextColor(c.Theme().TextMuted).Padding(8)
		return
	}
	item := w.previews[w.previewIndex]
	w.previewHeader(c, item, true)
	if item.window == nil && !w.previewOpen && w.previewIndex >= 0 && w.previews[w.previewIndex] == item {
		w.previewSurface(c, item)
	}
}

func (w *desktopWorkspace) detachPreview(item *desktopPreview) {
	if item.window != nil {
		item.window.Focus()
		return
	}
	item.window = mygo.NewWindow(mygo.WindowOptions{
		Title: "Floe · " + item.name, Width: 1020, Height: 700,
		Content: ui.View(func(c *ui.Context) {
			c.SetTheme(desktopTheme)
			ui.Column(c).Fill().AlignItems(ui.Stretch).Children(func() { w.previewSurface(c, item) })
		}),
	})
	item.window.OnClosed(func() {
		item.window = nil
		w.window.Invalidate()
	})
}

func (item *desktopPreview) closeResources() {
	if item.pdf != nil {
		item.pdf.cancel()
	}
	item.closeVideo()
}

func (item *desktopPreview) closeVideo() {
	if player := item.video; player != nil {
		item.video = nil
		player.Close()
	}
}

func (w *desktopWorkspace) closePreview(item *desktopPreview) {
	finish := func() {
		for index, current := range w.previews {
			if current != item {
				continue
			}
			if item.window != nil {
				item.window.Close()
			}
			item.closeResources()
			w.previews = slices.Delete(w.previews, index, index+1)
			switch {
			case len(w.previews) == 0:
				w.previewIndex, w.previewReveal = -1, nil
				w.previewOpen, w.previewPanel, w.previewMax = false, false, false
				if w.queueFilter == queuePreview {
					w.setQueueTab(queueActive)
				}
			case index == w.previewIndex:
				w.selectPreview(min(index, len(w.previews)-1))
			case index < w.previewIndex:
				w.previewIndex--
			}
			return
		}
	}
	if item.code == nil || !item.code.Buffer.Modified() {
		finish()
		return
	}
	go func() {
		choice, err := w.ask(mygo.MessageOptions{Type: mygo.MessageWarning, Message: "放弃未保存的修改？", Buttons: []string{"取消", "放弃修改"}})
		if err == nil && choice == 1 {
			w.window.Update(finish)
		}
	}()
}

func (c *desktopClient) readBytes(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err := desktopResponseError(response); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("文件超过预览上限 %d MiB", limit>>20)
	}
	return data, err
}
