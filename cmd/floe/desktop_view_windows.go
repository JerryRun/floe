package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"floe/internal/app"
	"floe/internal/core"

	"github.com/egoist/mygo/ui"
)

var fileColumns = []struct {
	title, id string
	width     float32
}{{"名称", "name", 0}, {"大小", "size", 90}, {"修改时间", "modified", 132}, {"权限", "", 86}}

var desktopTheme = func() *ui.Theme {
	theme := ui.LightTheme()
	theme.Background = ui.Hex("#ffffff")
	theme.Surface = ui.Hex("#f1f3f4")
	theme.SurfaceHover = ui.Hex("#edf5f6")
	theme.SurfacePressed = ui.Hex("#e4e7e9")
	theme.Border = ui.Hex("#cfd4d7")
	theme.Text = ui.Hex("#202529")
	theme.TextMuted = ui.Hex("#687178")
	theme.Accent = ui.Hex("#167783")
	theme.AccentHover = ui.Hex("#126a74")
	theme.AccentPressed = ui.Hex("#0e5760")
	theme.Selection = ui.Hex("#dceff1")
	theme.ScrollbarStyle = ui.ScrollbarStyle{
		TrackWidth: 12, ThumbWidth: 9, Radius: 5, Track: ui.Hex("#f7f7f7"),
		Thumb: ui.Hex("#8c8c8c"), Hover: ui.Hex("#787878"), Active: ui.Hex("#676767"),
	}
	theme.Focus = ui.Hex("#58aeb7")
	theme.Danger = ui.Hex("#bd4c40")
	theme.Radius, theme.FontSize = 0, 13
	theme.Font = "Segoe UI"
	return theme
}()

func (w *desktopWorkspace) view(c *ui.Context) {
	c.SetTheme(desktopTheme)
	if c.Shortcut(ui.Ctrl, ui.KeyK) {
		w.setQueueTab(queueMemory)
	}
	if w.workbenchPopupOpen() && c.Shortcut(0, ui.KeyEscape) {
		w.closeWorkbenchPopups()
	} else if w.queueExpanded && w.queueFilter == queueTools && c.Shortcut(0, ui.KeyEscape) {
		w.queueExpanded = false
	}

	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(desktopTheme.Background).Children(func() {
		if !w.queueExpanded || w.queueFilter != queueTools {
			w.sessionSidebar(c)
		}
		ui.Column(c).Width(0).Grow(1).FillHeight().MinHeight(0).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
			switch {
			case w.queueExpanded:
				w.queueView(c)
			case w.queueCollapsed:
				ui.Column(c).Grow(1).MinHeight(0).Children(func() { w.filePanes(c) })
				w.queueHeader(c)
			default:
				w.splitQueue(c)
			}
		})
		w.fileDialogView(c)
		w.sessionDialog(c)
		w.templateDialog(c)
		w.templateChooser(c)
		w.memoryDialog(c)
		w.transferConflictDialog(c)
		w.previewDialog(c)
	})
	w.showNotifications(c)
}

func (w *desktopWorkspace) splitQueue(c *ui.Context) {
	_, height := c.Size()
	minimum := float32(90)
	if w.queueFilter == queuePreview {
		minimum = 220
	}
	if w.queueTop == 0 || w.queueWindowH != height {
		w.queueWindowH = height
		w.queueTop = height - min(w.queueHeight, w.queueHeightLimit(height)) - 1
	}
	if ui.SplitVertical(c, &w.queueTop, func() { w.filePanes(c) }, func() { w.queueView(c) }).Fill().Changed() {
		w.queueHeight = min(w.queueHeightLimit(height), max(minimum, height-w.queueTop-1))
		w.queueTop = height - w.queueHeight - 1
	}
}

func (w *desktopWorkspace) queueHeightLimit(height float32) float32 {
	if w.queueFilter == queueTools {
		return max(0, height-160)
	}
	return height * .65
}

func (w *desktopWorkspace) filePanes(c *ui.Context) {
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		w.panelView(c, 0, "左侧", "上传")
		divider := ui.Column(c).Width(7).FillHeight().Background(ui.Hex("#e4e7e9")).
			BorderWidth(0, 1, 0, 1).BorderColor(c.Theme().Border)
		w.swapHandle(c, divider)
		w.panelView(c, 1, "右侧", "下载")
	})
}

func (w *desktopWorkspace) panelView(c *ui.Context, side int, title, copyLabel string) {
	panel, other := w.panel(side), w.panel(1-side)
	theme := c.Theme()
	block := ui.Column(c).Key(side).Width(0).Grow(1).FillHeight().MinWidth(0).MinHeight(0).AlignItems(ui.Stretch)
	if block.PressedInside() {
		w.activePane = side
	}

	if value, ok := ui.Drop[any](block); ok {
		w.dropOnPane(side, value)
	}
	block.Children(func() {
		w.tabBar(c, side, title)
		ui.Column(c).Key(panel).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			w.panelContents(c, panel, other, side, title, copyLabel)
		})
	})
	if value, ok := ui.DragOver[any](block); ok {
		if drag, ok := value.(desktopDrag); ok && drag.side != side && panel.ready && !panel.busy && !w.transferring {
			block.Children(func() {
				ui.Column(c).Absolute().Left(8).Right(8).Top(65).Bottom(26).Center().
					Background(ui.RGBA(220, 239, 241, .88)).Border(2, theme.Accent).BorderStyle(ui.BorderDashed).
					Children(func() { ui.Text(c, "释放以传输到这里").FontWeight(600).TextColor(ui.Hex("#105c65")) })
			})
		}
	}
}

