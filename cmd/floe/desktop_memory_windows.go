package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/text/width"
)

type desktopMemoryHit struct {
	ID         string `json:"id"`
	HitID      string `json:"hit_id"`
	BlockID    string `json:"block_id"`
	AnchorLine int    `json:"anchor_line"`
	Snippet    string `json:"snippet"`
	Match      string `json:"match"`
	Source     string `json:"source"`
	SourcePath string `json:"source_path"`
}

type desktopMemoryEntry struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	Source     string `json:"source"`
	SourcePath string `json:"source_path"`
}

type desktopMemoryHistory struct {
	Query string `json:"query"`
}

type desktopMemory struct {
	query, err                      string
	loaded, busy, showSecrets       bool
	hits                            []desktopMemoryHit
	history                         []desktopMemoryHistory
	editor                          *desktopMemoryEditor
	pageDirection                   string
	blockedBefore, blockedAfter     bool
	stream                          desktopMemoryStream
	streamList                      ui.ListState
	streamBusy                      bool
	highlight                       *regexp.Regexp
	index, seq, detailSeq, querySeq int
	list                            ui.ListState
	dialog                          string
	open                            bool
	dialogBusy, copyExisting        bool
	draft, defaultPath              string
	settingsCount                   int
	dialogError                     string
}

var memorySecrets = regexp.MustCompile(`(?i)((?:密码|口令|password|passwd|pwd)\s*[:=：]\s*)[^\s,，;；]+`)

func (w *desktopWorkspace) searchMemory() {
	w.searchMemoryAt("", 0)
}

func (w *desktopWorkspace) searchMemoryAt(recordID string, line int) {
	state := &w.memory
	state.seq++
	state.querySeq++
	request, query := state.seq, strings.TrimSpace(state.query)
	state.highlight = memoryPattern(query)
	state.busy, state.loaded, state.err, state.index = true, true, "", -1
	state.list = ui.ListState{}
	go func() {
		var hits []desktopMemoryHit
		var err error
		if query != "" {
			err = w.client.request(w.ctx, http.MethodGet, "/api/v1/memories?"+url.Values{"q": {query}, "limit": {"100"}}.Encode(), nil, &hits)
			if err == nil {
				_ = w.client.request(w.ctx, http.MethodPost, "/api/v1/memory-search-history", map[string]string{"query": query}, nil)
			}
		}
		var history []desktopMemoryHistory
		_ = w.client.request(w.ctx, http.MethodGet, "/api/v1/memory-search-history", nil, &history)
		w.window.Update(func() {
			if request != state.seq {
				return
			}
			state.busy, state.history = false, history
			state.stream, state.streamBusy = desktopMemoryStream{}, false
			state.pageDirection = ""
			state.detailSeq++
			if err != nil {
				state.err = err.Error()
				return
			}
			state.hits = hits
			if recordID != "" {
				distance := int(^uint(0) >> 1)
				for i, hit := range hits {
					if hit.ID == recordID {
						delta := max(line-hit.AnchorLine, hit.AnchorLine-line)
						if delta < distance {
							state.index, distance = i, delta
						}
					}
				}
				if state.index < 0 {
					w.readMemory(desktopMemoryHit{BlockID: fmt.Sprintf("%s:%d", recordID, line)})
					return
				}
			} else if len(hits) > 0 {
				state.index = 0
			}
			if state.index >= 0 {
				w.readMemory(hits[state.index])
			}
		})
	}()
}

func (w *desktopWorkspace) debounceMemorySearch() {
	state := &w.memory
	state.querySeq++
	sequence := state.querySeq
	time.AfterFunc(180*time.Millisecond, func() {
		if w.ctx.Err() != nil {
			return
		}
		w.window.Update(func() {
			if state.querySeq == sequence {
				w.searchMemory()
			}
		})
	})
}

