package main

import (
	"cmp"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"floe/internal/app"
	"floe/internal/core"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type desktopSessionForm struct {
	open, editing, busy bool
	request             core.ConnectRequest
	port, interval      string
	maxFailures         string
	auth, protocol      string
	autoPassword        bool
	hasPassword         bool
	privateKeyFile      string
	groupOpen           bool
	groupIndex          int
	err                 string
}

func (w *desktopWorkspace) editSession(id string) {
	form := &w.session
	*form = desktopSessionForm{
		open: true, editing: true, port: "22", interval: "60", maxFailures: "3", autoPassword: true,
		auth: "密码", protocol: "SFTP", request: core.ConnectRequest{Group: "我的会话"},
	}
	if id == "" {
		return
	}
	form.busy = true
	go func() {
		var details struct {
			core.ConnectRequest
			HasPassword bool `json:"has_password"`
		}
		err := w.client.request(w.ctx, http.MethodGet, "/api/v1/sessions/"+url.PathEscape(id), nil, &details)
		w.window.Update(func() {
			form.busy = false
			if err != nil {
				form.err = err.Error()
				return
			}
			form.request, form.hasPassword = details.ConnectRequest, details.HasPassword
			form.protocol, form.port = strings.ToUpper(details.Protocol), strconv.Itoa(details.Port)
			if details.AuthMethod == "key" {
				form.auth = "SSH 密钥"
			}
			form.autoPassword = details.TerminalAutoPassword == nil || *details.TerminalAutoPassword
			if details.ServerAliveInterval > 0 {
				form.interval = strconv.Itoa(details.ServerAliveInterval)
			}
			if details.ServerAliveCountMax > 0 {
				form.maxFailures = strconv.Itoa(details.ServerAliveCountMax)
			}
		})
	}()
}

func desktopField(c *ui.Context, label string, value *string) ui.Element {
	desktopFieldLabel(c, label)
	return ui.TextInput(c, value).Label(label).FillWidth().Height(29).Padding(0, 7).Background(c.Theme().Background)
}

func (w *desktopWorkspace) sessionDialog(c *ui.Context) {
	form := &w.session
	if !form.open {
		form.editing = false
		return
	}
	title := "添加会话"
	if form.request.ID != "" {
		title = "会话属性 · " + form.request.Name
	}
	if form.groupOpen && c.Shortcut(0, ui.KeyEscape) {
		form.groupOpen = false
	}
	desktopDialog(c, &form.open, title, 520, form.busy, func() {
		sftp := form.protocol == "SFTP"
		ui.Column(c).FillWidth().AlignItems(ui.Stretch).Gap(10).Disabled(form.busy).Children(func() {
			ui.Row(c).Gap(10).Children(func() {
				ui.Column(c).Width(0).Grow(1).Children(func() {
					desktopFieldLabel(c, "协议")
					if ui.Select(c, &form.protocol, []string{"SFTP", "FTP"}).FillWidth().Height(29).Background(c.Theme().Background).Label("协议").Changed() {
						form.port = "22"
						if form.protocol == "FTP" {
							form.port, form.auth = "21", "密码"
						}
					}
				})
				ui.Column(c).Width(145).Children(func() {
					desktopField(c, "端口", &form.port)
				})
			})
			ui.Column(c).FillWidth().Children(func() {
				desktopField(c, "名称", &form.request.Name).Placeholder("生产服务器")
			})
			ui.Column(c).FillWidth().Children(func() {
				desktopFieldLabel(c, "分组")
				w.sessionGroupField(c, form)
			})
			ui.Column(c).FillWidth().Children(func() {
				desktopField(c, "主机", &form.request.Host).Placeholder("192.168.1.20")
			})
			ui.Row(c).Gap(10).Children(func() {
				ui.Column(c).Width(0).Grow(1).Children(func() {
					placeholder := "root"
					if !sftp {
						placeholder = "anonymous（可留空）"
					}
					desktopField(c, "用户名", &form.request.User).Placeholder(placeholder)
				})
				ui.Column(c).Width(145).Children(func() {
					if sftp {
						desktopFieldLabel(c, "登录方式")
						ui.Select(c, &form.auth, []string{"密码", "SSH 密钥"}).FillWidth().Height(29).Background(c.Theme().Background).Label("登录方式")
					}
				})
			})
			key := sftp && form.auth == "SSH 密钥"
			ui.Column(c).FillWidth().Children(func() {
				label, placeholder := "密码", "尚未设置密码"
				if key {
					label, placeholder = "私钥口令（可留空）", "未加密私钥可留空"
					if form.hasPassword && form.request.AuthMethod == "key" {
						placeholder = "留空则保留现有私钥口令"
					}
				} else if form.hasPassword && form.request.AuthMethod != "key" {
					placeholder = "留空则保留现有密码"
				}
				desktopField(c, label, &form.request.Password).Password().Placeholder(placeholder)
			})
			if key {
				ui.Column(c).FillWidth().Children(func() {
					const label = "SSH 私钥文件（公钥认证）"
					desktopFieldLabel(c, label)
					ui.Row(c).FillWidth().Gap(6).Children(func() {
						ui.TextInput(c, &form.request.PrivateKey).Label(label).Height(29).Grow(1).
							Placeholder(`C:\Users\you\.ssh\id_ed25519`)
						if desktopDialogButton(c, "选择文件", false, false).Size(76, 29).Clicked() {
							w.choosePrivateKey()
						}
					})
					hint := "请选择客户端私钥，不能使用 .pub 公钥文件"
					if form.request.PrivateKey != "" {
						hint = "已保存密钥；重新选择文件可替换"
					}
					ui.Text(c, hint).Margin(4, 0, 0).FontSize(11).TextColor(c.Theme().TextMuted)
				})
			}
			ui.Row(c).Height(29).Width(335).Padding(0, 7).Gap(5).AlignItems(ui.Center).
				Border(1, c.Theme().Border).Children(func() {
				if ui.Button(c, "选择文件").Height(23).Padding(0, 6).Radius(2).Clicked() {
					w.choosePrivateKey()
				}
				label := "未选择文件"
				if form.privateKeyFile != "" {
					label = filepath.Base(form.privateKeyFile)
				}
				ui.Text(c, label).SingleLine().Grow(1)
			})

			if sftp {
				ui.Checkbox(c, &form.request.SSHKeepAlive, "启用 SSH 心跳保活").FontSize(12)
				if form.request.SSHKeepAlive {
					ui.Row(c).Gap(10).Padding(9, 10).Border(1, c.Theme().Border).
						Background(c.Theme().Surface).Children(func() {
						ui.Column(c).Width(0).Grow(1).Children(func() { desktopField(c, "心跳间隔（秒）", &form.interval) })
						ui.Column(c).Width(0).Grow(1).Children(func() { desktopField(c, "允许失败次数", &form.maxFailures) })
					})
				}
				ui.Checkbox(c, &form.autoPassword, "Windows Terminal 自动输入已保存密码").FontSize(12)
			}
			if form.err != "" {
				ui.Text(c, form.err).TextColor(c.Theme().Danger)
			}
		})
	}, func() {
		if desktopDialogButton(c, "取消", false, form.busy).Clicked() {
			form.open = false
		}
		if desktopDialogButton(c, "保存", false, form.busy).Clicked() {
			w.saveSession(false)
		}
		if desktopDialogButton(c, "保存并连接", true, form.busy).Clicked() {
			w.saveSession(true)
		}
	})
}

func (w *desktopWorkspace) sessionGroupField(c *ui.Context, form *desktopSessionForm) {
	var groups []string
	for _, provider := range w.providers {
		group := strings.TrimSpace(provider.Group)
		if provider.Kind != "local" && group != "" && !slices.Contains(groups, group) {
			groups = append(groups, group)
		}
	}
	names := collate.New(language.Chinese)
	slices.SortFunc(groups, names.CompareString)
	control := ui.Row(c).Height(29).FillWidth().AlignItems(ui.Center).Border(1, c.Theme().Border)
	control.Children(func() {
		input := ui.TextInputBase(c, &form.request.Group).Label("分组").Grow(1).MinWidth(0).Height(27).Padding(0, 7)
		arrow := desktopIconButton(c, iconDown, "选择已有分组", len(groups) == 0).Size(28, 27).Radius(0).
			BorderWidth(0, 0, 0, 1).BorderColor(ui.Hex("#e5e8ea"))
		if len(groups) == 0 {
			form.groupOpen = false
			return
		}
		if arrow.Clicked() {
			form.groupOpen = !form.groupOpen
			form.groupIndex = max(0, slices.Index(groups, strings.TrimSpace(form.request.Group)))
			input.Focus()
		}
		move := 0
		if input.Shortcut(0, ui.KeyDown) {
			move = 1
		} else if input.Shortcut(0, ui.KeyUp) {
			move = -1
		}
		if move != 0 {
			if form.groupOpen {
				form.groupIndex = (form.groupIndex + move + len(groups)) % len(groups)
			} else {
				form.groupIndex = max(0, slices.Index(groups, strings.TrimSpace(form.request.Group)))
				form.groupOpen = true
			}
		}
		choose := func(index int) {
			form.request.Group, form.groupOpen = groups[index], false
			input.Focus()
		}
		if form.groupOpen && input.Submitted() {
			choose(min(form.groupIndex, len(groups)-1))
		}
		ui.PopoverBase(c, control, &form.groupOpen, func(popup ui.Element) {
			styleDirectoryPopup(popup, max(1, control.Bounds().W))
			popup.Padding(3, 0).Shadow(0, 7, 20, 0, ui.RGBA(28, 37, 41, .18))
			ui.Scroll(c).Height(min(172, float32(len(groups)*28))).Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					for index, group := range groups {
						option := ui.Button(c, "").Label(group).FillWidth().Height(28).Padding(0, 8).
							Border(0, ui.Transparent).Background(ui.Transparent)
						color := c.Theme().Text
						if index == form.groupIndex || option.Hovered() {
							option.Background(c.Theme().Selection)
							color = ui.Hex("#105f68")
						}
						option.Children(func() { ui.Text(c, group).SingleLine().Grow(1).TextColor(color) })
						if option.Clicked() {
							choose(index)
						}
					}
				})
			})
		})
	})
}