func (w *desktopWorkspace) panelContents(c *ui.Context, panel, other *desktopPanel, side int, title, copyLabel string) {
	theme := c.Theme()
	ui.Row(c).Height(35).Padding(3, 5).Gap(3).AlignItems(ui.Center).Background(theme.Background).
		BorderWidth(0, 0, 1, 0).BorderColor(ui.Hex("#e5e8ea")).Children(func() {
		location := panel.location
		if desktopIconButton(c, iconUp, "上一级", panel.busy || !panel.ready).Clicked() {
			w.activePane = side
			w.openLocation(panel, desktopLocation{location.provider, path.Dir(location.path)})
		}
		if desktopIconButton(c, iconRefresh, "刷新", panel.busy).Clicked() {
			w.activePane = side
			w.openLocation(panel, location)
		}
		ui.Row(c).Height(27).Grow(1).MinWidth(55).AlignItems(ui.Stretch).
			Border(1, theme.Border).Background(theme.Background).Children(func() {
			input := ui.TextInputBase(c, &panel.address).Grow(1).MinWidth(0).Height(25).
				Label(title+"路径").Padding(0, 7).Font("Cascadia Code").FontSize(12).Disabled(panel.busy)
			if input.Focused() {
				w.activePane = side
			}
			if input.Submitted() {
				w.navigatePanel(panel, side)
			}
			if isLocalProvider(panel.location.provider) {
				arrow := desktopIconButton(c, iconDown, "选择本地目录", panel.busy).Size(24, 25).Radius(0).
					BorderWidth(0, 0, 0, 1).BorderColor(ui.Hex("#e5e8ea"))
				w.localTreeButton(c, arrow, side, false)
			}
		})
		ui.Row(c).Size(43, 27).AlignItems(ui.Center).Children(func() {
			key, entry := panel.bookmark()
			star, color := "☆", theme.TextMuted
			if w.isBookmarked(key, entry) {
				star, color = "★", ui.Hex("#a96000")
			}
			bookmark := ui.Button(c, "").Label("添加或删除当前路径书签").Size(25, 25).Padding(0).
				Background(ui.Transparent).BorderWidth(0, 1, 0, 0).BorderColor(ui.Hex("#e5e8ea")).
				Disabled(panel.busy || !panel.ready || w.bookmarkBusy)
			bookmark.Children(func() {
				ui.Text(c, star).Font("Segoe UI Symbol").FontSize(18).TextColor(color)
			})
			if bookmark.Clicked() {
				w.togglePanelBookmark(panel)
			}
			arrow := ui.Button(c, "").Label("打开书签列表").Tooltip("打开书签").Size(18, 25).Padding(0).
				Background(ui.Transparent).Border(0, ui.Transparent).Disabled(panel.busy || !panel.ready)
			arrow.Children(func() {
				ui.Icon(c, iconDown).FontSize(14).TextColor(theme.TextMuted)
			})
			if arrow.Clicked() {
				w.localTree.open = false
				if w.bookmarkOpen && w.bookmarkPanel == panel {
					w.bookmarkOpen = false
				} else {
					w.showBookmarks(panel)
				}
			}
			w.bookmarkView(c, arrow, panel, side)
		})
		canCopy := panel.ready && other.ready && !panel.busy && !other.busy && !w.transferring
		icon := iconUpload
		if side == 1 {
			icon = iconDownload
		}
		copyButton := ui.Button(c, "").Label(copyLabel).Size(62, 27).Padding(0, 3).Radius(2).Gap(3).Disabled(!canCopy).
			Border(1, ui.Hex("#abc8cc")).Background(ui.Hex("#edf7f8")).TextColor(ui.Hex("#105f68")).Children(func() {
			ui.Icon(c, icon).FontSize(16)
			ui.Text(c, copyLabel).FontSize(12)
		})
		if copyButton.Clicked() {
			if panel.selected.Len() == 0 {
				w.notify("请选择文件", true)
			} else {
				w.copyEntries(1-side, desktopDrag{
					side: side, location: panel.location, displayPath: panel.displayPath, entries: panel.selectedEntries(),
				})
			}
		}
		viewIcon, viewLabel := iconList, "切换网格视图"
		if w.gridView[side] {
			viewIcon, viewLabel = iconGrid, "切换列表视图"
		}
		if desktopIconButton(c, viewIcon, viewLabel, panel.busy).Clicked() {
			w.gridView[side] = !w.gridView[side]
		}
		w.terminalButton(c, panel)
	})

	if panel.error != "" {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(3, 10).Children(func() {
			ui.Text(c, panel.error).TextColor(theme.Danger).SingleLine().Tooltip(panel.error).Grow(1)
			if panel.requested.provider != "" && !isLocalProvider(panel.requested.provider) &&
				ui.Button(c, "重试连接").Disabled(panel.busy).Clicked() {
				w.openLocation(panel, panel.requested)
			}
		})
	}
	var table ui.Element
	var activated *core.Entry
	rowClick := -1
	var rowModifiers ui.Modifiers
	selected := panel.selectedEntries()
	if w.gridView[side] {
		panel.grid.Key = func(row int) any { return panel.entries[row].Path }
		panel.grid.Label = func(row int) string { return panel.entries[row].Name }
		panel.grid.Selected = &panel.row
		panel.grid.Selection = &panel.selected
		table = ui.GridView(c, &panel.grid, len(panel.entries), 146, 132, func(row int) {
			entry := panel.entries[row]
			icon, color := desktopFileVisual(entry, true)
			cell := ui.Column(c).Fill().Disabled(panel.busy).AlignItems(ui.Center).Padding(6, 4, 3)
			w.fileDragSource(cell, panel, side, row, selected)
			if clicks := cell.Clicks(); clicks > 0 {
				rowClick, rowModifiers = row, cell.ClickModifiers()
				if panel.fileClick(c.Now(), entry.Path, clicks, rowModifiers) {
					activated = &entry
				}
			}
			cell.Children(func() {
				thumbnail := w.fileThumbnail(panel, entry)
				if thumbnail != nil {
					box := ui.Column(c).FillWidth().Height(104).Center().Background(theme.Surface).
						Border(1, ui.Hex("#e2e5e7"))
					box.Children(func() {
						ui.Image(c, thumbnail).Fill().Fit(ui.Contain)
						desktopLinkMarker(c, entry, true)
					})
				} else {
					box := ui.Column(c).Size(92, 92).Margin(3, 0, 5).Center().Radius(8).
						Background(ui.Hex("#f4f7f8")).Border(1, ui.Hex("#d9e0e3"))
					box.Children(func() {
						ui.Icon(c, icon).FontSize(60).TextColor(color)
						desktopLinkMarker(c, entry, true)
					})
				}
				ui.Text(c, entry.Name).FontSize(12).MaxLines(2).TextAlign(ui.Center).Tooltip(entry.Name).TextColor(theme.Text)
			})
		}).Grow(1).MinHeight(0).Disabled(panel.busy)
	} else {
		panel.list.Key = func(row int) any { return panel.entries[row].Path }
		panel.list.Label = func(row int) string { return panel.entries[row].Name }
		panel.list.Selected = &panel.row
		panel.list.Selection = &panel.selected
		w.fileHeader(c, panel)
		table = ui.List(c, &panel.list, len(panel.entries), func(row int) {
			entry := panel.entries[row]
			bar := ui.Row(c).Height(29).FillWidth().Disabled(panel.busy).AlignItems(ui.Center).Border(1, ui.Transparent)
			w.fileDragSource(bar, panel, side, row, selected)
			if clicks := bar.Clicks(); clicks > 0 {
				rowClick, rowModifiers = row, bar.ClickModifiers()
				if panel.fileClick(c.Now(), entry.Path, clicks, rowModifiers) {
					activated = &entry
				}
			}
			if panel.selected.Has(entry.Path) {
				bar.Background(ui.Hex("#dceff1")).Border(1, ui.Hex("#afd1d5"))
			} else if bar.Hovered() {
				bar.Background(theme.SurfaceHover)
			}
			bar.Children(func() {
				icon, color := desktopFileVisual(entry, false)
				ui.Row(c).Width(0).Grow(1).MinWidth(0).Padding(0, 7).Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Size(18, 18).Center().Children(func() {
						ui.Icon(c, icon).FontSize(18).TextColor(color)
						desktopLinkMarker(c, entry, false)
					})
					ui.Text(c, entry.Name).SingleLine().Tooltip(desktopEntryTitle(entry)).TextColor(theme.Text)
				})
				size := ""
				if !entry.IsDir && !entry.LinkBroken && !entry.LinkUnresolved {
					size = desktopSize(entry.Size)
				}
				modified := ""
				if !entry.Modified.IsZero() {
					modified = desktopFileTime(entry.Modified)
				}
				for i, value := range []string{size, modified, entry.Mode} {
					ui.Text(c, value).Width(fileColumns[i+1].width).Padding(0, 7).Font("Cascadia Code").
						FontSize(11).TextColor(theme.TextMuted).SingleLine().Tooltip(value)
				}
			})
		}).Grow(1).MinHeight(0).Disabled(panel.busy)
	}
	if panel.busy || (panel.ready && len(panel.entries) == 0) {
		height := float32(29)
		message := "空目录"
		if panel.busy {
			message = "正在读取目录…"
		}
		table.Height(0).Grow(0)
		ui.Column(c).Grow(1).Padding(40, 12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, message).Height(height).TextColor(theme.TextMuted)
		})
	}
	table.PopoverMenu(func(menu *ui.Menu) { w.buildFileMenu(c, panel, side, menu) })
	if rowClick >= 0 && !panel.busy {
		panel.selectFileRow(rowClick, rowModifiers)
		table.Focus()
		w.activePane = side
	}
	if table.Changed() {
		w.activePane = side
	}
	if !panel.busy && activated != nil {
		w.activePane = side
		w.openFileEntry(panel, *activated)
	} else if !panel.busy && table.Submitted() && panel.row >= 0 && panel.row < len(panel.entries) {
		w.openFileEntry(panel, panel.entries[panel.row])
	}
	ui.Row(c).Height(23).AlignItems(ui.Center).Padding(0, 7).Background(theme.Surface).
		BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
		status := fmt.Sprintf("%d 项", len(panel.entries))
		if panel.busy {
			status = "正在读取目录…"
		}
		ui.Text(c, status).FontSize(11).TextColor(theme.TextMuted).SingleLine().Grow(1)
		if panel.selected.Len() > 0 {
			label := fmt.Sprintf("已选择 %d 项", panel.selected.Len())
			names := make([]string, len(selected))
			for index, entry := range selected {
				names[index] = entry.Name
			}
			if len(names) == 1 {
				label = names[0]
			}
			ui.Text(c, label).FontSize(11).TextColor(theme.TextMuted).SingleLine().Tooltip(strings.Join(names, "\n"))
		}
	})

}