func (w *desktopWorkspace) readMemory(hit desktopMemoryHit) {
	state := &w.memory
	state.detailSeq++
	request := state.detailSeq
	state.showSecrets, state.streamBusy = false, true
	state.pageDirection = ""
	state.blockedBefore, state.blockedAfter = false, false
	go func() {
		var stream desktopMemoryStream
		query := url.Values{"anchor": {hit.BlockID}, "limit": {"80"}}
		err := w.client.request(w.ctx, http.MethodGet, "/api/v1/memory-stream?"+query.Encode(), nil, &stream)
		w.window.Update(func() {
			if request != state.detailSeq {
				return
			}
			state.streamBusy = false
			if err != nil {
				state.err = err.Error()
				return
			}
			state.stream, state.streamList = stream, ui.ListState{}
			for i, block := range stream.Blocks {
				if block.ID == stream.Anchor {
					state.streamList.ScrollTo(i, ui.Center)
					break
				}
			}
		})
	}()
}

func (w *desktopWorkspace) memoryView(c *ui.Context) {
	state := &w.memory
	ui.Column(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Height(35).Gap(6).Padding(4, 6).AlignItems(ui.Center).Background(ui.Hex("#f8f9fa")).
			BorderWidth(0, 0, 1, 0).BorderColor(desktopTheme.Border).Children(func() {
			ui.Row(c).Grow(1).MinWidth(0).Height(27).AlignItems(ui.Center).Background(desktopTheme.Background).
				Border(1, ui.Hex("#aeb7bb")).Children(func() {
				search := ui.SearchField(c, &state.query).Grow(1).MinWidth(0).Height(25).Border(0, ui.Transparent).
					Background(ui.Transparent).Label("搜索速查").Placeholder("搜索文字或命令，使用引号精确匹配")
				if search.Submitted() {
					w.searchMemory()
				} else if search.Changed() {
					w.debounceMemorySearch()
				}
				desktopIconButton(c, iconDown, "显示搜索历史", false).PopoverMenuButton(func(history *ui.Menu) {
					if len(state.history) == 0 {
						history.Item("暂无搜索历史").Disabled(true)
					}
					for _, item := range state.history {
						if history.Item(item.Query).Chosen() {
							state.query = item.Query
							w.searchMemory()
						}
					}
				})
			})
			if memoryToolbarButton(c, iconAdd, "随手记").Clicked() {
				if state.editor == nil || !state.editor.saving {
					state.editor = &desktopMemoryEditor{}
				}
			}
			if memoryToolbarButton(c, iconUpload, "导入").Clicked() {
				w.importMemories()
			}
			if memoryToolbarButton(c, iconFolder, "位置").Clicked() {
				w.memorySettings()
			}
			if desktopIconButton(c, iconMaximize, "展开或还原速查", false).Width(31).Border(1, desktopTheme.Border).Clicked() {
				w.queueExpanded = !w.queueExpanded
			}
		})
		if state.err != "" {
			ui.Text(c, state.err).TextColor(c.Theme().Danger).Padding(3, 8)
		}
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).WidthPercent(36).MinHeight(0).BorderWidth(0, 1, 0, 0).BorderColor(c.Theme().Border).Children(func() {
				if len(state.hits) == 0 {
					ui.Column(c).Grow(1).Center().Gap(5).Padding(12).Children(func() {
						switch {
						case state.busy:
							ui.Text(c, "正在搜索…").TextColor(c.Theme().TextMuted)
						case strings.TrimSpace(state.query) == "":
							ui.Text(c, "输入关键词开始搜索").FontSize(12).Bold()
							ui.Text(c, "左侧只显示实际命中的内容片段").TextColor(c.Theme().TextMuted)
						default:
							ui.Text(c, "没有匹配的速查内容").TextColor(c.Theme().TextMuted)
						}
					})
					return
				}
				state.list.Key = func(index int) any { return state.hits[index].HitID }
				state.list.Selected = &state.index
				state.list.Label = func(index int) string { return state.hits[index].Snippet }
				results := ui.List(c, &state.list, len(state.hits), func(index int) {
					hit := state.hits[index]
					row := ui.Column(c).FillWidth().Padding(8, 10, 7).Gap(3).
						Background(desktopTheme.Background).BorderWidth(0, 0, 1, 0).BorderColor(ui.Hex("#e5e8ea"))
					if index == state.index {
						row.Background(ui.Hex("#dceff1")).BorderWidth(0, 0, 1, 3).BorderColor(desktopTheme.Accent)
					}
					row.Children(func() {
						snippet := memorySecrets.ReplaceAllString(hit.Snippet, "${1}••••••••")
						ui.RichText(c, memorySpans(snippet, state.highlight)...).FontSize(11).FontWeight(550).LineHeight(1.48).MaxLines(3).TextColor(ui.Hex("#303a40"))
						source := memorySource(hit.Source, hit.SourcePath)
						if match := memoryMatchName(hit.Match); match != "" {
							source = match + " · " + source
						}
						ui.Text(c, source).SingleLine().FontSize(10).TextColor(desktopTheme.TextMuted)
					})
				}).Grow(1).MinHeight(0)
				if results.Changed() && state.index >= 0 && state.index < len(state.hits) {
					w.readMemory(state.hits[state.index])
				}

			})
			ui.Column(c).Width(0).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() { w.memoryStreamView(c) })
		})
	})
}

