package main

import (
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"floe/internal/core"

	"github.com/egoist/mygo/ui"
)

type desktopDirectory struct {
	Name string `json:"name"`
	Path string `json:"path"`

	children []*desktopDirectory
	loading  bool
	loaded   bool
	expanded bool
	err      string
}

type desktopLocalTree struct {
	root   *desktopDirectory
	open   bool
	side   int
	tabBar bool
}

func styleDirectoryPopup(panel ui.Element, width float32) {
	panel.Width(width).Padding(4, 0).Margin(2, 0, 0).Background(desktopTheme.Background).
		Border(1, ui.Hex("#aeb5b9")).Shadow(0, 10, 32, 0, ui.RGBA(28, 37, 41, .24))
}

func (w *desktopWorkspace) localTreeButton(c *ui.Context, button ui.Element, side int, tabBar bool) {
	tree := &w.localTree
	sameAnchor := tree.side == side && tree.tabBar == tabBar
	if button.Clicked() {
		w.bookmarkOpen = false
		if sameAnchor && tree.open {
			tree.open = false
		} else {
			*tree = desktopLocalTree{root: &desktopDirectory{}, open: true, side: side, tabBar: tabBar}
			sameAnchor = true
			w.loadDirectoryChildren(tree.root)
		}
	}
	if !sameAnchor || !tree.open {
		return
	}
	_, windowHeight := c.Size()
	ui.PopoverBase(c, button, &tree.open, func(panel ui.Element) {
		styleDirectoryPopup(panel, 330)
		ui.Scroll(c).MaxHeight(min(470, windowHeight*.65-10)).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				switch {
				case tree.root.loading:
					ui.Text(c, "正在读取磁盘…").Padding(12, 10).TextColor(c.Theme().TextMuted)
				case tree.root.err != "":
					ui.Text(c, tree.root.err).Padding(12, 10).TextColor(c.Theme().Danger)
				default:
					for _, directory := range tree.root.children {
						w.localTreeRow(c, side, directory, 0)
					}
				}
			})
		})
	})
}

func (w *desktopWorkspace) localTreeRow(c *ui.Context, side int, dir *desktopDirectory, depth int) {
	row := ui.Row(c).Key(dir.Path).Height(28).FillWidth().Padding(0, 8, 0, float32(depth*16)).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(c.Theme().SurfaceHover)
	}
	row.Children(func() {
		arrow, label := iconNext, "展开 "
		if dir.expanded {
			arrow, label = iconDown, "折叠 "
		}
		if dir.loading {
			arrow, label = iconRefresh, "正在读取 "
		}
		if desktopIconButton(c, arrow, label+dir.Name, dir.loading).Size(22, 26).Clicked() {
			if dir.loaded {
				dir.expanded = !dir.expanded
			} else {
				w.loadDirectoryChildren(dir)
			}
		}
		folder := iconFolder
		if depth == 0 {
			folder = iconDrive
		}
		button := ui.Button(c, "").Label(dir.Name).Tooltip(dir.Path).Height(28).Width(0).Grow(1).MinWidth(0).
			Padding(0).Gap(0).Background(ui.Transparent).Border(0, ui.Transparent)
		button.Children(func() {
			ui.Icon(c, folder).Width(20).FontSize(18).TextColor(ui.Hex("#d99f2f"))
			ui.Text(c, dir.Name).SingleLine().Grow(1)
		})
		if button.Clicked() {
			w.localTree.open = false
			w.createLocalTab(side, dir.Path, "/")
		}
	})
	if dir.err != "" {
		ui.Text(c, dir.err).Padding(4, 8, 4, float32(depth*16+22)).TextColor(c.Theme().Danger)
	}
	if dir.expanded {
		for _, child := range dir.children {
			w.localTreeRow(c, side, child, depth+1)
		}
	}
}

func (w *desktopWorkspace) loadDirectoryChildren(dir *desktopDirectory) {
	dir.loading, dir.err = true, ""
	endpoint := "/api/v1/local/tree"
	if dir.Path != "" {
		endpoint += "?" + url.Values{"path": {dir.Path}}.Encode()
	}
	go func() {
		var children []*desktopDirectory
		err := w.client.request(w.ctx, http.MethodGet, endpoint, nil, &children)
		w.window.Update(func() {
			dir.loading = false
			if err != nil {
				dir.err = err.Error()
				return
			}
			dir.children, dir.loaded, dir.expanded = children, true, true
		})
	}()
}

func (w *desktopWorkspace) createLocalTab(side int, folder, initialPath string) {
	source := w.panel(side)
	go func() {
		var provider core.ProviderInfo
		err := w.client.request(w.ctx, http.MethodPost, "/api/v1/local/tabs", map[string]string{"path": folder}, &provider)
		w.window.Update(func() {
			if err != nil {
				source.error = err.Error()
				return
			}
			if !slices.ContainsFunc(w.providers, func(item core.ProviderInfo) bool { return item.ID == provider.ID }) {
				w.providers = append(w.providers, provider)
			}
			w.openProviderTab(side, desktopLocation{provider.ID, initialPath})
			w.refreshProviders()
		})
	}()
}

