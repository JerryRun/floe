package main

import (
	"fmt"
	"math"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"floe/internal/app"
	"floe/internal/core"
	"github.com/egoist/mygo"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

func (w *desktopWorkspace) previewDialog(c *ui.Context) {
	if w.previewPanel || !w.previewOpen || w.previewIndex < 0 || w.previewIndex >= len(w.previews) {
		return
	}
	item := w.previews[w.previewIndex]
	if item.window != nil {
		w.previewOpen = false
		return
	}
	width, height := c.Size()
	dialogWidth, dialogHeight := min(1100, width*.92), min(760, height*.88)
	dismissed := ui.DialogBase(c, &w.previewOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(30, 38, 42, .3))
		if w.previewMax {
			panel.Width(width).Height(height)
		} else {
			panel.Width(dialogWidth).Height(dialogHeight)
		}
		panel.Background(c.Theme().Background).Border(1, ui.Hex("#aeb5b9")).
			Shadow(0, 18, 55, 0, ui.RGBA(27, 37, 42, .24))
		ui.Column(c).Fill().MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			w.previewHeader(c, item, false)
			if w.previewOpen && w.previewIndex >= 0 && w.previews[w.previewIndex] == item {
				w.previewSurface(c, item)
			}
		})
	})
	if dismissed.Dismissed() {
		if item.video != nil {
			item.video.Pause()
		}
	}
}