// Keep the click pair on the file, independent of how MyGo routes selection
// events between the draggable content and its enclosing list or grid.
func (panel *desktopPanel) fileClick(now time.Time, filePath string, clicks int, modifiers ui.Modifiers) bool {
	if modifiers != 0 {
		panel.lastFileClick = time.Time{}
		return false
	}
	interval, _, _ := procGetDoubleClickTime.Call()
	if interval == 0 {
		interval = 500
	}
	if clicks > 1 || panel.lastClickedPath == filePath &&
		now.Sub(panel.lastFileClick) <= time.Duration(interval)*time.Millisecond {
		panel.lastFileClick = time.Time{}
		return true
	}
	panel.lastClickedPath, panel.lastFileClick = filePath, now
	return false
}

func (panel *desktopPanel) selectFileRow(row int, modifiers ui.Modifiers) {
	entry := panel.entries[row]
	if modifiers&ui.Shift != 0 && panel.row >= 0 && panel.row < len(panel.entries) {
		if modifiers&ui.Ctrl == 0 {
			panel.selected.Clear()
		}
		for i := min(panel.row, row); i <= max(panel.row, row); i++ {
			panel.selected.Add(panel.entries[i].Path)
		}
	} else if modifiers&ui.Ctrl != 0 {
		if panel.selected.Has(entry.Path) {
			panel.selected.Remove(entry.Path)
		} else {
			panel.selected.Add(entry.Path)
		}
	} else {
		panel.selected.Clear()
		panel.selected.Add(entry.Path)
	}
	panel.row = row
}

