package main

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"floe/internal/app"
	"floe/internal/core"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type desktopFileAction string

const (
	createFolder desktopFileAction = "新建目录"
	createFile   desktopFileAction = "新建文件"
	renameFile   desktopFileAction = "重命名"
)

type desktopFileDialog struct {
	open   bool
	saving bool
	action desktopFileAction
	panel  *desktopPanel
	entry  core.Entry
	name   string
	error  string
}

func (w *desktopWorkspace) showFileDialog(panel *desktopPanel, action desktopFileAction, entry core.Entry) {
	w.fileDialog = desktopFileDialog{open: true, action: action, panel: panel, entry: entry, name: entry.Name}
}

func (w *desktopWorkspace) fileDialogView(c *ui.Context) {
	dialog := &w.fileDialog
	if !dialog.open {
		return
	}
	title := "文件名称"
	switch dialog.action {
	case createFolder:
		title = "目录名称"
	case renameFile:
		title = "重命名文件"
		if dialog.entry.IsDir {
			title = "重命名目录"
		}
	}
	submitted := false
	desktopDialog(c, &dialog.open, title, 400, dialog.saving, func() {
		submitted = ui.TextInput(c, &dialog.name).Label(title).AutoFocus().FillWidth().Height(29).
			Background(c.Theme().Background).Disabled(dialog.saving).Submitted()
		if dialog.error != "" {
			ui.Text(c, dialog.error).TextColor(c.Theme().Danger)
		}
	}, func() {
		if desktopDialogButton(c, "取消", false, dialog.saving).Clicked() {
			dialog.open = false
		}
		confirm := desktopDialogButton(c, "确定", true, dialog.saving).Clicked()
		if dialog.open && !dialog.saving && (confirm || submitted) {
			w.submitFileDialog()
		}
	})
}

func (w *desktopWorkspace) submitFileDialog() {
	dialog := &w.fileDialog
	name := strings.TrimSpace(dialog.name)
	if strings.TrimSpace(name) == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		dialog.error = "请输入有效名称，名称中不能包含路径分隔符。"
		return
	}
	if dialog.action == renameFile && name == dialog.entry.Name {
		dialog.open = false
		return
	}

	panel := dialog.panel
	location := panel.location
	body := map[string]string{"provider": location.provider, "path": path.Join(location.path, name)}
	var endpoint string
	switch dialog.action {
	case createFolder:
		endpoint = "/api/v1/files/mkdir"
	case createFile:
		endpoint = "/api/v1/files/create"
	case renameFile:
		endpoint = "/api/v1/files/rename"
		body["new_path"], body["path"] = body["path"], dialog.entry.Path
	}

	dialog.saving, dialog.error = true, ""
	panel.busy = true
	go func() {
		err := w.fileRequest(location.provider, http.MethodPost, endpoint, body)
		w.window.Update(func() {
			dialog.saving, panel.busy = false, false
			if err != nil {
				dialog.error = err.Error()
				return
			}
			dialog.open = false
			if dialog.action == createFile {
				w.openPreview(panel, core.Entry{Name: name, Path: body["path"]})
			} else if dialog.action == renameFile {
				if panel.selected.Has(dialog.entry.Path) {
					panel.selected.Remove(dialog.entry.Path)
					panel.selected.Add(body["new_path"])
				}
				if dialog.entry.IsDir {
					w.rebaseRenamedTabs(location.provider, dialog.entry.Path, body["new_path"])
				}
			}
			w.refreshFileLocation(panel, location)
		})
	}()
}

func (w *desktopWorkspace) rebaseRenamedTabs(provider, previous, next string) {
	for side, tabs := range w.tabs {
		for _, tab := range tabs {
			location := tab.location
			if location.provider != provider || (location.path != previous && !strings.HasPrefix(location.path, previous+"/")) {
				continue
			}
			location.path = next + strings.TrimPrefix(location.path, previous)
			if w.panel(side) == tab {
				w.openLocation(tab, location)
			} else {
				tab.location = location
			}
		}
	}
}

