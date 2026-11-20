package main

import (
	"net/url"
	"path"
	"strings"

	"floe/internal/core"

	"github.com/egoist/mygo/ui"
)

func desktopFileVisual(entry core.Entry, grid bool) (*ui.SVG, ui.Color) {
	icon, color := iconFile, "#52636c"
	switch {
	case entry.IsDir:
		icon, color = iconFolder, "#a96000"
		if grid {
			color = "#b76b00"
		}
	case desktopImageEntry(entry):
		icon, color = iconImage, "#14775d"
	default:
		switch strings.ToLower(path.Ext(entry.Name)) {
		case ".mp4", ".m4v", ".m3u8", ".m3u":
			icon, color = iconMovie, "#7653a6"
		case ".zip", ".gz", ".xz", ".7z", ".tar", ".rar":
			icon, color = iconArchive, "#9a5c22"
		case ".sh", ".ps1", ".py", ".go", ".rs", ".js", ".ts", ".c", ".cpp", ".h":
			icon, color = iconCode, "#1e648e"
		case ".txt", ".md", ".log", ".conf", ".ini", ".yaml", ".yml", ".json", ".xml", ".csv", ".css", ".html":
			color = "#496b7f"
		}
	}
	if entry.LinkBroken {
		color = "#b3261e"
	}
	return icon, ui.Hex(color)
}

func desktopImageEntry(entry core.Entry) bool {
	switch strings.ToLower(path.Ext(entry.Name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".avif":
		return !entry.IsDir
	}
	return false
}

func desktopLinkMarker(c *ui.Context, entry core.Entry, grid bool) {
	if !entry.IsLink {
		return
	}
	size, offset, font, radius := float32(11), float32(-4), float32(9), float32(2)
	if grid {
		size, offset, font, radius = 20, 5, 15, 4
	}
	color := ui.Hex("#1e648e")
	if entry.LinkBroken {
		color = ui.Hex("#b3261e")
	}
	ui.Column(c).Absolute().Right(offset).Bottom(offset).Size(size, size).Radius(radius).
		Background(c.Theme().Background).Center().Children(func() {
		ui.Text(c, "↗").FontSize(font).Bold().TextColor(color)
	})
}

type desktopThumbnail struct {
	source ui.ImageSource
}

func (w *desktopWorkspace) fileThumbnail(panel *desktopPanel, entry core.Entry) ui.ImageSource {
	if !desktopImageEntry(entry) || entry.LinkBroken {
		return nil
	}
	if state := panel.thumbnails[entry.Path]; state != nil {
		return state.source
	}
	if w.thumbnailJobs >= 4 {
		return nil
	}
	if panel.thumbnails == nil {
		panel.thumbnails = make(map[string]*desktopThumbnail)
	}
	if len(panel.thumbnails) >= 256 {
		for name := range panel.thumbnails {
			delete(panel.thumbnails, name)
			break
		}
	}
	state := &desktopThumbnail{}
	panel.thumbnails[entry.Path] = state
	w.thumbnailJobs++
	endpoint := "/api/v1/files/thumbnail"
	ext := strings.ToLower(path.Ext(entry.Name))
	switch ext {
	case ".svg", ".webp", ".avif", ".bmp":
		endpoint = "/api/v1/files/raw"
	}
	query := url.Values{"provider": {panel.location.provider}, "path": {entry.Path}, "size": {"160"}}.Encode()
	go func() {
		var source ui.ImageSource
		data, err := w.client.readBytes(w.ctx, endpoint+"?"+query, 12<<20)
		if err == nil {
			if ext == ".svg" {
				source, err = ui.ParseSVG(data)
			} else {
				source, err = ui.DecodeBitmap(data)
			}
		}
		w.window.Update(func() {
			w.thumbnailJobs--
			if err == nil {
				state.source = source
			}
		})
	}()
	return nil
}

func desktopEntryTitle(entry core.Entry) string {
	title := entry.Path
	if entry.LinkTarget != "" {
		title += " → " + entry.LinkTarget
	}
	switch {
	case entry.LinkBroken:
		return title + "（链接目标不存在）"
	case entry.LinkUnresolved:
		return title + "（链接，正在解析目标）"
	case entry.IsLink && entry.IsDir:
		return title + "（目录链接）"
	}
	return title
}

func (w *desktopWorkspace) fileDragSource(element ui.Element, panel *desktopPanel, side, row int, selected []core.Entry) {
	entry := panel.entries[row]
	chosen := panel.selected.Has(entry.Path)
	if !chosen {
		selected = []core.Entry{entry}
	}
	element.Drag(desktopDrag{side: side, location: panel.location, displayPath: panel.displayPath, entries: selected})
	if element.Dragging() {
		element.Opacity(.45)
		if !chosen {
			panel.row = row
			panel.selected.Clear()
			panel.selected.Add(entry.Path)
		}
	}
}