func (w *desktopWorkspace) fileHeader(c *ui.Context, panel *desktopPanel) {
	ui.Row(c).Height(27).FillWidth().Background(ui.Hex("#f7f8f9")).
		BorderWidth(0, 0, 1, 0).BorderColor(desktopTheme.Border).Children(func() {
		for _, col := range fileColumns {
			button := ui.Button(c, "").Label(col.title).Height(26).Padding(0, 7).
				Radius(0).Background(ui.Hex("#f7f8f9")).BorderWidth(0, 1, 0, 0).BorderColor(ui.Hex("#e5e8ea"))
			if col.width == 0 {
				button.Width(0).Grow(1).MinWidth(0)
			} else {
				button.Width(col.width).Shrink(0)
			}
			button.Children(func() {
				color := ui.Hex("#555e64")
				active := col.id != "" && panel.sort.Column == col.id
				if active {
					color = ui.Hex("#105f68")
				}
				ui.Text(c, col.title).TextColor(color).Grow(1)
				if active {
					icon := ui.Icon(c, iconUp).FontSize(14).TextColor(ui.Hex("#105f68"))
					if panel.sort.Descending {
						icon.Rotate(180)
					}
				}
			})
			if col.id != "" && button.Clicked() {
				panel.sort.Descending = panel.sort.Column == col.id && !panel.sort.Descending
				panel.sort.Column = col.id
				panel.sortEntries()
			}
		}
	})
}