func (w *desktopWorkspace) choosePrivateKey() {
	form := &w.session
	form.busy = true
	go func() {
		files, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: w.window, Title: "选择 SSH 私钥文件"})
		w.window.Update(func() {
			form.busy = false
			if err != nil {
				form.err = err.Error()
			} else if len(files) != 0 {
				if strings.EqualFold(filepath.Ext(files[0]), ".pub") {
					form.err = "请选择私钥文件"
					return
				}
				form.privateKeyFile = files[0]
			}
		})
	}()
}

func readDesktopPrivateKey(filename string) ([]byte, error) {
	const limit = 2 << 20
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if len(data) > limit {
		clear(data)
		return nil, errors.New("SSH 私钥文件不能超过 2 MB")
	}
	return data, err
}

func (w *desktopWorkspace) saveSession(connect bool) {
	form := &w.session
	if strings.TrimSpace(form.request.Host) == "" {
		form.err = "主机不能为空"
		return
	}
	if form.protocol == "SFTP" && strings.TrimSpace(form.request.User) == "" {
		form.err = "SFTP 用户名不能为空"
		return
	}
	request := form.request
	request.Protocol = strings.ToLower(form.protocol)
	request.Port, _ = strconv.Atoi(form.port)
	if strings.TrimSpace(form.port) == "" {
		request.Port = 22
		if request.Protocol == "ftp" {
			request.Port = 21
		}
	}
	if request.Port < 1 || request.Port > 65535 {
		form.err = "端口必须在 1–65535 之间。"
		return
	}
	request.AuthMethod = "password"
	if request.Protocol == "sftp" && form.auth == "SSH 密钥" {
		request.AuthMethod = "key"
	}
	if request.Protocol == "ftp" {
		request.SSHKeepAlive = false
	}
	request.ServerAliveInterval, _ = strconv.Atoi(form.interval)
	request.ServerAliveCountMax, _ = strconv.Atoi(form.maxFailures)
	autoPassword := request.Protocol == "sftp" && form.autoPassword
	request.TerminalAutoPassword = &autoPassword
	form.busy, form.err = true, ""
	privateKeyFile := form.privateKeyFile
	go func() {
		var provider core.ProviderInfo
		var err error
		if request.AuthMethod == "key" && privateKeyFile != "" {
			var data []byte
			data, err = readDesktopPrivateKey(privateKeyFile)
			if err == nil {
				var imported struct{ Path string }
				err = w.client.request(w.ctx, http.MethodPost, "/api/v1/sessions/private-key", data, &imported)
				clear(data)
				request.PrivateKey = imported.Path
			}
		}
		if err == nil {
			err = w.client.request(w.ctx, http.MethodPost, "/api/v1/sessions", request, &provider)
		}
		w.window.Update(func() {
			form.busy = false
			if err != nil {
				form.err = err.Error()
				return
			}
			form.open = false
			form.request.Password = ""
			w.refreshProviders()
			if connect {
				w.openSessionInPane(1, provider)
			}
		})
	}()
}