func (w *desktopWorkspace) openProviderTab(side int, location desktopLocation) {
	w.activePane = side
	for index, tab := range w.tabs[side] {
		if tab.location.provider == location.provider {
			w.active[side] = index
			w.openLocation(tab, location)
			return
		}
	}
	tab := newDesktopPanel(location)
	w.tabs[side] = append(w.tabs[side], tab)
	w.active[side] = len(w.tabs[side]) - 1
	w.openLocation(tab, location)
}

func (w *desktopWorkspace) openLocalBookmark(side int, folder string) {
	var best core.ProviderInfo
	var relative string
	for _, provider := range w.providers {
		if provider.Kind != "local" || len(best.Location) >= len(provider.Location) {
			continue
		}
		rel, ok := desktopRelativeFolder(provider.Location, folder)
		if ok && rel != "/" {
			best, relative = provider, rel
		}
	}
	if best.ID != "" {
		w.openProviderTab(side, desktopLocation{best.ID, relative})
		return
	}
	root := filepath.VolumeName(folder) + string(filepath.Separator)
	relative, _ = desktopRelativeFolder(root, folder)
	w.createLocalTab(side, root, relative)
}

func desktopRelativeFolder(root, folder string) (string, bool) {
	relative, err := filepath.Rel(root, folder)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return path.Join("/", filepath.ToSlash(relative)), true
}

type desktopTabDrag struct {
	side  int
	panel *desktopPanel
}

func (w *desktopWorkspace) dropOnPane(side int, value any) {
	switch drag := value.(type) {
	case desktopDrag:
		w.copyEntries(side, drag)
	case core.ProviderInfo:
		w.openSessionInPane(side, drag)
	case desktopTabDrag:
		source := w.tabs[drag.side]
		index := slices.Index(source, drag.panel)
		if index < 0 {
			return
		}
		if side == drag.side {
			w.activePane = side
			w.selectTab(side, index)
			return
		}
		w.openProviderTab(side, drag.panel.location)
		if len(source) <= 1 || drag.side == 0 && drag.panel.location.provider == "local" {
			return
		}
		w.tabs[drag.side] = slices.Delete(source, index, index+1)
		switch {
		case w.active[drag.side] == index:
			w.selectTab(drag.side, 0)
		case w.active[drag.side] > index:
			w.active[drag.side]--
		}
	}
}

func (w *desktopWorkspace) closeAllTabs(side int) {
	fallback := newDesktopPanel(desktopLocation{"local", "/"})
	for _, tab := range w.tabs[side] {
		if tab.location.provider == "local" {
			fallback = tab
			break
		}
	}
	w.tabs[side] = []*desktopPanel{fallback}
	w.selectTab(side, 0)
}

func (w *desktopWorkspace) swapHandle(c *ui.Context, divider ui.Element) {
	divider.Children(func() {
		button := desktopIconButton(c, iconSwap, "交换左右面板", false).Absolute().Left(-10).TopPercent(47).
			Size(25, 25).Radius(13).Background(c.Theme().Background).Border(1, c.Theme().Border)
		if !divider.Hovered() && !button.Hovered() && !button.FocusVisible() {
			button.Opacity(0)
		}
		if button.Clicked() {
			// Both panes must finish building before their list states move.
			w.window.Update(w.swapPanels)
		}
	})
}

func (w *desktopWorkspace) swapPanels() {
	w.tabs[0], w.tabs[1] = w.tabs[1], w.tabs[0]
	w.active[0], w.active[1] = w.active[1], w.active[0]
	w.gridView[0], w.gridView[1] = w.gridView[1], w.gridView[0]
	if !slices.ContainsFunc(w.tabs[0], func(tab *desktopPanel) bool { return tab.location.provider == "local" }) {
		w.tabs[0] = slices.Insert(w.tabs[0], 0, newDesktopPanel(desktopLocation{"local", "/"}))
		w.active[0]++
	}
	for side := range w.tabs {
		panel := w.panel(side)
		w.openLocation(panel, panel.location)
	}
}

func (w *desktopWorkspace) workbenchPopupOpen() bool {
	return w.localTree.open || w.bookmarkOpen || w.panel(0).terminalOpen || w.panel(1).terminalOpen
}

func (w *desktopWorkspace) closeWorkbenchPopups() {
	w.localTree.open, w.bookmarkOpen = false, false
	for _, tabs := range w.tabs {
		for _, panel := range tabs {
			panel.terminalOpen = false
		}
	}
}