func memoryToolbarButton(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	return ui.Button(c, "").Label(label).Height(27).Padding(0, 8).Gap(4).Radius(0).
		Border(1, desktopTheme.Border).Background(desktopTheme.Background).Children(func() {
		ui.Icon(c, icon).FontSize(16).TextColor(ui.Hex("#3f4d54"))
		ui.Text(c, label).FontSize(11).TextColor(ui.Hex("#3f4d54"))
	})
}

type desktopMemoryBlock struct {
	ID            string    `json:"id"`
	MemoryID      string    `json:"memory_id"`
	Text          string    `json:"text"`
	Source        string    `json:"source"`
	SourcePath    string    `json:"source_path"`
	Line          int       `json:"line"`
	CreatedAt     time.Time `json:"created_at"`
	DocumentStart bool      `json:"document_start"`
}

type desktopMemoryStream struct {
	Blocks    []desktopMemoryBlock `json:"blocks"`
	Anchor    string               `json:"anchor"`
	HasBefore bool                 `json:"has_before"`
	HasAfter  bool                 `json:"has_after"`
}

var memoryQuoted = regexp.MustCompile(`["“”]([^"“”]+)["“”]`)

func memoryPattern(query string) *regexp.Regexp {
	query = width.Fold.String(query)
	var terms []string
	query = memoryQuoted.ReplaceAllStringFunc(query, func(quoted string) string {
		match := memoryQuoted.FindStringSubmatch(quoted)
		if phrase := strings.Join(strings.Fields(match[1]), " "); phrase != "" {
			terms = append(terms, phrase)
		}
		return " "
	})
	plain := strings.FieldsFunc(query, func(r rune) bool {
		return strings.ContainsRune(",，;；。.!！?？、", r) || unicode.IsSpace(r)
	})
	if len(terms) == 0 && len(plain) > 1 {
		terms = append(terms, strings.Join(plain, " "))
	}
	for _, term := range append(slices.Clone(terms), plain...) {
		terms = append(terms, strings.Fields(term)...)
	}
	if len(terms) == 0 {
		return nil
	}
	slices.SortStableFunc(terms, func(a, b string) int { return len(b) - len(a) })
	seen := make(map[string]bool, len(terms))
	var escaped []string
	for _, term := range terms {
		key := strings.ToLower(term)
		if !seen[key] {
			escaped = append(escaped, regexp.QuoteMeta(term))
			seen[key] = true
		}
	}
	return regexp.MustCompile("(?i)" + strings.Join(escaped, "|"))
}