func (w *desktopWorkspace) sessionAction(provider core.ProviderInfo, remove bool) {
	go func() {
		method, endpoint := http.MethodPost, "/api/v1/sessions/"+url.PathEscape(provider.ID)+"/copy"
		if remove {
			chosen, err := w.ask(mygo.MessageOptions{
				Type: mygo.MessageWarning, Message: "删除会话 " + provider.Name + "？",
				Buttons: []string{"取消", "删除"}, DestructiveButtons: []int{1},
			})
			if err != nil || chosen != 1 {
				return
			}
			method, endpoint = http.MethodDelete, strings.TrimSuffix(endpoint, "/copy")
		}
		var duplicate core.ProviderInfo
		var result any
		if !remove {
			result = &duplicate
		}
		err := w.client.request(w.ctx, method, endpoint, nil, result)
		w.window.Update(func() {
			if err != nil {
				w.providerError = err.Error()
				return
			}
			w.refreshProviders()
			if !remove {
				w.editSession(duplicate.ID)
				return
			}
			for side := range w.tabs {
				active := w.panel(side)
				w.tabs[side] = slices.DeleteFunc(w.tabs[side], func(panel *desktopPanel) bool {
					return panel.location.provider == provider.ID
				})
				if len(w.tabs[side]) == 0 {
					w.closeAllTabs(side)
					continue
				}
				if index := slices.Index(w.tabs[side], active); index >= 0 {
					w.active[side] = index
				} else {
					w.selectTab(side, 0)
				}
			}
		})
	}()
}