func (w *desktopWorkspace) deleteEntries(panel *desktopPanel, entries []core.Entry) {
	location := panel.location
	panel.busy, panel.error = true, ""
	go func() {
		detail := entries[0].Name
		if len(entries) > 1 {
			detail = fmt.Sprintf("共 %d 项，包含 %s", len(entries), detail)
		}
		answer, err := w.ask(mygo.MessageOptions{
			Type: mygo.MessageWarning, Message: "永久删除选中的项目？",
			Detail:  detail + "\n目录及其内容会一起删除。",
			Buttons: []string{"取消", "删除"}, DestructiveButtons: []int{1},
		})
		removed := 0
		if err == nil && answer == 1 {
			for _, entry := range entries {
				err = w.fileRequest(location.provider, http.MethodDelete, "/api/v1/files",
					map[string]string{"provider": location.provider, "path": entry.Path})
				if err != nil {
					err = fmt.Errorf("已删除 %d 项 · %s · %w", removed, entry.Name, err)
					break
				}
				removed++
			}
		}
		if removed > 0 {
			display, files, refreshErr := w.loadLocation(location)
			w.finishLocation(panel, location, display, files, refreshErr)
		} else {
			w.window.Update(func() { panel.busy = false })
		}
		if err != nil {
			w.window.Update(func() { panel.error = err.Error() })
		}
	}()
}

func (w *desktopWorkspace) fileRequest(provider, method, endpoint string, body any) error {
	err := w.client.request(w.ctx, method, endpoint, body, nil)
	var failure *desktopAPIError
	// This response is emitted before any filesystem mutation.
	if !isLocalProvider(provider) && errors.As(err, &failure) && failure.Code == "PROVIDER_NOT_FOUND" {
		if err = w.connect(provider); err == nil {
			err = w.client.request(w.ctx, method, endpoint, body, nil)
		}
	}
	return err
}

func (w *desktopWorkspace) refreshFileLocation(panel *desktopPanel, location desktopLocation) {
	w.openLocation(panel, location)
	for side := range w.tabs {
		other := w.panel(side)
		if other != panel && other.location == location {
			w.openLocation(other, location)
		}
	}
}