func memorySpans(text string, match *regexp.Regexp) []ui.Span {
	if match == nil || text == "" {
		return []ui.Span{{Text: text}}
	}
	var spans []ui.Span
	offset := 0
	for _, loc := range match.FindAllStringIndex(text, -1) {
		if loc[0] > offset {
			spans = append(spans, ui.Span{Text: text[offset:loc[0]]})
		}
		spans = append(spans, ui.Span{Text: text[loc[0]:loc[1]], Background: ui.Hex("#ffe79a"), PaddingX: 1})
		offset = loc[1]
	}
	if offset < len(text) {
		spans = append(spans, ui.Span{Text: text[offset:]})
	}
	return spans
}

func memoryMatchName(match string) string {
	switch match {
	case "exact":
		return "精确短语"
	case "phrase":
		return "完整短语"
	case "all":
		return "全部词"
	case "partial":
		return "部分匹配"
	default:
		return ""
	}
}

func memorySource(source, name string) string {
	switch source {
	case "import":
		if name != "" {
			return "导入 · " + name
		}
		return "导入"
	case "onenote":
		if name != "" {
			return "OneNote · " + name
		}
		return "OneNote"
	default:
		return "Floe 随手记"
	}
}

func (w *desktopWorkspace) memoryStreamView(c *ui.Context) {
	state := &w.memory
	if state.editor != nil {
		w.memoryEditorView(c, state.editor)
		return
	}
	stream := state.stream
	if state.streamBusy || len(stream.Blocks) == 0 {
		ui.Column(c).Grow(1).Center().Gap(4).Background(ui.Hex("#f8fafb")).Children(func() {
			label := "搜索并选择左侧片段，右侧将在统一知识流中定位"
			if state.streamBusy {
				ui.Spinner(c).Size(14, 14).TextColor(c.Theme().Accent)
				label = "正在定位知识内容…"
			}
			ui.Text(c, label).TextColor(c.Theme().TextMuted)
		})
		return
	}
	state.streamList.Key = func(row int) any { return stream.Blocks[row].ID }
	list := ui.List(c, &state.streamList, len(stream.Blocks), func(i int) {
		block := stream.Blocks[i]
		ui.Column(c).FillWidth().Children(func() {
			if i == 0 && stream.HasBefore {
				memoryEdge(c, "向上滚动加载更早内容", "正在加载更早内容…", state.pageDirection == "before")
			}
			if block.DocumentStart || i == 0 {
				ui.Row(c).Height(25).Margin(5, 15, 4).AlignItems(ui.Center).
					BorderWidth(1, 0, 0, 0).BorderColor(ui.Hex("#e5e8ea")).Children(func() {
					label := memorySource(block.Source, block.SourcePath) + " · " + block.CreatedAt.Local().Format("06/01/02 15:04")
					ui.Text(c, label).FontSize(10).TextColor(ui.Hex("#879096")).Grow(1)
					edit := ui.Button(c, "").Label("编辑").Height(23).Padding(0, 6).Gap(3).
						Background(ui.Transparent).Border(0, ui.Transparent)
					edit.Children(func() {
						ui.Icon(c, iconEdit).FontSize(11).TextColor(c.Theme().TextMuted)
						ui.Text(c, "编辑").FontSize(10).TextColor(c.Theme().TextMuted)
					})
					if edit.Clicked() {
						w.editMemoryRecord(block)
					}
				})
			}
			row := ui.Column(c).FillWidth().MinHeight(20).Padding(0, 18)
			if block.ID == stream.Anchor {
				row.Background(ui.Hex("#e2f2f4")).BorderWidth(0, 0, 0, 3).BorderColor(desktopTheme.Accent).Padding(0, 15)
			}
			row.Children(func() {
				value := block.Text
				if !state.showSecrets {
					value = memorySecrets.ReplaceAllString(value, "${1}••••••••")
				}
				ui.RichText(c, memorySpans(value, state.highlight)...).FillWidth().Font("Cascadia Code").FontSize(12).
					LineHeight(1.65).TextColor(ui.Hex("#273036")).Selectable()
			})
			if i == len(stream.Blocks)-1 && stream.HasAfter {
				memoryEdge(c, "向下滚动加载后续内容", "正在加载后续内容…", state.pageDirection == "after")
			}
		})
	}).Grow(1).MinHeight(0).Padding(7, 0, 18)
	list.PopoverMenu(func(menu *ui.Menu) {
		if menu.Item("显示敏感字段").Checked(state.showSecrets).Chosen() {
			state.showSecrets = !state.showSecrets
		}
	})
	first, last := state.streamList.Visible()
	if last < first {
		return
	}
	nearBefore, nearAfter := first <= 4, last >= len(stream.Blocks)-6
	if !nearBefore {
		state.blockedBefore = false
	}
	if !nearAfter {
		state.blockedAfter = false
	}
	if state.pageDirection == "" {
		if nearBefore && stream.HasBefore && !state.blockedBefore {
			w.pageMemory(true)
		} else if nearAfter && stream.HasAfter && !state.blockedAfter {
			w.pageMemory(false)
		}
	}
}