type desktopTemplateForm struct {
	open, busy bool
	template   app.TransferTemplate
	err        string
}

func (w *desktopWorkspace) refreshTemplates() {
	go func() {
		var result []app.TransferTemplate
		err := w.client.request(w.ctx, http.MethodGet, "/api/v1/transfer-templates", nil, &result)
		w.window.Update(func() {
			if err == nil {
				w.templates = result
			} else {
				w.notify(err.Error(), true)
			}
		})
	}()
}

func (w *desktopWorkspace) publishSidebar(c *ui.Context) {
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(7, 6, 12).Gap(1).Children(func() {
			if len(w.templates) == 0 {
				ui.Text(c, "还没有发布任务\n在成功列表中右键保存模板").
					FillWidth().Padding(20, 10).TextAlign(ui.Center).TextColor(c.Theme().TextMuted)
			}
			for _, item := range w.templates {
				row := ui.Row(c).Key(item.ID).Height(31).FillWidth().Padding(0, 2, 0, 0).Gap(1).AlignItems(ui.Center).
					BorderWidth(0, 0, 1, 0).BorderColor(ui.Hex("#e5e8ea"))
				if row.Hovered() {
					row.Background(c.Theme().SurfaceHover)
				}
				row.Children(func() {
					name := ui.Button(c, "").Label(item.Name).Width(0).Grow(1).MinWidth(0).Height(30).Padding(0, 5).
						Background(ui.Transparent).Border(0, ui.Transparent)
					name.Children(func() {
						ui.Text(c, item.Name).FontSize(12).SingleLine().Grow(1)
					})
					if desktopIconButton(c, iconPlay, "执行发布任务", false).Size(25, 25).Clicked() {
						w.templateAction(item.ID, "run")
					}
					if desktopIconButton(c, iconEdit, "编辑发布任务", false).Size(25, 25).Clicked() {
						w.editTemplate(item)
					}
					if desktopIconButton(c, iconDelete, "删除发布任务", false).Size(25, 25).Clicked() {
						w.templateAction(item.ID, "delete")
					}
					if name.Hovered() || name.FocusVisible() {
						w.publishDetails(c, name, item)
					}
				})
			}
		})
	})
}

