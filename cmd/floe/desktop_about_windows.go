package main

import (
	"net/url"

	"floe/docs/releases"
	"floe/internal/app"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

const floeGitHubURL = "https://github.com/JerryRun/floe"

// Set by build-windows.sh to the release this build is based on.
var releaseTag string

func (t *nativeTray) showAbout() {
	if t.about != nil {
		if t.about.IsMinimized() {
			t.about.Restore()
		}
		t.about.Show()
		t.about.Focus()
		return
	}

	version := app.Version
	if releaseTag != "" && releaseTag == "v"+version {
		version = releaseTag
	}
	notes := releases.Notes(releaseTag)
	notesTitle := "Release Notes"
	releaseURL := floeGitHubURL + "/releases"
	if releaseTag != "" {
		notesTitle += " · " + releaseTag
		releaseURL += "/tag/" + url.PathEscape(releaseTag)
	}
	icon, _ := ui.DecodeBitmap(desktopBrandPNG)
	options := &markdown.Options{}
	t.about = mygo.NewWindow(mygo.WindowOptions{
		Title: "关于 Floe", Width: 640, Height: 545, MinWidth: 440, MinHeight: 320,
		Content: ui.View(func(c *ui.Context) {
			c.SetTheme(desktopTheme)
			ui.Column(c).Fill().AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Padding(18, 20).Gap(12).AlignItems(ui.Center).
					BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
					ui.Image(c, icon).Size(44, 44)
					ui.Column(c).Gap(4).Children(func() {
						ui.Text(c, "Floe").FontSize(21).Bold()
						ui.Text(c, "远程文件工作区 · Jerry").FontSize(12).TextColor(c.Theme().TextMuted)
					})
					ui.Box(c).Grow(1)
					ui.Text(c, "版本 "+version).Font("Cascadia Code").FontSize(12)
				})
				ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
					ui.Column(c).FillWidth().Padding(16, 20).Gap(10).Children(func() {
						ui.Text(c, notesTitle).FontSize(14).Bold()
						if notes != "" {
							markdown.View(c, notes, options)
						} else {
							ui.Text(c, "查看 GitHub 上的发布说明。").TextColor(c.Theme().TextMuted)
						}
					})
				})
				ui.Row(c).Padding(12, 20).Gap(12).AlignItems(ui.Center).Background(c.Theme().Surface).
					BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
					ui.Link(c, "GitHub · JerryRun/floe", floeGitHubURL)
					ui.Link(c, "在线发布说明", releaseURL)
					ui.Box(c).Grow(1)
					if ui.Button(c, "关闭").Clicked() || c.Shortcut(0, ui.KeyEscape) {
						t.about.Close()
					}
				})
			})
		}),
	})
	_ = t.about.SetIcon(desktopBrandPNG)
	t.about.OnClosed(func() { t.about = nil })
	t.about.Focus()
}
