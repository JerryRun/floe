package main

import (
	"github.com/egoist/mygo/ui"
	"time"
)

func desktopDialog(c *ui.Context, open *bool, title string, width float32, busy bool, body, actions func()) {
	windowWidth, windowHeight := c.Size()
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(30, 38, 42, .3))
		panel.Width(min(width, windowWidth-32)).Background(c.Theme().Background).
			Border(1, ui.Hex("#aeb5b9")).Shadow(0, 18, 55, 0, ui.RGBA(27, 37, 42, .24))
		ui.Column(c).FillWidth().AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Height(38).Padding(0, 10).AlignItems(ui.Center).Background(c.Theme().Surface).
				BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
				ui.Text(c, title).FontSize(14).Bold().SingleLine().Grow(1)
				if desktopIconButton(c, iconClose, "关闭", busy).Size(26, 25).Clicked() {
					*open = false
				}
			})
			ui.Scroll(c).MaxHeight(windowHeight - 113).Children(func() {
				ui.Column(c).FillWidth().Padding(14).Gap(10).AlignItems(ui.Stretch).Children(body)
			})
			ui.Row(c).Height(43).Padding(0, 10).Gap(7).AlignItems(ui.Center).Justify(ui.End).
				BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Background(c.Theme().Surface).Children(actions)
		})
		if busy || backdrop.Clicked() {
			*open = true
		}
	})
}

func desktopDialogButton(c *ui.Context, label string, primary, disabled bool) ui.Element {
	button := ui.Button(c, label).Height(27).MinWidth(66).Padding(0, 6).Radius(0).
		Border(1, c.Theme().Border).Background(c.Theme().Background).Disabled(disabled)
	if primary {
		button.Background(c.Theme().Accent).BorderColor(ui.Hex("#116a74")).TextColor(ui.Hex("#ffffff"))
	}
	return button
}

func desktopFieldLabel(c *ui.Context, label string) ui.Element {
	return ui.Text(c, label).FontSize(12).Height(19).TextColor(c.Theme().TextMuted).Margin(0, 0, 4)
}

func (w *desktopWorkspace) notify(message string, failed bool) {
	kind := "info"
	if failed {
		kind = "error"
	}
	w.notifications = append(w.notifications, ui.Toast{Title: message, Type: kind, Timeout: 3500 * time.Millisecond})
}

func (w *desktopWorkspace) showNotifications(c *ui.Context) {
	for _, toast := range w.notifications {
		c.AddToast(toast)
	}
	w.notifications = w.notifications[:0]
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(16).AlignItems(ui.End).Gap(6)
		for _, message := range toasts {
			toast := ui.ToastBase(c, message)
			toast.Root.Width(300).Padding(9, 11).Background(ui.Hex("#233b40")).Border(1, ui.Hex("#324d51")).
				Shadow(0, 7, 25, 0, ui.RGBA(20, 28, 31, .2))
			if message.Type == "error" {
				toast.Root.BorderWidth(1, 1, 1, 4).DrawOver(func(p *ui.Painter, r ui.Rect) {
					p.Fill(ui.Rect{X: r.X, Y: r.Y, W: 4, H: r.H}, c.Theme().Danger, 0)
				})
			}
			toast.Root.Children(func() {
				ui.Text(c, message.Title).TextColor(ui.Hex("#ffffff"))
			})
		}
	})
}