func (w *desktopWorkspace) publishDetails(c *ui.Context, anchor ui.Element, template app.TransferTemplate) {
	_, height := c.Size()
	ui.Overlay(c, func() {
		ui.Column(c).AttachTo(anchor, ui.AnchorTopRight, ui.AnchorTopLeft).Width(470).
			Margin(0, 0, 0, 8).Border(1, ui.Hex("#9eabad")).Background(c.Theme().Background).
			Shadow(0, 10, 28, 0, ui.RGBA(28, 37, 41, .24)).Children(func() {
			ui.Scroll(c).MaxHeight(min(498, height-18)).Children(func() {
				ui.Column(c).FillWidth().Padding(10, 12).Gap(7).Children(func() {
					ui.Text(c, template.Name).FontSize(11).FontWeight(650).TextColor(c.Theme().Accent)
					w.publishItems(c, &template, false)
				})
			})
		})
	})
}

func (w *desktopWorkspace) editTemplate(template app.TransferTemplate) {
	template.Tasks = slices.Clone(templateTasks(template))
	w.template = desktopTemplateForm{open: true, template: template}
}

func templateTasks(template app.TransferTemplate) []app.TransferTemplateTask {
	if len(template.Tasks) > 0 {
		return template.Tasks
	}
	return []app.TransferTemplateTask{{
		SourceProvider: template.SourceProvider, SourcePath: template.SourcePath,
		TargetProvider: template.TargetProvider, TargetPath: template.TargetPath,
		ConflictPolicy: template.ConflictPolicy, Concurrency: template.Concurrency,
		Verify: template.Verify, PreserveStructure: template.PreserveStructure, Filter: template.Filter,
	}}
}