func memoryEdge(c *ui.Context, idle, loading string, busy bool) {
	if busy {
		idle = loading
	}
	ui.Text(c, idle).FillWidth().Padding(8, 15).TextAlign(ui.Center).
		FontSize(10).TextColor(ui.Hex("#8a9499"))
}

func (w *desktopWorkspace) pageMemory(before bool) {
	state := &w.memory
	if state.streamBusy || state.pageDirection != "" || len(state.stream.Blocks) == 0 {
		return
	}
	request := state.detailSeq
	state.pageDirection = "after"
	if before {
		state.pageDirection = "before"
	}
	query := url.Values{"limit": {"60"}}
	if before {
		query.Set("before", state.stream.Blocks[0].ID)
	} else {
		query.Set("after", state.stream.Blocks[len(state.stream.Blocks)-1].ID)
	}
	go func() {
		var page desktopMemoryStream
		err := w.client.request(w.ctx, http.MethodGet, "/api/v1/memory-stream?"+query.Encode(), nil, &page)
		w.window.Update(func() {
			if state.detailSeq != request {
				return
			}
			state.pageDirection = ""
			if err != nil {
				if before {
					state.blockedBefore = true
				} else {
					state.blockedAfter = true
				}
				w.notify(err.Error(), true)
				return
			}
			if before {
				state.stream.Blocks = append(page.Blocks, state.stream.Blocks...)
				state.stream.HasBefore = page.HasBefore
			} else {
				state.stream.Blocks = append(state.stream.Blocks, page.Blocks...)
				state.stream.HasAfter = page.HasAfter
			}
		})
	}()
}

type desktopMemoryEditor struct {
	id              string
	line            int
	entry           desktopMemoryEntry
	draft, err      string
	loading, saving bool
}

func (w *desktopWorkspace) editMemoryRecord(block desktopMemoryBlock) {
	edit := &desktopMemoryEditor{id: block.MemoryID, line: block.Line, loading: true}
	w.memory.editor = edit
	go func() {
		var entry desktopMemoryEntry
		err := w.client.request(w.ctx, http.MethodGet, "/api/v1/memories/"+url.PathEscape(edit.id), nil, &entry)
		w.window.Update(func() {
			if w.memory.editor != edit {
				return
			}
			edit.loading = false
			if err != nil {
				edit.err = err.Error()
				return
			}
			edit.entry, edit.draft = entry, entry.Content
		})
	}()
}

