package main

import (
	"embed"

	"github.com/egoist/mygo/ui"
)

//go:embed assets/material/*.svg
var materialIcons embed.FS

//go:embed assets/floe-64.png
var desktopBrandPNG []byte

var (
	iconEdit     = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path d="M4 17.25V20h2.75L17.8 8.95l-2.75-2.75L4 17.25Zm15.7-9.3c.4-.4.4-1.05 0-1.45l-2.2-2.2a1.03 1.03 0 0 0-1.45 0l-1.35 1.35 2.75 2.75Z"/></svg>`))
	iconPlay     = materialIcon("play_arrow")
	iconPause    = materialIcon("pause")
	iconZoomIn   = materialIcon("zoom_in")
	iconZoomOut  = materialIcon("zoom_out")
	iconPrevious = materialIcon("chevron_left")
	iconAdd      = materialIcon("add")
	iconDown     = materialIcon("expand_more")
	iconNext     = materialIcon("chevron_right")
	iconStar     = materialIcon("star_border")
	iconStarFill = materialIcon("star")
	iconCloud    = materialIcon("cloud")
	iconComputer = materialIcon("computer")
	iconUpload   = materialIcon("upload")
	iconDownload = materialIcon("download")
	iconClose    = materialIcon("close")
	iconDelete   = materialIcon("delete")
	iconGrid     = materialIcon("grid_view")
	iconList     = materialIcon("view_list")
	iconSearch   = materialIcon("search")
	iconDrive    = materialIcon("hard_drive")
	iconImage    = materialIcon("image")
	iconMovie    = materialIcon("movie")
	iconArchive  = materialIcon("archive")
	iconCode     = materialIcon("code")
	iconTerminal = materialIcon("terminal")
	iconSwap     = materialIcon("swap_horiz")
	iconMaximize = materialIcon("fullscreen")
	iconFile     = materialIcon("description")
	iconFolder   = materialIcon("folder")
	iconRefresh  = materialIcon("refresh")
	iconUp       = materialIcon("arrow_upward")
)

func materialIcon(name string) *ui.SVG {
	data, err := materialIcons.ReadFile("assets/material/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(data)
}

func desktopIconButton(c *ui.Context, icon *ui.SVG, label string, disabled bool) ui.Element {
	return ui.Button(c, "").Label(label).Tooltip(label).Size(27, 27).Padding(0).Radius(2).Background(ui.Transparent).Border(0, ui.Transparent).Disabled(disabled).Children(func() {
		ui.Icon(c, icon).FontSize(17).TextColor(ui.Hex("#35464e"))
	})
}