func (w *desktopWorkspace) previewHeader(c *ui.Context, current *desktopPreview, docked bool) {
	background := ui.Hex("#e4e7e9")
	if docked {
		background = c.Theme().Surface
	}
	ui.Row(c).Height(30).FillWidth().AlignItems(ui.Stretch).Background(background).
		BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
		scroll := &w.previewTabScroll[0]
		if docked {
			scroll = &w.previewTabScroll[1]
		}
		delta := max(140, (scroll.MaxX+220)*.7)
		if desktopIconButton(c, iconPrevious, "上一个预览标签", scroll.X <= 1).Size(24, 29).Clicked() {
			scroll.X = max(0, scroll.X-delta)
		}
		ui.ScrollHorizontal(c).Key(docked).Width(0).Grow(1).MinWidth(0).Scrollbars(false).TrackScroll(scroll).Children(func() {
			ui.Row(c).Height(29).Gap(3).Padding(3, 4, 0).AlignItems(ui.Stretch).Children(func() {
				for index, item := range w.previews {
					active := index == w.previewIndex
					tab := ui.Row(c).Key(item).Height(27).Width(220).MinWidth(150).MaxWidth(260).
						Padding(0, 5, 0, 8).Gap(6).AlignItems(ui.Center).
						BorderWidth(1, 1, 0, 1).BorderColor(ui.Transparent)
					if active {
						theme := c.Theme()
						tab.Background(theme.Background).BorderColor(theme.Border)
						accent := theme.Accent
						tab.DrawOver(func(p *ui.Painter, bounds ui.Rect) {
							p.Fill(ui.Rect{X: bounds.X, Y: bounds.Y, W: bounds.W, H: 2}, accent, 0)
						})
					} else {
						tab.Background(ui.RGBA(255, 255, 255, .38))
						if tab.Hovered() {
							tab.BorderColor(c.Theme().Border).Background(c.Theme().SurfaceHover)
						}
					}
					if item == w.previewReveal {
						tab.ScrollIntoView()
						w.previewReveal = nil
					}
					tab.Children(func() {
						kind := ui.Text(c, previewKindName(item)).FontSize(10).Padding(2, 4).
							Radius(2).Background(c.Theme().Selection).TextColor(ui.Hex("#105c65"))
						if !active {
							kind.Background(c.Theme().Surface).TextColor(c.Theme().TextMuted)
						}
						name := item.name
						if item.code != nil && item.code.Buffer.Modified() {
							name += " •"
						}
						label := ui.Button(c, "").Label(item.name).Tooltip(item.path).Width(0).Grow(1).
							MinWidth(0).Height(26).Padding(0).Background(ui.Transparent).Border(0, ui.Transparent)
						label.Children(func() {
							ui.Text(c, name).FontSize(12).SingleLine().Grow(1).
								TextColor(map[bool]ui.Color{true: ui.Hex("#202529"), false: ui.Hex("#687178")}[active])
						})
						if label.Clicked() {
							w.selectPreview(index)
						}
						close := ui.Button(c, "").Label("关闭 "+item.name).Size(18, 18).Padding(0).
							Radius(3).Background(ui.Transparent).Border(0, ui.Transparent)
						if close.Hovered() {
							close.Background(ui.Hex("#cfd4d7"))
						}
						close.Children(func() {
							ui.Icon(c, iconClose).FontSize(14).TextColor(ui.Hex("#687178"))
						})
						if close.Clicked() {
							w.closePreview(item)
							if len(w.previews) == 0 {
								w.previewOpen = false
							}
						}
					})
				}
			})
		})
		if desktopIconButton(c, iconNext, "下一个预览标签", scroll.X >= scroll.MaxX-1).Size(24, 29).Clicked() {
			scroll.X = min(scroll.MaxX, scroll.X+delta)
		}
		ui.Row(c).Height(29).Padding(0, 6).AlignItems(ui.Center).Children(func() {
			refresh := desktopIconButton(c, iconRefresh, "从文件重新载入", current.busy)
			if docked {
				refresh.Size(29, 23).Radius(0).Background(c.Theme().Background).Border(1, c.Theme().Border)
			}
			if refresh.Clicked() {
				w.reloadPreview(current)
			}
			if docked {
				ui.Box(c).Width(4)
				button := ui.Button(c, "").Label("还原为窗口").Height(23).Padding(0, 6).Gap(3).
					Radius(0).Background(c.Theme().Background).Border(1, c.Theme().Border).Disabled(current.window != nil)
				button.Children(func() {
					ui.Icon(c, iconMaximize).FontSize(15).TextColor(c.Theme().TextMuted)
					ui.Text(c, "还原为窗口").FontSize(11).TextColor(c.Theme().TextMuted)
				})
				if button.Clicked() {
					w.previewPanel, w.previewOpen = false, true
					w.previewReveal = current
					current.imageViewSize = ui.Rect{}
					w.setQueueTab(queueActive)
					c.Invalidate()
				}
				return
			}
			if desktopIconButton(c, iconDown, "收到任务栏标签页", false).Clicked() {
				w.previewOpen, w.previewPanel = false, true
				w.previewReveal = current
				current.imageViewSize = ui.Rect{}
				w.setQueueTab(queuePreview)
				c.Invalidate()
			}
			label := "窗口内全屏"
			if w.previewMax {
				label = "还原窗口"
			}
			if desktopIconButton(c, iconMaximize, label, false).Clicked() {
				w.previewMax = !w.previewMax
			}
			if desktopIconButton(c, iconClose, "关闭全部预览", false).Clicked() {
				w.closeAllPreviews()
			}
		})
	})
}

func previewKindName(item *desktopPreview) string {
	switch item.kind {
	case "image":
		return "图片"
	case "video":
		if strings.EqualFold(path.Ext(item.name), ".m3u8") || strings.EqualFold(path.Ext(item.name), ".m3u") {
			return "M3U8"
		}
		return "MP4"
	case "pdf":
		return "PDF"
	default:
		return "文本"
	}
}

func (w *desktopWorkspace) previewSurface(c *ui.Context, item *desktopPreview) {
	if item.kind == "image" {
		if c.Shortcut(0, ui.KeyLeft) {
			w.stepPreviewImage(item, -1)
		} else if c.Shortcut(0, ui.KeyRight) {
			w.stepPreviewImage(item, 1)
		}
	}
	w.previewWindowContent(c, item)
	w.previewWindowFooter(c, item)
}