func (w *desktopWorkspace) memoryEditorView(c *ui.Context, edit *desktopMemoryEditor) {
	theme := c.Theme()
	ui.Column(c.Key(edit)).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Background(theme.Background).Children(func() {
		ui.Row(c).Height(34).Shrink(0).Gap(8).Padding(0, 10).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
			title, hint := "随手记", "不需要标题或标签，第一行会作为摘要"
			if edit.id != "" {
				title, hint = "编辑原始记录", memorySource(edit.entry.Source, edit.entry.SourcePath)+" · Ctrl+Enter 保存"
			}
			ui.Text(c, title).FontSize(12).Bold().Shrink(0)
			ui.Box(c).Grow(1)
			ui.Text(c, hint).MinWidth(0).SingleLine().FontSize(10).TextColor(theme.TextMuted)
		})
		disabled := edit.loading || edit.saving || (edit.id != "" && edit.entry.ID == "")
		field := ui.TextArea(c, &edit.draft).Grow(1).MinHeight(0).Padding(12, 14).
			Border(0, ui.Transparent).Background(theme.Background).Font("Cascadia Code").FontSize(12).
			LineHeight(1.6).Label("速查内容").Placeholder("输入任何需要记住的内容……").Disabled(disabled)
		if !disabled {
			field.AutoFocus()
		}
		if edit.err != "" {
			ui.Text(c, edit.err).Padding(3, 10).FontSize(11).TextColor(theme.Danger)
		}
		ui.Row(c).Height(38).Shrink(0).Gap(6).Padding(0, 9).AlignItems(ui.Center).
			Background(theme.Surface).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
			if edit.id != "" && memoryToolbarButton(c, iconDelete, "删除整条记录").Disabled(disabled).Clicked() {
				w.deleteMemoryRecord(edit)
			}
			ui.Box(c).Grow(1)
			if toolboxButton(c, "取消").Disabled(edit.saving).Clicked() {
				w.memory.editor = nil
				c.Invalidate()
				return
			}
			if (ui.PrimaryButton(c, "保存记录").Height(27).FontSize(11).Disabled(disabled).Clicked() ||
				field.Shortcut(ui.Ctrl, ui.KeyEnter)) && !disabled {
				w.saveMemoryRecord(edit)
			}
		})
	})
}

func (w *desktopWorkspace) saveMemoryRecord(edit *desktopMemoryEditor) {
	content := strings.TrimSpace(edit.draft)
	if content == "" {
		if edit.id != "" {
			w.deleteMemoryRecord(edit)
		} else {
			edit.err = "请输入需要记录的内容"
		}
		return
	}
	endpoint, method := "/api/v1/memories", http.MethodPost
	body := map[string]string{"content": content}
	if edit.id != "" {
		endpoint, method = endpoint+"/"+url.PathEscape(edit.id), http.MethodPut
		body["source"], body["source_path"] = edit.entry.Source, edit.entry.SourcePath
	}
	edit.saving, edit.err = true, ""
	go func() {
		var saved desktopMemoryEntry
		err := w.client.request(w.ctx, method, endpoint, body, &saved)
		w.window.Update(func() {
			edit.saving = false
			if err != nil {
				edit.err = err.Error()
				return
			}
			w.memory.editor = nil
			if edit.id != "" {
				w.notify("速查记录已更新", false)
				w.searchMemoryAt(saved.ID, min(edit.line, strings.Count(saved.Content, "\n")))
			} else {
				w.notify("已记下", false)
				w.searchMemory()
			}
		})
	}()
}

func (w *desktopWorkspace) deleteMemoryRecord(edit *desktopMemoryEditor) {
	edit.saving, edit.err = true, ""
	go func() {
		answer, err := w.ask(mygo.MessageOptions{Type: mygo.MessageWarning,
			Message: "确定删除这整条原始记录吗？此操作无法撤销。", Buttons: []string{"取消", "删除"}})
		if err == nil && answer == 1 {
			err = w.client.request(w.ctx, http.MethodDelete, "/api/v1/memories/"+url.PathEscape(edit.id), nil, nil)
		}
		w.window.Update(func() {
			edit.saving = false
			if err != nil {
				edit.err = err.Error()
			} else if answer == 1 {
				w.memory.editor = nil
				w.notify("原始记录已删除", false)
				w.searchMemory()
			}
		})
	}()
}