func (w *desktopWorkspace) publishItems(c *ui.Context, template *app.TransferTemplate, editable bool) {
	type providers struct{ source, target string }
	indices := make(map[providers][]int)
	var groups []providers
	tasks := templateTasks(*template)
	if editable {
		tasks = template.Tasks
	}
	for index, item := range tasks {
		key := providers{item.SourceProvider, item.TargetProvider}
		if len(indices[key]) == 0 {
			groups = append(groups, key)
		}
		indices[key] = append(indices[key], index)
	}
	remove := -1
	name := func(id, fallback string) string {
		for _, provider := range w.providers {
			if provider.ID == id {
				return provider.Name
			}
		}
		return cmp.Or(fallback, id, "未指定")
	}
	for i, group := range groups {
		items := indices[group]
		first := tasks[items[0]]
		title := name(group.source, first.SourceProviderName) + " -> " + name(group.target, first.TargetProviderName) + ":"
		if editable {
			ui.Text(c, title).Padding(5, 4, 3).FontSize(11).FontWeight(650)
		} else {
			if i > 0 {
				ui.Box(c).FillWidth().Height(1).Margin(5, 0, 4).Background(ui.Hex("#e5e8ea"))
			}
			ui.Text(c, title).FontSize(11).FontWeight(650)
		}
		for _, index := range items {
			item := tasks[index]
			text := "- " + cmp.Or(item.SourcePath, "/") + " -> " + cmp.Or(item.TargetPath, "/")
			if editable {
				ui.Row(c).Key(index).Height(27).FillWidth().Padding(0, 0, 0, 9).Gap(3).AlignItems(ui.Center).
					Background(c.Theme().Background).BorderWidth(0, 0, 1, 0).BorderColor(ui.Hex("#e5e8ea")).Children(func() {
					ui.Text(c, text).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine().Grow(1).Tooltip(text)
					if desktopIconButton(c, iconDelete, "删除此发布项", w.template.busy).Size(25, 25).Clicked() {
						remove = index
					}
				})
			} else {
				ui.Text(c, text).FontSize(11).TextColor(c.Theme().TextMuted)
			}
		}
	}
	if remove >= 0 {
		template.Tasks = slices.Delete(template.Tasks, remove, remove+1)
	}
}

func (w *desktopWorkspace) templateDialog(c *ui.Context) {
	form := &w.template
	if !form.open {
		return
	}
	title, width := "编辑发布任务", float32(760)
	if form.template.ID == "" {
		title, width = "模板名称", 400
	}
	desktopDialog(c, &form.open, title, width, form.busy, func() {
		ui.Column(c).FillWidth().Children(func() {
			desktopField(c, "任务名称", &form.template.Name).Disabled(form.busy)
		})
		if form.template.ID != "" {
			ui.Scroll(c).MaxHeight(360).FillWidth().Padding(7).Background(c.Theme().Surface).
				Border(1, c.Theme().Border).Children(func() {
				ui.Column(c).FillWidth().Gap(1).Children(func() { w.publishItems(c, &form.template, true) })
			})
		}
		if form.err != "" {
			ui.Text(c, form.err).TextColor(c.Theme().Danger)
		}
	}, func() {
		if desktopDialogButton(c, "取消", false, form.busy).Clicked() {
			form.open = false
		}
		if desktopDialogButton(c, "保存", true, form.busy).Clicked() {
			w.saveTemplate()
		}
	})
}

func (w *desktopWorkspace) saveTemplate() {
	form := &w.template
	template := form.template
	if strings.TrimSpace(template.Name) == "" || len(template.Tasks) == 0 {
		form.err = "请填写任务名称并保留至少一个发布项"
		return
	}
	form.busy, form.err = true, ""
	go func() {
		err := w.client.request(w.ctx, http.MethodPost, "/api/v1/transfer-templates", template, nil)
		w.window.Update(func() {
			form.busy = false
			if err != nil {
				form.err = err.Error()
				return
			}
			form.open = false
			w.refreshTemplates()
		})
	}()
}

func (w *desktopWorkspace) templateAction(id, action string) {
	go func() {
		endpoint := "/api/v1/transfer-templates/" + url.PathEscape(id)
		method, body := http.MethodDelete, any(nil)
		if action == "run" {
			method, endpoint, body = http.MethodPost, endpoint+"/run", struct{}{}
		} else {
			answer, err := w.ask(mygo.MessageOptions{Type: mygo.MessageWarning, Message: "删除此发布任务？", Buttons: []string{"取消", "删除"}})
			if err != nil || answer != 1 {
				return
			}
		}
		err := w.client.request(w.ctx, method, endpoint, body, nil)
		w.window.Update(func() {
			if err != nil {
				w.notify(err.Error(), true)
				w.setQueueTab(queueActive)
			} else if action == "run" {
				w.setQueueTab(queueActive)
			} else {
				w.refreshTemplates()
			}
		})
	}()
}