func (w *desktopWorkspace) previewWindowContent(c *ui.Context, item *desktopPreview) {
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Background(c.Theme().Background).
		Children(func() {
			switch item.kind {
			case "image":
				w.previewImageStage(c, item)
			case "video":
				w.previewVideoStage(c, item)
			case "pdf":
				w.previewPDFStage(c, item)
			default:
				w.previewTextStage(c, item)
			}
			if item.busy || item.err != "" {
				message := item.err
				color := c.Theme().Danger
				if item.busy {
					message, color = "正在读取文件…", c.Theme().TextMuted
				}
				if message != "" {
					ui.Row(c).Height(28).Padding(0, 10).AlignItems(ui.Center).
						BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).
						Children(func() { ui.Text(c, message).FontSize(11).TextColor(color).SingleLine() })
				}
			}
		})
}

func (w *desktopWorkspace) previewImageStage(c *ui.Context, item *desktopPreview) {
	stage := ui.Box(c).Grow(1).MinHeight(0).Clip().Background(ui.Hex("#292f32"))
	stage.Draw(func(p *ui.Painter, bounds ui.Rect) {
		const square = float32(10)
		dark := ui.Hex("#24292c")
		for y, row := float32(0), 0; y < bounds.H; y, row = y+square, row+1 {
			for x := float32(row%2) * square; x < bounds.W; x += 2 * square {
				p.Fill(ui.Rect{X: bounds.X + x, Y: bounds.Y + y, W: min(square, bounds.W-x), H: min(square, bounds.H-y)}, dark, 0)
			}
		}
	})
	bounds := stage.Bounds()
	imageWidth, imageHeight := ui.ImageSize(item.image)
	if imageWidth > 0 && imageHeight > 0 && bounds.W > 0 && bounds.H > 0 &&
		(bounds.W != item.imageViewSize.W || bounds.H != item.imageViewSize.H) {
		item.scale = float64(max(.1, min(1, (bounds.W-40)/imageWidth, (bounds.H-40)/imageHeight)))
		item.imageViewSize = bounds
	}
	stage.Children(func() {
		if item.image == nil {
			if !item.busy && item.err == "" {
				ui.Column(c).Fill().Center().Children(func() {
					ui.Text(c, "图片无法读取").TextColor(ui.Hex("#e7ebed"))
				})
			}
			return
		}
		padding := float32(20)
		if w.previewPanel {
			padding = 8
		}
		width, height := imageWidth*float32(item.scale), imageHeight*float32(item.scale)
		ui.ScrollBoth(c).Key(item).Fill().Children(func() {
			ui.Column(c).Size(max(bounds.W, width+2*padding), max(bounds.H, height+2*padding)).
				Shrink(0).Center().Children(func() {
				ui.Image(c, item.image).Size(width, height).Shrink(0).Fit(ui.Contain).
					Shadow(0, 3, 24, 0, ui.RGBA(0, 0, 0, .35))
			})
		})
	})
}

func (w *desktopWorkspace) previewPDFStage(c *ui.Context, item *desktopPreview) {
	pdf := item.pdf
	stage := ui.ScrollHorizontal(c).Key(pdf).Grow(1).MinHeight(0).Background(ui.Hex("#d6dadd"))
	if pdf == nil || len(pdf.sizes) == 0 {
		return
	}
	stage.TrackScroll(&pdf.horizontal)
	viewportWidth := stage.Bounds().W
	if viewportWidth <= 0 {
		width, _ := c.Size()
		viewportWidth = min(1100, width*.92)
		c.Invalidate()
	}
	pageWidth := max(1, viewportWidth-40) * float32(item.scale)
	stage.Children(func() {
		first, last := pdf.visiblePages()
		w.renderPDFPages(item, first, last)
		ui.List(c, &pdf.list, len(pdf.sizes), func(page int) {
			size := pdf.sizes[page]
			height := pageWidth * max(1, size.Height) / max(1, size.Width)
			ui.Row(c).FillWidth().Height(height+9).Padding(0, 0, 9).Justify(ui.Center).Children(func() {
				ui.Column(c).Size(pageWidth, height).Shrink(0).Center().Background(ui.Hex("#ffffff")).Children(func() {
					if bitmap := pdf.images[page]; bitmap != nil {
						ui.Image(c, bitmap).Fill().Fit(ui.Contain)
					} else if pdf.err != nil {
						ui.Text(c, pdf.err.Error()).FontSize(12).TextColor(c.Theme().Danger)
					} else {
						ui.Spinner(c).Size(16, 16).Label("正在加载页面")
					}
				})
			})
		}).Label("PDF 页面").Width(max(viewportWidth, pageWidth+40)).FillHeight().KeepScrollbars(true)
	})
}