func (w *desktopWorkspace) memorySettings() {
	state := &w.memory
	state.dialog, state.open, state.dialogBusy = "知识库存储位置", true, true
	state.dialogError, state.copyExisting = "", true
	go func() {
		var settings struct {
			Path    string
			Default string `json:"default_path"`
			Count   int    `json:"count"`
		}
		err := w.client.request(w.ctx, http.MethodGet, "/api/v1/memory-settings", nil, &settings)
		w.window.Update(func() {
			state.dialogBusy = false
			if err != nil {
				state.dialogError = err.Error()
				return
			}
			state.draft, state.defaultPath, state.settingsCount = settings.Path, settings.Default, settings.Count
		})
	}()
}

func (w *desktopWorkspace) memoryDialog(c *ui.Context) {
	state := &w.memory
	if !state.open {
		return
	}
	desktopDialog(c, &state.open, state.dialog, 650, state.dialogBusy, func() {
		ui.Column(c).FillWidth().Children(func() {
			desktopField(c, "存储目录", &state.draft).Font("Cascadia Code").Disabled(state.dialogBusy)
			summary := "知识库内容和加密密钥将保存在此目录"
			if state.defaultPath != "" {
				summary = fmt.Sprintf("%d 条记录 · 默认位置：%s", state.settingsCount, state.defaultPath)
			}
			ui.Text(c, summary).Margin(4, 0, 0).
				FontSize(11).TextColor(c.Theme().TextMuted)
		})
		ui.Checkbox(c, &state.copyExisting, "将当前知识库复制到新位置").FontSize(12).Disabled(state.dialogBusy)
		ui.Text(c, "如果目标目录已经包含知识库，请取消复制选项以直接切换。原目录不会自动删除，可作为备份保留。").
			Padding(8, 9).Background(ui.Hex("#f8fafb")).Border(1, ui.Hex("#d8dfe2")).
			FontSize(11).TextColor(c.Theme().TextMuted)
		if state.dialogError != "" {
			ui.Text(c, state.dialogError).TextColor(c.Theme().Danger)
		}
	}, func() {
		if desktopDialogButton(c, "恢复默认位置", false, state.dialogBusy).Clicked() {
			state.draft = state.defaultPath
		}
		if desktopDialogButton(c, "取消", false, state.dialogBusy).Clicked() {
			state.open = false
		}
		if desktopDialogButton(c, "保存并切换", true, state.dialogBusy).Clicked() {
			w.saveMemorySettings()
		}
	})
}

func (w *desktopWorkspace) saveMemorySettings() {
	state := &w.memory
	body := map[string]any{"path": strings.TrimSpace(state.draft), "copy_existing": state.copyExisting}
	state.dialogError, state.dialogBusy = "", true
	go func() {
		err := w.client.request(w.ctx, http.MethodPut, "/api/v1/memory-settings", body, nil)
		w.window.Update(func() {
			state.dialogBusy = false
			if err != nil {
				state.dialogError = err.Error()
				return
			}
			state.open = false
			w.searchMemory()
		})
	}()
}

func (w *desktopWorkspace) importMemories() {
	go func() {
		files, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: w.window, Title: "导入文本 / Markdown", Multiple: true,
			Filters: []mygo.FileFilter{{Name: "文本与 Markdown", Extensions: []string{"txt", "md", "markdown"}}},
		})
		for _, filename := range files {
			file, openErr := os.Open(filename)
			if openErr != nil {
				err = openErr
				break
			}
			const limit = 1 << 20
			data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
			_ = file.Close()
			if readErr != nil {
				err = readErr
				break
			}
			if len(data) > limit {
				err = fmt.Errorf("%s 超过 1 MiB", filepath.Base(filename))
				break
			}
			err = w.client.request(w.ctx, http.MethodPost, "/api/v1/memories", map[string]string{
				"content": string(data), "source": "import", "source_path": filepath.Base(filename),
			}, nil)
			if err != nil {
				break
			}
		}
		w.window.Update(func() {
			if len(files) > 0 {
				w.searchMemory()
			}
			if err != nil {
				w.memory.err = err.Error()
			}
		})
	}()
}