func (w *desktopWorkspace) tabBar(c *ui.Context, side int, title string) {
	tabs := w.tabs[side]
	windowWidth, _ := c.Size()
	paneWidth := (windowWidth - 210 - 7) / 2
	tabWidth := min(150, max(75, (paneWidth-33)/float32(len(tabs))-1))
	ui.ScrollHorizontal(c).Height(31).FillWidth().Scrollbars(false).Background(desktopTheme.Surface).
		BorderWidth(0, 0, 1, 0).BorderColor(desktopTheme.Border).Children(func() {
		ui.Row(c).Height(30).Padding(3, 3, 0).Gap(1).AlignItems(ui.End).Children(func() {
			for index, panel := range tabs {
				label, icon, color := w.locationTab(panel)
				active := index == w.active[side]
				textColor := desktopTheme.Text
				row := ui.Row(c).Key(panel).Size(tabWidth, 27).Shrink(0).AlignItems(ui.Center).
					Padding(0, 7).Gap(5).BorderWidth(1, 1, 0, 1).BorderColor(ui.Transparent)
				if active {
					row.Background(desktopTheme.Background).BorderColor(desktopTheme.Border)
					textColor = ui.Hex("#105c65")
				} else if row.Hovered() {
					row.Background(ui.Hex("#e8ebed"))
				}
				row.Children(func() {
					button := ui.Button(c, "").Label(label).Tooltip(panel.address).Width(0).Grow(1).MinWidth(0).
						Height(26).Padding(0).Gap(5).Radius(0).Background(ui.Transparent).Border(0, ui.Transparent).
						AccessSelected(active).Drag(desktopTabDrag{side: side, panel: panel})
					button.PopoverMenu(func(menu *ui.Menu) {
						if menu.Item("关闭标签").Disabled(len(tabs) == 1 || side == 0 && panel.location.provider == "local").Chosen() {
							w.closeTabAt(side, index)
						}
						if menu.Item("关闭全部标签").Disabled(len(tabs) == 1 && panel.location.provider == "local").Chosen() {
							w.closeAllTabs(side)
						}
					})
					button.Children(func() {
						ui.Icon(c, icon).FontSize(16).TextColor(color)
						ui.Text(c, label).SingleLine().Grow(1).TextColor(textColor)
					})
					if button.Clicked() {
						w.activePane = side
						w.selectTab(side, index)
					}
					pinned := side == 0 && panel.location.provider == "local"
					if !pinned {
						close := ui.Button(c, "").Label("关闭 "+label).Size(15, 15).Padding(0).Radius(2).
							Background(ui.Transparent).Border(0, ui.Transparent)
						if close.Hovered() {
							close.Background(ui.Hex("#d6dadd"))
						}
						close.Children(func() {
							ui.Icon(c, iconClose).FontSize(14).TextColor(ui.Hex("#7a8287"))
						})
						if close.Clicked() {
							w.closeTabAt(side, index)
						}
					}
				})
			}
			add := desktopIconButton(c, iconAdd, title+"新建本地标签", false).Size(26, 25).Margin(0, 0, 1).Shrink(0)
			w.localTreeButton(c, add, side, true)
		})
	})
}

func (w *desktopWorkspace) locationTab(panel *desktopPanel) (string, *ui.SVG, ui.Color) {
	label, icon, color := panel.location.provider, iconCloud, ui.Hex("#899399")
	if isLocalProvider(panel.location.provider) {
		label, icon, color = "本地", iconComputer, ui.Hex("#386d74")
		if panel.location.provider != "local" && panel.displayPath != "" {
			label = filepath.Base(panel.displayPath)
		}
	}
	for _, provider := range w.providers {
		if provider.ID == panel.location.provider {
			label = provider.Name
			if provider.Connected {
				color = ui.Hex("#147481")
			}
			break
		}
	}
	return label, icon, color
}

var desktopTerminalItems = []struct {
	label, placement string
	icon             *ui.SVG
	rotate           bool
}{
	{"新标签页", "new-tab", iconAdd, false},
	{"当前标签右侧窗格", "split-right", iconList, false},
	{"当前标签下方窗格", "split-down", iconList, true},
	{"指定标签右侧窗格", "tab-right", iconList, false},
	{"指定标签下方窗格", "tab-down", iconList, true},
}

func (w *desktopWorkspace) terminalButton(c *ui.Context, panel *desktopPanel) {
	var sessionID string
	for _, provider := range w.providers {
		if provider.ID == panel.location.provider && provider.Kind == "sftp" {
			sessionID = provider.ID
			break
		}
	}
	button := ui.Button(c, "").Label("打开 SSH").Tooltip("在 Windows Terminal 中打开 SSH").
		Size(35, 27).Gap(0).Padding(0).Border(0, ui.Transparent).Background(ui.Transparent).
		Disabled(panel.busy || sessionID == "").Children(func() {
		ui.Icon(c, iconTerminal).FontSize(18)
		ui.Icon(c, iconDown).FontSize(13).TextColor(c.Theme().TextMuted)
	})
	if sessionID == "" {
		panel.terminalOpen = false
		return
	}
	if button.Clicked() {
		if !slices.ContainsFunc(w.providers, func(provider core.ProviderInfo) bool { return provider.ID == sessionID && provider.Connected }) {
			w.notify("请先在面板中连接一个 SFTP 会话", true)
			return
		}
		panel.terminalOpen = !panel.terminalOpen
		if w.terminalTab == "" {
			w.terminalTab = "1"
		}
	}
	ui.PopoverBase(c, button, &panel.terminalOpen, func(popup ui.Element) {
		popup.AttachTo(button, ui.AnchorBottomEnd, ui.AnchorTopEnd)
		styleDirectoryPopup(popup, 230)
		ui.Column(c).FillWidth().Children(func() {
			for index, item := range desktopTerminalItems {
				if index == 3 {
					ui.Box(c).FillWidth().Height(1).Margin(4, 0).Background(ui.Hex("#e5e8ea"))
					ui.Row(c).Height(32).Padding(0, 9).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "指定标签编号").Grow(1).FontSize(11).TextColor(c.Theme().TextMuted)
						ui.TextInput(c, &w.terminalTab).Label("指定标签编号").Size(58, 24).Padding(0, 5)
					})
				}
				option := ui.Button(c, "").Label(item.label).FillWidth().Height(30).Padding(0, 9).Gap(0).
					Background(ui.Transparent).Border(0, ui.Transparent)
				if option.Hovered() {
					option.Background(c.Theme().Selection)
				}
				option.Children(func() {
					icon := ui.Icon(c, item.icon).Width(25).FontSize(17).TextColor(ui.Hex("#346a73"))
					if item.rotate {
						icon.Rotate(90)
					}
					ui.Text(c, item.label).Grow(1)
				})
				if option.Clicked() {
					tab, err := strconv.Atoi(w.terminalTab)
					if w.terminalTab == "" {
						tab, err = 1, nil
					}
					if index >= 3 && (err != nil || tab < 1 || tab > 100) {
						w.notify("标签编号必须在 1–100 之间", true)
					} else {
						panel.terminalOpen = false
						w.openTerminal(sessionID, item.placement, tab)
					}
				}
			}
		})
	})
}