func (panel *desktopPanel) selectedEntries() []core.Entry {
	var entries []core.Entry
	for _, entry := range panel.entries {
		if panel.selected.Has(entry.Path) {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (panel *desktopPanel) pathForClipboard(entry core.Entry) string {
	if isLocalProvider(panel.location.provider) && filepath.IsAbs(panel.displayPath) {
		return filepath.Join(panel.displayPath, entry.Name)
	}
	return entry.Path
}

func (panel *desktopPanel) bookmark() (string, app.PathBookmark) {
	location := panel.location
	value := strings.TrimSpace(panel.address)
	if value == "" {
		value = cmp.Or(panel.displayPath, location.path)
	}
	if isLocalProvider(location.provider) {
		return "local", app.PathBookmark{Path: value, Label: value}
	}
	normalized := value
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	return location.provider, app.PathBookmark{Path: normalized, Label: value}
}

func (w *desktopWorkspace) showBookmarks(panel *desktopPanel) {
	w.bookmarkPanel, w.bookmarkOpen = panel, true
	w.refreshBookmarks()
}

func (w *desktopWorkspace) refreshBookmarks() {
	if w.bookmarkBusy {
		return
	}
	w.bookmarkBusy, w.bookmarkError = true, ""
	go func() {
		var bookmarks map[string][]app.PathBookmark
		err := w.client.request(w.ctx, http.MethodGet, "/api/v1/bookmarks", nil, &bookmarks)
		w.window.Update(func() {
			w.bookmarkBusy = false
			if err != nil {
				w.bookmarkError = err.Error()
				return
			}
			w.bookmarkMaps = bookmarks
		})
	}()
}

func (w *desktopWorkspace) updateBookmark(key string, entry app.PathBookmark, remove bool) {
	w.bookmarkBusy, w.bookmarkError = true, ""
	go func() {
		var result []app.PathBookmark
		body := map[string]any{"key": key, "entry": entry, "remove": remove}
		err := w.client.request(w.ctx, http.MethodPatch, "/api/v1/bookmarks", body, &result)
		w.window.Update(func() {
			w.bookmarkBusy = false
			if err != nil {
				w.bookmarkError = err.Error()
				w.notify(w.bookmarkError, true)
				return
			}
			if w.bookmarkMaps == nil {
				w.bookmarkMaps = make(map[string][]app.PathBookmark)
			}
			w.bookmarkMaps[key] = result
			message := "已添加当前路径书签"
			if remove {
				message = "已删除当前路径书签"
			}
			w.notify(message, false)
		})
	}()
}

func (w *desktopWorkspace) togglePanelBookmark(panel *desktopPanel) {
	key, entry := panel.bookmark()
	w.bookmarkBusy = true
	go func() {
		var store map[string][]app.PathBookmark
		err := w.client.request(w.ctx, http.MethodGet, "/api/v1/bookmarks", nil, &store)
		w.window.Update(func() {
			if err != nil {
				w.bookmarkBusy = false
				panel.error = err.Error()
				return
			}
			w.bookmarkMaps = store
			w.updateBookmark(key, entry, w.isBookmarked(key, entry))
		})
	}()
}

func (w *desktopWorkspace) isBookmarked(key string, entry app.PathBookmark) bool {
	for _, bookmark := range w.bookmarkMaps[key] {
		if bookmark.Path == entry.Path ||
			(key == "local" && strings.EqualFold(filepath.Clean(bookmark.Path), filepath.Clean(entry.Path))) {
			return true
		}
	}
	return false
}

func (w *desktopWorkspace) bookmarkView(c *ui.Context, anchor ui.Element, panel *desktopPanel, side int) {
	if !w.bookmarkOpen || w.bookmarkPanel != panel {
		return
	}
	key, _ := panel.bookmark()
	bookmarks := w.bookmarkMaps[key]
	width := float32(330)
	for _, entry := range bookmarks {
		labelWidth, _ := c.MeasureText(0, ui.Span{Text: entry.Label, Font: "Cascadia Code", Size: 11})
		width = max(width, labelWidth+34)
	}
	windowWidth, windowHeight := c.Size()
	ui.PopoverBase(c, anchor, &w.bookmarkOpen, func(popup ui.Element) {
		popup.AttachTo(anchor, ui.AnchorBottomEnd, ui.AnchorTopEnd)
		styleDirectoryPopup(popup, min(width, windowWidth-12))
		if len(bookmarks) == 0 {
			message := "当前服务器还没有书签"
			switch {
			case w.bookmarkError != "":
				message = w.bookmarkError
			case w.bookmarkBusy:
				message = "正在更新书签…"
			}
			ui.Text(c, message).Padding(12, 10).TextColor(c.Theme().TextMuted)
			return
		}
		ui.Scroll(c).Height(min(float32(len(bookmarks)*30), min(410, windowHeight*.6-10))).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for _, entry := range bookmarks {
					button := ui.Button(c, "").Key(entry.Path).Label(entry.Label).Tooltip(entry.Label).
						FillWidth().Height(30).Padding(0, 10).Background(ui.Transparent).Border(0, ui.Transparent)
					if button.Hovered() {
						button.Background(c.Theme().Selection)
					}
					button.Children(func() {
						ui.Text(c, entry.Label).Font("Cascadia Code").FontSize(11).SingleLine().Grow(1)
					})
					if button.Clicked() {
						w.bookmarkOpen = false
						w.activePane = side
						if key == "local" {
							w.openLocalBookmark(side, entry.Path)
						} else {
							w.openLocation(panel, desktopLocation{panel.location.provider, entry.Path})
						}
					}
				}
			})
		})
	})
}