func (w *desktopWorkspace) previewVideoStage(c *ui.Context, item *desktopPreview) {
	stage := ui.Column(c).Grow(1).MinHeight(0).Center().Background(ui.Hex("#111416"))
	stage.Children(func() {
		if item.video != nil {
			ui.Video(c, item.video).Fill().Fit(ui.Contain)
		} else if !item.busy {
			ui.Text(c, "视频无法播放").TextColor(ui.Hex("#e7ebed"))
		}
	})
}

func (w *desktopWorkspace) previewTextStage(c *ui.Context, item *desktopPreview) {
	if item.code == nil {
		ui.Column(c).Grow(1).MinHeight(0).Background(ui.Hex("#fbfcfc"))
		return
	}
	if item.render {
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(0).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).
				BorderWidth(0, 1, 0, 0).BorderColor(ui.Hex("#bfc7ca")).Children(func() {
				editor.View(c, item.code).Grow(1).MinHeight(0).Disabled(item.busy)
			})
			ui.Column(c).Width(0).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).
				Children(func() { w.markdownDocument(c, item) })
		})
		return
	}

	ui.Column(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Background(ui.Hex("#fbfcfc")).Children(func() {
		editor.View(c, item.code).Grow(1).FillWidth().Disabled(item.busy)
	})
}

func (w *desktopWorkspace) markdownDocument(c *ui.Context, item *desktopPreview) {
	options := &item.markdown
	if options.Theme == nil {
		options.Theme = &markdown.Theme{
			FontSize: 14, Font: "Segoe UI", CodeFont: "Cascadia Code", LineHeight: 1.72,
			Text: ui.Hex("#273137"), Muted: ui.Hex("#58666d"), Link: ui.Hex("#126f7b"),
			Code: ui.Hex("#934530"), CodeBackground: ui.Hex("#edf1f2"),
			CodeBlockBackground: ui.Hex("#f5f7f8"), CodeBlockBorder: ui.Hex("#dce2e4"),
			CodeBlockPadding: .9, CodeBlockRadius: 0, QuoteBar: ui.Hex("#86b8be"),
			QuoteText: ui.Hex("#58666d"), TableBorder: ui.Hex("#d8dfe2"),
			TableHeader: ui.Hex("#f1f5f6"), Rule: ui.Hex("#d8dfe2"),
			Headings: [6]markdown.HeadingStyle{
				{Size: 2, Weight: 650, Color: ui.Hex("#17262c"), Top: 1.55, Bottom: .65, Rule: true},
				{Size: 1.5, Weight: 650, Color: ui.Hex("#17262c"), Top: 1.55, Bottom: .65, Rule: true},
				{Size: 1.22, Weight: 650, Color: ui.Hex("#17262c"), Top: 1.55, Bottom: .65},
				{Size: 1.05, Weight: 650, Color: ui.Hex("#17262c"), Top: 1.55, Bottom: .65},
			},
		}
		options.LoadImage = func(source string) (ui.ImageSource, error) {
			if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") {
				return nil, fmt.Errorf("远程图片无法在离线预览中加载")
			}
			resource, err := url.Parse(source)
			if err != nil {
				return nil, err
			}
			target := resource.Path
			if !strings.HasPrefix(target, "/") {
				target = path.Join(path.Dir(item.path), target)
			}
			query := url.Values{"provider": {item.provider}, "path": {path.Clean(target)}}.Encode()
			data, err := w.client.readBytes(w.ctx, "/api/v1/files/raw?"+query, 40<<20)
			if err != nil {
				return nil, err
			}
			if strings.EqualFold(path.Ext(target), ".svg") {
				return ui.ParseSVG(data)
			}
			return ui.DecodeBitmap(data)
		}
		options.OnLink = func(destination string) {
			if strings.HasPrefix(destination, "#") {
				return
			}
			if strings.HasPrefix(destination, "https://") || strings.HasPrefix(destination, "http://") || strings.HasPrefix(destination, "mailto:") {
				_ = app.OpenBrowser(destination)
				return
			}
			resource, err := url.Parse(destination)
			if err != nil {
				return
			}
			target := resource.Path
			if !strings.HasPrefix(target, "/") {
				target = path.Join(path.Dir(item.path), target)
			}
			target = path.Clean(target)
			w.openPreview(&desktopPanel{location: desktopLocation{item.provider, path.Dir(target)}}, core.Entry{Name: path.Base(target), Path: target})
		}
	}
	viewport := ui.Scroll(c).Grow(1).MinHeight(0).Background(ui.Hex("#eef1f2"))
	bounds := viewport.Bounds()
	viewport.Children(func() {
		ui.Row(c).FillWidth().Padding(18).Justify(ui.Center).Children(func() {
			ui.Column(c).Width(min(760, max(0, bounds.W-36))).MinHeight(max(0, bounds.H-36)).Padding(30, 38, 56).
				Border(1, ui.Hex("#dde2e4")).Background(ui.Hex("#ffffff")).
				Shadow(0, 2, 9, 0, ui.RGBA(37, 48, 54, .06)).Children(func() {
				markdown.View(c, item.code.Buffer.String(), options)
			})
		})
	})
}