func (w *desktopWorkspace) buildFileMenu(c *ui.Context, panel *desktopPanel, side int, menu *ui.Menu) {
	entries := panel.selectedEntries()
	if len(entries) > 0 {
		entry := entries[0]
		if panel.row >= 0 && panel.row < len(panel.entries) && panel.selected.Has(panel.entries[panel.row].Path) {
			entry = panel.entries[panel.row]
		}
		verb, destination := "上传", "右侧"
		if side == 1 {
			verb, destination = "下载", "左侧"
		}
		label := verb + "到" + destination
		if len(entries) > 1 {
			label = fmt.Sprintf("%s选中的 %d 项", verb, len(entries))
		}
		other := w.panel(1 - side)
		if menu.Item(label).Disabled(w.transferring || other.busy || !other.ready).Chosen() {
			w.copyEntries(1-side, desktopDrag{side: side, location: panel.location, displayPath: panel.displayPath, entries: entries})
		}
		if menu.Item("打开").Chosen() {
			w.openFileEntry(panel, entry)
		}
		menu.Separator()
		if menu.Item("复制路径").Chosen() {
			c.WriteClipboard(panel.pathForClipboard(entry))
		}
		for _, provider := range w.providers {
			if provider.ID == panel.location.provider && (provider.Kind == "sftp" || provider.Kind == "ftp") {
				if menu.Item("复制 URL").Chosen() {
					address := url.URL{
						Scheme: provider.Kind, User: url.User(provider.User), Path: entry.Path,
						Host: net.JoinHostPort(strings.Trim(provider.Host, "[]"), strconv.Itoa(provider.Port)),
					}
					c.WriteClipboard(address.String())
				}
				break
			}
		}
		menu.Separator()
		tasks := make([]app.TransferTemplateTask, 0, len(entries))
		for _, selected := range entries {
			tasks = append(tasks, app.TransferTemplateTask{
				SourceProvider: panel.location.provider, SourcePath: selected.Path,
				TargetProvider: other.location.provider, TargetPath: path.Join(other.location.path, selected.Name),
				ConflictPolicy: core.ConflictOverwrite, Concurrency: 4, Verify: true, PreserveStructure: true,
			})
		}
		w.templateMenu(menu, tasks)
		menu.Separator()
		if len(entries) == 1 && menu.Item("重命名").Chosen() {
			w.showFileDialog(panel, renameFile, entry)
		}
		label = "删除"
		if len(entries) > 1 {
			label = fmt.Sprintf("删除选中的 %d 项", len(entries))
		}
		if menu.Item(label).Chosen() {
			w.deleteEntries(panel, entries)
		}
		menu.Separator()
	}
	if menu.Item("新增目录").Chosen() {
		w.showFileDialog(panel, createFolder, core.Entry{})
	}
	if menu.Item("新增文件").Chosen() {
		w.showFileDialog(panel, createFile, core.Entry{})
	}
}

func (w *desktopWorkspace) openFileEntry(panel *desktopPanel, entry core.Entry) {
	if entry.LinkUnresolved {
		w.resolveFileLink(panel, entry)
		return
	}
	if entry.LinkBroken {
		message := "链接目标不存在"
		if entry.LinkTarget != "" {
			message += "：" + entry.LinkTarget
		}
		w.notify(message, true)
		return
	}
	if entry.IsDir {
		w.openLocation(panel, desktopLocation{panel.location.provider, entry.Path})
	} else {
		w.openPreview(panel, entry)
	}
}

func (w *desktopWorkspace) resolveFileLink(panel *desktopPanel, entry core.Entry) {
	if panel.busy {
		return
	}
	panel.busy = true
	location := panel.location
	go func() {
		var result struct{ Links []core.Entry }
		err := w.client.request(w.ctx, http.MethodPost, "/api/v1/files/resolve-links",
			map[string]any{"provider": location.provider, "paths": []string{entry.Path}}, &result)
		w.window.Update(func() {
			panel.busy = false
			if panel.location != location {
				return
			}
			if err != nil {
				w.notify(err.Error(), true)
				return
			}
			if len(result.Links) == 0 {
				w.notify("无法解析链接目标", true)
				return
			}
			link := result.Links[0]
			entry.LinkUnresolved = false
			entry.LinkBroken, entry.LinkTarget, entry.IsDir, entry.Size = link.LinkBroken, link.LinkTarget, link.IsDir, link.Size
			if index := slices.IndexFunc(panel.entries, func(item core.Entry) bool { return item.Path == entry.Path }); index >= 0 {
				panel.entries[index] = entry
				panel.sortEntries()
			}
			w.openFileEntry(panel, entry)
		})
	}()
}

