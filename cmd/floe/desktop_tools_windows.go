package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"floe/internal/core"

	"github.com/egoist/mygo/ui"
)

func (w *desktopWorkspace) openTerminal(provider, placement string, tab int) {
	go func() {
		var result struct{ Askpass bool }
		err := w.client.request(w.ctx, http.MethodPost, "/api/v1/terminal/tabs", map[string]any{
			"kind": "ssh", "provider": provider, "placement": placement, "tab": tab,
		}, &result)
		w.window.Update(func() {
			if err != nil {
				w.notify(err.Error(), true)
			} else if result.Askpass {
				w.notify("已打开 SSH，正在自动输入密码", false)
			} else {
				w.notify("已打开 SSH", false)
			}
		})
	}()
}

const desktopSidebarWidth = 210

func (w *desktopWorkspace) sessionSidebar(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Width(desktopSidebarWidth).FillHeight().AlignItems(ui.Stretch).Background(theme.Surface).
		BorderWidth(0, 1, 0, 0).BorderColor(theme.Border).Children(func() {
		publish := "发布任务"
		if len(w.templates) > 0 {
			publish = fmt.Sprintf("发布任务 (%d)", len(w.templates))
		}
		ui.Row(c).Height(30).FillWidth().Background(ui.Hex("#f8f9fa")).Children(func() {
			for index, label := range []string{"服务器", publish} {
				if desktopSectionTab(c, label, w.sidebarTab == index, false).Width(0).Grow(1).Clicked() {
					w.sidebarTab = index
					if index == 1 {
						w.refreshTemplates()
					}
				}
			}
		})
		if w.sidebarTab == 1 {
			w.publishSidebar(c)
			return
		}
		ui.Row(c).Height(34).Padding(5, 7, 4).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Height(25).Grow(1).MinWidth(0).Background(theme.Background).Border(1, theme.Border).
				AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconSearch).Width(24).FontSize(17).TextColor(ui.Hex("#46535a"))
				ui.TextInputBase(c, &w.sessionQuery).Grow(1).MinWidth(0).Height(23).
					Padding(0, 5, 0, 0).Label("搜索会话").Placeholder("搜索会话")
				add := desktopIconButton(c, iconAdd, "添加会话", w.session.editing).Size(25, 23).Radius(0).
					BorderWidth(0, 0, 0, 1).BorderColor(ui.Hex("#e5e8ea"))
				if add.Clicked() {
					w.editSession("")
				}
			})
		})
		if w.providerError != "" {
			ui.Text(c, w.providerError).TextColor(theme.Danger)
		}
		ids := make(map[string][]core.ProviderInfo)
		var groups []string
		query := strings.ToLower(strings.TrimSpace(w.sessionQuery))
		for _, session := range w.providers {
			if query != "" && !strings.Contains(strings.ToLower(session.Name+" "+session.Group), query) {
				continue
			}
			group := session.Group
			if group == "" {
				group = "其他"
				if session.Kind == "sftp" {
					group = "我的会话"
				}
			}
			if _, ok := ids[group]; !ok {
				groups = append(groups, group)
			}
			ids[group] = append(ids[group], session)
		}
		if len(groups) == 0 {
			message := "暂无会话"
			if query != "" {
				message = "没有匹配的会话"
			}
			ui.Text(c, message).TextColor(theme.TextMuted).Padding(4)
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Padding(2, 0, 8).Children(func() {
				for _, group := range groups {
					expanded := w.sessionGroups[group]
					if expanded == nil {
						initial := true
						expanded = &initial
						if w.sessionGroups == nil {
							w.sessionGroups = make(map[string]*bool)
						}
						w.sessionGroups[group] = expanded
					}
					heading := ui.Button(c, "").Label(group).FillWidth().Height(28).Padding(0, 8).Gap(5).
						Background(ui.Transparent).Border(0, ui.Transparent)
					heading.Children(func() {
						icon := iconNext
						if *expanded {
							icon = iconDown
						}
						ui.Icon(c, icon).Width(12).FontSize(17).TextColor(theme.TextMuted)
						ui.Text(c, group).FontWeight(600).SingleLine().Grow(1)
						ui.Text(c, fmt.Sprint(len(ids[group]))).FontSize(10).TextColor(theme.TextMuted)
					})
					if heading.Clicked() {
						*expanded = !*expanded
					}
					if *expanded {
						for _, provider := range ids[group] {
							w.sessionRow(c, provider)
						}
					}
				}
			})
		})
	})
}