func (w *desktopWorkspace) previewWindowFooter(c *ui.Context, item *desktopPreview) {
	ui.Row(c).Height(43).FillWidth().Padding(0, 8, 0, 10).Gap(10).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Background(c.Theme().Surface).
		Children(func() {
			ui.Text(c, item.path).Width(0).Grow(1).MinWidth(0).SingleLine().
				FontSize(12).FontWeight(600).Tooltip(item.path)
			switch item.kind {
			case "image":
				count := len(item.imageEntries)
				ui.Text(c, fmt.Sprintf("%d / %d　%s", item.imageIndex+1, count, item.name)).
					FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
				ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
					if desktopIconButton(c, iconPrevious, "上一张", count < 2 || item.busy).Clicked() {
						w.stepPreviewImage(item, -1)
					}
					if desktopIconButton(c, iconZoomOut, "缩小", item.image == nil).Clicked() {
						item.scale = max(.1, item.scale/1.2)
					}
					previewScaleLabel(c, item.scale)
					if desktopIconButton(c, iconZoomIn, "放大", item.image == nil).Clicked() {
						item.scale = min(8, item.scale*1.2)
					}
					if previewFooterButton(c, "1:1").Width(38).Font("Cascadia Code").
						Disabled(item.image == nil).Clicked() {
						item.scale = 1
					}
					if desktopIconButton(c, iconNext, "下一张", count < 2 || item.busy).Clicked() {
						w.stepPreviewImage(item, 1)
					}
				})
			case "video":
				status := "正在加载"
				if item.video != nil {
					status = "MP4"
					if strings.EqualFold(path.Ext(item.name), ".m3u8") || strings.EqualFold(path.Ext(item.name), ".m3u") {
						status = "HLS · 已就绪"
					}
					position := item.video.Position().Seconds()
					duration := item.video.Duration().Seconds()
					if duration > 0 {
						status = fmt.Sprintf("%s · %s / %s", status, previewTime(position), previewTime(duration))
						seek := position
						if ui.Slider(c, &seek, 0, duration).Width(160).Label("播放位置").Changed() {
							item.video.Seek(time.Duration(seek * float64(time.Second)))
						}
					} else if videoErr := item.video.Err(); videoErr != nil {
						status += " · " + videoErr.Error()
					} else if width, _ := item.video.Size(); width == 0 {
						status += " · 正在加载"
					} else {
						status += " · 直播"
					}
				}
				if item.video != nil {
					action := iconPause
					label := "暂停"
					if !item.video.Playing() {
						action, label = iconPlay, "播放"
					}
					if desktopIconButton(c, action, label, false).Size(25, 25).Clicked() {
						if item.video.Playing() {
							item.video.Pause()
						} else {
							item.video.Play()
						}
					}
				}
				ui.Text(c, status).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine().Grow(1)
			case "text", ".md", ".markdown":
				w.previewEditorFooter(c, item)
			case "pdf":
				w.previewPDFFooter(c, item)
			}
		})
}