func (w *desktopWorkspace) navigatePanel(panel *desktopPanel, side int) {
	address := strings.TrimSpace(panel.address)
	if address == "" {
		return
	}
	if isLocalProvider(panel.location.provider) && filepath.IsAbs(address) {
		for _, provider := range w.providers {
			if provider.ID == panel.location.provider {
				if relative, ok := desktopRelativeFolder(provider.Location, address); ok {
					w.openLocation(panel, desktopLocation{provider.ID, relative})
					return
				}
				break
			}
		}
		w.createLocalTab(side, address, "/")
		return
	}
	if !strings.HasPrefix(address, "/") {
		address = path.Join(panel.location.path, address)
	}
	w.openLocation(panel, desktopLocation{panel.location.provider, path.Clean(address)})
}

const (
	queueActive = iota
	queueCompleted
	queueFailed
	queueLogs
	queuePreview
	queueMemory
	queueTools
)

func (w *desktopWorkspace) setQueueTab(tab int) {
	if w.queueFilter == queueTools || tab == queueTools {
		w.queueExpanded = false
	}
	w.queueFilter, w.queueCollapsed = tab, false
	w.queueTop = 0
	w.queueList = ui.ListState{}
	w.filterQueue()
	switch tab {
	case queueMemory:
		if w.queueHeight < 260 {
			w.queueHeight, w.queueTop = 300, 0
		}
		if !w.memory.loaded {
			w.searchMemory()
		}
	case queueTools:
		if w.queueHeight < 320 {
			w.queueHeight = 360
		}
	case queuePreview:
		w.previewPanel, w.previewOpen = true, false
		if w.queueHeight < 300 {
			w.queueHeight, w.queueTop = 340, 0
		}
	}
}

func desktopSectionTab(c *ui.Context, label string, active, top bool) ui.Element {
	theme := c.Theme()
	button := ui.Button(c, label).FontSize(12).Radius(0).Height(31).
		BorderWidth(0, 1, 1, 0).BorderColor(theme.Border).Background(ui.Transparent).
		TextColor(theme.TextMuted).AccessSelected(active)
	if active {
		button.Background(theme.Background).TextColor(theme.Text)
		if top {
			button.BorderWidth(2, 0, 0, 0)
		} else {
			button.BorderWidth(0, 0, 2, 0)
		}
		button.BorderColor(theme.Accent)
	}
	return button
}

func (w *desktopWorkspace) queueHeader(c *ui.Context) {
	theme := c.Theme()
	ui.Row(c).Height(32).FillWidth().AlignItems(ui.Center).Background(theme.Surface).
		BorderWidth(1, 0, 1, 0).BorderColor(theme.Border).Children(func() {
		var counts [5]int
		active := 0
		for _, item := range w.queue {
			counts[desktopTaskCategory(item)]++
			if desktopTaskRunning(item) {
				active++
			}
		}
		counts[queueLogs], counts[queuePreview] = len(w.activity), len(w.previews)
		for index, label := range []string{"队列", "成功", "失败", "日志", "预览", "速查", "工具箱"} {
			if index == queuePreview && len(w.previews) == 0 && w.queueFilter != queuePreview {
				continue
			}
			selected := index == w.queueFilter
			button := ui.Button(c, "").Label(label).MinWidth(76).Height(31).Shrink(0).
				Padding(0, 10).Gap(6).Radius(0).AlignItems(ui.Center).Justify(ui.Center).
				BorderWidth(0, 1, 0, 0).BorderColor(theme.Border).
				Background(ui.Transparent).AccessSelected(selected)
			color := theme.TextMuted
			if selected {
				button.Background(theme.Background).DrawOver(func(p *ui.Painter, r ui.Rect) {
					p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: 2}, theme.Accent, 0)
				})
				color = theme.Text
			} else if button.Hovered() {
				button.Background(ui.Hex("#e4e7e9"))
			}
			button.Children(func() {
				ui.Text(c, label).FontSize(12).TextColor(color).SingleLine().Shrink(0)
				if index < len(counts) && (index != queueLogs || counts[index] > 0) {
					ui.Text(c, strconv.Itoa(counts[index])).Font("Cascadia Code").FontSize(10).
						TextColor(theme.TextMuted).SingleLine().Shrink(0)
				}
			})
			if button.Clicked() {
				w.setQueueTab(index)
			}
		}
		status := ""
		switch {
		case w.queueFilter == queuePreview:
			status = "支持多文件预览"
		case w.queueFilter == queueTools:
			status = "本地计算 · 输入仅在本次窗口保留"
		case w.queueRetrying:
			status = "进度连接已中断，正在重连"
		case w.queueFilter == queueLogs && len(w.activity) > 0:
			status = fmt.Sprintf("最近 %d 条操作记录", len(w.activity))
		case w.queueFilter == queueLogs:
			status = "暂无日志"
		case w.queueFilter == queueMemory && w.memory.loaded:
			status = fmt.Sprintf("%d 个命中片段", len(w.memory.hits))
		case w.queueFilter < queueLogs && len(w.queue) > 0:
			status = fmt.Sprintf("%d 进行中 · %d 总计", active, len(w.queue))
		case w.queueFilter < queueLogs:
			status = "暂无任务"
		}
		ui.Text(c, status).Width(0).MinWidth(0).Padding(0, 0, 0, 10).FontSize(11).
			TextColor(theme.TextMuted).SingleLine().Grow(1)

		switch w.queueFilter {
		case queueCompleted, queueFailed:
			if queueClearButton(c, "清空").Disabled(w.queueAction || len(w.visibleQueue) == 0).Clicked() {
				status := "completed"
				if w.queueFilter == queueFailed {
					status = "failed"
				}
				w.deleteQueueData("/api/v1/transfers?status="+status, "清空当前分类的传输历史？")
			}
		case queueLogs:
			if queueClearButton(c, "清空日志").Disabled(w.queueAction || len(w.activity) == 0).Clicked() {
				w.deleteQueueData("/api/v1/logs", "清空操作日志？")
			}
		}
		toggle := ui.Button(c, "").Label("收起或展开任务区").Size(28, 31).Padding(0).
			Border(0, ui.Transparent).Background(ui.Transparent)
		if toggle.Hovered() {
			toggle.Background(ui.Hex("#e4e7e9"))
		}
		toggle.Children(func() {
			icon := ui.Icon(c, iconDown).FontSize(18).TextColor(theme.TextMuted)
			if w.queueCollapsed {
				icon.Rotate(-90)
			}
		})
		if toggle.Clicked() {
			if w.queueExpanded {
				w.queueExpanded = false
			} else {
				w.queueCollapsed = !w.queueCollapsed
			}
		}
	})
}