func (w *desktopWorkspace) sessionRow(c *ui.Context, provider core.ProviderInfo) {
	active := w.panel(w.activePane).location.provider == provider.ID
	row := ui.Button(c, "").Key(provider.ID).Label(provider.Name).FillWidth().Height(28).
		Padding(0, 8, 0, 25).Gap(7).Background(ui.Transparent).Border(0, ui.Transparent).
		Tooltip(providerLabel(provider) + " · " + provider.Host).AccessSelected(active).Drag(provider)
	if active {
		row.Background(ui.Hex("#dceff1"))
	}
	row.Children(func() {
		color, icon := ui.Hex("#899399"), iconCloud
		if provider.Connected {
			color = ui.Hex("#147481")
		}
		if provider.Kind == "local" {
			icon = iconComputer
		}
		ui.Icon(c, icon).Width(17).FontSize(16).TextColor(color)
		text := ui.Text(c, provider.Name).SingleLine().Grow(1).TextColor(c.Theme().Text)
		if active {
			text.TextColor(ui.Hex("#125b63"))
		}
	})
	if row.DoubleClicked() {
		w.openSessionInPane(1, provider)
	} else if row.Focused() && row.Shortcut(0, ui.KeyEnter) {
		w.openSessionInPane(1, provider)
	}
	if provider.Kind != "local" {
		row.PopoverMenu(func(menu *ui.Menu) {
			label := "连接并打开"
			if provider.Connected {
				label = "在右侧打开"
			}
			if menu.Item(label).Chosen() {
				w.openSessionInPane(1, provider)
			}
			if menu.Item("查看 / 修改配置").Chosen() {
				w.editSession(provider.ID)
			}
			if menu.Item("复制会话").Chosen() {
				w.sessionAction(provider, false)
			}
			if provider.Host != "" && menu.Item("复制主机地址").Chosen() {
				c.WriteClipboard(provider.Host)
			}
			if provider.Kind == "sftp" {
				menu.Separator()
				for _, item := range desktopTerminalItems[:3] {
					if menu.Item(item.label).Chosen() {
						go func() {
							if err := w.connect(provider.ID); err != nil {
								w.window.Update(func() { w.notify(err.Error(), true) })
								return
							}
							w.openTerminal(provider.ID, item.placement, 1)
						}()
					}
				}
			}
			menu.Separator()
			if menu.Item("删除会话").Chosen() {
				w.sessionAction(provider, true)
			}
		})
	}
}

func (w *desktopWorkspace) openSessionInPane(side int, provider core.ProviderInfo) {
	if provider.Kind == "local" || provider.Connected {
		w.activateSessionTab(side, provider)
		return
	}
	if w.pendingSessions == nil {
		w.pendingSessions = make(map[string]bool)
	}
	if w.pendingSessions[provider.ID] {
		return
	}
	w.pendingSessions[provider.ID] = true
	go func() {
		err := w.connect(provider.ID)
		if err == nil {
			var providers []core.ProviderInfo
			err = w.client.request(w.ctx, http.MethodGet, "/api/v1/providers", nil, &providers)
			for _, current := range providers {
				if current.ID == provider.ID {
					provider = current
					break
				}
			}
		}
		w.window.Update(func() {
			delete(w.pendingSessions, provider.ID)
			if err != nil {
				w.notify(err.Error(), true)
				return
			}
			w.activateSessionTab(side, provider)
			w.refreshProviders()
		})
	}()
}

func (w *desktopWorkspace) activateSessionTab(side int, provider core.ProviderInfo) {
	w.activePane = side
	if index := slices.IndexFunc(w.tabs[side], func(panel *desktopPanel) bool {
		return panel.location.provider == provider.ID
	}); index >= 0 {
		w.selectTab(side, index)
		return
	}
	initialPath := "/"
	if provider.Kind == "sftp" && strings.HasPrefix(provider.Location, "/") {
		initialPath = provider.Location
	}
	w.openProviderTab(side, desktopLocation{provider.ID, initialPath})
}