func previewFooterButton(c *ui.Context, label string) ui.Element {
	button := ui.Button(c, label).Height(27).Padding(0, 6).FontSize(11).
		Background(ui.Transparent).Border(1, ui.Transparent)
	if button.Hovered() {
		button.Background(c.Theme().Background).Border(1, c.Theme().Border)
	}
	return button
}

func previewScaleLabel(c *ui.Context, scale float64) {
	ui.Text(c, fmt.Sprintf("%d%%", int(math.Round(scale*100)))).Width(44).
		TextAlign(ui.Center).Font("Cascadia Code").FontSize(11).TextColor(c.Theme().TextMuted)
}

func (w *desktopWorkspace) previewEditorFooter(c *ui.Context, item *desktopPreview) {
	if item.code == nil {
		return
	}
	position := item.code.CursorPos()
	dirty := item.code.Buffer.Modified()
	state := "未修改"
	if dirty {
		state = "已修改"
	}
	if previewFooterButton(c, fmt.Sprintf("Ln %d, Col %d", position.Line+1, position.Col+1)).
		Font("Cascadia Code").Clicked() {
		item.code.Run(editor.GoToLine)
	}
	for _, value := range []string{
		strings.ToUpper(item.document.Encoding) + " · " + strings.ToUpper(item.document.Newline) + " · " + desktopSize(int64(item.code.Buffer.Len())),
		state,
	} {
		ui.Text(c, value).FontSize(11).TextColor(c.Theme().TextMuted).Padding(2, 0, 2, 7).
			BorderWidth(0, 0, 0, 1).BorderColor(c.Theme().Border).SingleLine()
	}
	ui.Box(c).Grow(1)
	if item.kind != "text" {
		toggle := previewFooterButton(c, "预览").MinWidth(62).AccessSelected(item.render).Border(1, c.Theme().Border)
		if item.render {
			toggle.Background(c.Theme().Selection).TextColor(ui.Hex("#105c65")).Border(1, ui.Hex("#89aeb3"))
		}
		if toggle.Clicked() {
			item.render = !item.render
		}
	}
	if desktopIconButton(c, iconSearch, "查找", false).Clicked() {
		item.code.Run(editor.Find)
	}
	if ui.Select(c, &item.syntaxMode, []string{"自动识别", "纯文本", "JSON", "YAML", "Markdown", "JavaScript / TypeScript", "Python", "Go", "Rust", "Shell / PowerShell", "HTML / XML", "CSS", "SQL", "C / C++"}).
		Width(134).Height(27).FontSize(11).Label("语法高亮").Changed() {
		extensions := map[string]string{"JSON": ".json", "YAML": ".yaml", "Markdown": ".md", "JavaScript / TypeScript": ".js", "Python": ".py", "Go": ".go", "Rust": ".rs", "Shell / PowerShell": ".sh", "HTML / XML": ".html", "CSS": ".css", "SQL": ".sql", "C / C++": ".cpp"}
		name := "file" + extensions[item.syntaxMode]
		if item.syntaxMode == "自动识别" {
			name = item.name
		}
		item.code.Buffer.SetLanguage(editor.LanguageFor(name))
	}
	if ui.PrimaryButton(c, "保存").Height(27).MinWidth(66).FontSize(11).
		Disabled(item.busy || !dirty).Clicked() || c.Shortcut(ui.Ctrl, ui.KeyS) {
		w.savePreview(item)
	}
}