func (w *desktopWorkspace) queueView(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Fill().MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		w.queueHeader(c)
		switch w.queueFilter {
		case queueLogs:
			w.activityView(c)
			return
		case queuePreview:
			w.previewView(c)
			return
		case queueMemory:
			w.memoryView(c)
			return
		case queueTools:
			w.toolboxView(c)
			return
		}
		if len(w.visibleQueue) == 0 {
			message := []string{"队列中没有任务", "还没有成功任务", "还没有失败任务"}[w.queueFilter]
			ui.Text(c, message).Padding(18, 10).TextColor(theme.TextMuted)
			return
		}
		w.queueList.Key = func(row int) any { return w.visibleQueue[row].ID }
		w.queueList.Label = func(row int) string { return path.Base(w.visibleQueue[row].SourcePath) }
		width, _ := c.Size()
		ui.ScrollHorizontal(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.List(c, &w.queueList, len(w.visibleQueue), func(row int) {
				w.taskRow(c, w.visibleQueue[row])
			}).Width(max(835, width-desktopSidebarWidth)).FillHeight().MinHeight(0).KeepScrollbars(true)
		})
	})
}

func (w *desktopWorkspace) activityView(c *ui.Context) {
	if len(w.activity) == 0 {
		ui.Text(c, "还没有操作日志").Padding(18, 10).TextColor(c.Theme().TextMuted)
		return
	}
	ui.List(c, nil, len(w.activity), func(row int) {
		entry := w.activity[row]
		level, color := entry.Level, c.Theme().TextMuted
		switch entry.Level {
		case "info":
			level = "信息"
		case "warning":
			level, color = "警告", ui.Hex("#a96000")
		case "error":
			level, color = "错误", c.Theme().Danger
		}
		ui.Row(c).FillWidth().MinHeight(29).Padding(6, 8).Gap(8).AlignItems(ui.Start).Font("Cascadia Code").FontSize(11).
			BorderWidth(0, 0, 1, 0).BorderColor(ui.Hex("#e5e8ea")).Children(func() {
			ui.Text(c, entry.Time.Local().Format("06/01/02 15:04")).Width(135).TextColor(c.Theme().TextMuted)
			ui.Text(c, level).Width(54).TextColor(color)
			ui.Text(c, entry.Category).Width(76).TextColor(c.Theme().TextMuted)
			ui.Column(c).Width(0).Grow(1).Children(func() {
				ui.Text(c, entry.Message)
				if entry.Detail != "" {
					ui.Text(c, entry.Detail).FontSize(10).TextColor(c.Theme().TextMuted)
				}
			})
		})
	}).Grow(1).MinHeight(0)
}

func desktopSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	number := float64(size)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"} {
		number /= 1024
		if number < 1024 {
			return fmt.Sprintf("%.1f %s", number, unit)
		}
	}
	return fmt.Sprintf("%d B", size)
}

func desktopFileTime(value time.Time) string {
	// Windows builds of the web UI use the Windows locale for this column.
	// Format numeric fields without dropping the time when the column is narrow.
	return value.Local().Format("06/01/02 15:04")
}