func (w *desktopWorkspace) previewPDFFooter(c *ui.Context, item *desktopPreview) {
	pdf := item.pdf
	if pdf == nil || len(pdf.sizes) == 0 {
		return
	}
	count := len(pdf.sizes)
	ui.Text(c, fmt.Sprintf("%d 页", count)).FontSize(11).TextColor(c.Theme().TextMuted)
	if desktopIconButton(c, iconPrevious, "上一页", pdf.current == 0).Clicked() {
		pdf.goTo(pdf.current - 1)
		c.Invalidate()
	}
	if ui.TextInput(c, &pdf.pageInput).Size(48, 24).FontSize(11).Label("页码").Submitted() {
		page, err := strconv.Atoi(strings.TrimSpace(pdf.pageInput))
		if err == nil && page >= 1 && page <= count {
			pdf.goTo(page - 1)
			c.Invalidate()
		} else {
			pdf.pageInput = strconv.Itoa(pdf.current + 1)
		}
	}
	ui.Text(c, fmt.Sprintf("/ %d", count)).FontSize(11).TextColor(c.Theme().TextMuted)
	if desktopIconButton(c, iconNext, "下一页", pdf.current+1 >= count).Clicked() {
		pdf.goTo(pdf.current + 1)
		c.Invalidate()
	}
	if desktopIconButton(c, iconZoomOut, "缩小", false).Clicked() {
		item.scale = max(.1, item.scale/1.2)
		pdf.goTo(pdf.current)
		c.Invalidate()
	}
	previewScaleLabel(c, item.scale)
	if desktopIconButton(c, iconZoomIn, "放大", false).Clicked() {
		item.scale = min(8, item.scale*1.2)
		pdf.goTo(pdf.current)
		c.Invalidate()
	}
	if previewFooterButton(c, "适应宽度").Clicked() {
		item.scale = 1
		pdf.horizontal.X = 0
		pdf.goTo(pdf.current)
		c.Invalidate()
	}
}

func previewTime(seconds float64) string {
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return "--:--"
	}
	value := int(seconds)
	return fmt.Sprintf("%02d:%02d", value/60, value%60)
}

func (w *desktopWorkspace) stepPreviewImage(item *desktopPreview, delta int) {
	if item.busy || len(item.imageEntries) < 2 {
		return
	}
	item.imageIndex = (item.imageIndex + delta + len(item.imageEntries)) % len(item.imageEntries)
	entry := item.imageEntries[item.imageIndex]
	item.path, item.name = entry.Path, entry.Name
	w.loadPreview(item)
}

func (w *desktopWorkspace) closeAllPreviews() {
	if slices.ContainsFunc(w.previews, func(item *desktopPreview) bool {
		return item.code != nil && item.code.Buffer.Modified()
	}) {
		go func() {
			choice, err := w.ask(mygo.MessageOptions{
				Type: mygo.MessageWarning, Message: "打开的文件有未保存的修改，确定全部关闭？",
				Buttons: []string{"取消", "关闭全部"}, DestructiveButtons: []int{1},
			})
			if err == nil && choice == 1 {
				w.window.Update(w.finishCloseAllPreviews)
			}
		}()
		return
	}
	w.finishCloseAllPreviews()
}

func (w *desktopWorkspace) finishCloseAllPreviews() {
	for _, item := range w.previews {
		item.closeResources()
		if item.window != nil {
			item.window.Close()
		}
	}
	w.previews, w.previewIndex, w.previewReveal = nil, -1, nil
	if w.queueFilter == queuePreview {
		w.setQueueTab(queueActive)
	}
	w.previewOpen = false
	w.previewPanel = false
	w.previewMax = false
}
