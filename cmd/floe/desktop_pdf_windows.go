package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"
)

// Windows.Data.Pdf renders a page into a native bitmap. The document never
// passes through a browser engine.
const renderPDFPageScript = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$null = [Windows.Storage.StorageFile,Windows.Storage,ContentType=WindowsRuntime]
$null = [Windows.Data.Pdf.PdfDocument,Windows.Data.Pdf,ContentType=WindowsRuntime]
$null = [Windows.Data.Pdf.PdfPageRenderOptions,Windows.Data.Pdf,ContentType=WindowsRuntime]
$null = [Windows.Storage.Streams.InMemoryRandomAccessStream,Windows.Storage.Streams,ContentType=WindowsRuntime]
$null = [Windows.Storage.Streams.DataReader,Windows.Storage.Streams,ContentType=WindowsRuntime]
$methods = [System.WindowsRuntimeSystemExtensions].GetMethods()
$operation = $methods | Where-Object {
    $_.Name -eq 'AsTask' -and $_.IsGenericMethod -and $_.GetGenericArguments().Length -eq 1 -and
    $_.GetParameters().Length -eq 1 -and $_.GetParameters()[0].ParameterType.Name.StartsWith('IAsyncOperation')
} | Select-Object -First 1
$action = $methods | Where-Object {
    $_.Name -eq 'AsTask' -and -not $_.IsGenericMethod -and
    $_.GetParameters().Length -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncAction'
} | Select-Object -First 1
function Await-Result($pending, [Type]$type) {
    $task = $operation.MakeGenericMethod($type).Invoke($null, @($pending))
    $task.GetAwaiter().GetResult()
}
$file = Await-Result ([Windows.Storage.StorageFile]::GetFileFromPathAsync($env:FLOE_PDF_SOURCE)) ([Windows.Storage.StorageFile])
$document = Await-Result ([Windows.Data.Pdf.PdfDocument]::LoadFromFileAsync($file)) ([Windows.Data.Pdf.PdfDocument])
if ($env:FLOE_PDF_LAYOUT -eq 'true') {
    $sizes = @(
        for ($i = 0; $i -lt $document.PageCount; $i++) {
            $page = $document.GetPage([uint32]$i)
            try { @{ width = $page.Size.Width; height = $page.Size.Height } }
            finally { $page.Dispose() }
        }
    )
    ConvertTo-Json -InputObject $sizes -Compress
}
foreach ($number in $env:FLOE_PDF_PAGES.Split(',')) {
    $index = [uint32]$number
    if ($index -ge $document.PageCount) { continue }
    $page = $document.GetPage($index)
    $stream = New-Object Windows.Storage.Streams.InMemoryRandomAccessStream
    try {
        $options = New-Object Windows.Data.Pdf.PdfPageRenderOptions
        $width = [Math]::Max(1, $page.Size.Width)
        $height = [Math]::Max(1, $page.Size.Height)
        $scale = 1500 / [Math]::Max($width, $height)
        $options.DestinationWidth = [uint32][Math]::Max(1, [Math]::Round($width * $scale))
        $options.DestinationHeight = [uint32][Math]::Max(1, [Math]::Round($height * $scale))
        $task = $action.Invoke($null, @($page.RenderToStreamAsync($stream, $options)))
        $null = $task.GetAwaiter().GetResult()
        $reader = New-Object Windows.Storage.Streams.DataReader($stream.GetInputStreamAt(0))
        try {
            $null = Await-Result ($reader.LoadAsync([uint32]$stream.Size)) ([uint32])
            $bytes = New-Object byte[] ([int]$stream.Size)
            $reader.ReadBytes($bytes)
            [IO.File]::WriteAllBytes((Join-Path $env:FLOE_PDF_OUTPUT "$index.png"), $bytes)
        } finally { $reader.Dispose() }
    } finally { $stream.Dispose(); $page.Dispose() }
}
`

type desktopPDFPageSize struct {
	Width, Height float32
}

func renderDesktopPDF(ctx context.Context, data []byte, pages []int, layout bool) ([]desktopPDFPageSize, map[int]ui.ImageSource, error) {
	temp, err := os.MkdirTemp("", "floe-pdf-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(temp)
	source := filepath.Join(temp, "document.pdf")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		return nil, nil, err
	}
	utf := utf16.Encode([]rune(renderPDFPageScript))
	encoded := make([]byte, len(utf)*2)
	for i, value := range utf {
		encoded[i*2] = byte(value)
		encoded[i*2+1] = byte(value >> 8)
	}
	numbers := make([]string, len(pages))
	for i, page := range pages {
		numbers[i] = strconv.Itoa(page)
	}
	powerShell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	command := exec.CommandContext(ctx, powerShell, "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded))
	command.Env = append(os.Environ(), "FLOE_PDF_SOURCE="+source, "FLOE_PDF_OUTPUT="+temp,
		"FLOE_PDF_LAYOUT="+strconv.FormatBool(layout), "FLOE_PDF_PAGES="+strings.Join(numbers, ","))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	result, err := command.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("Windows PDF 预览失败: %w", err)
	}
	var sizes []desktopPDFPageSize
	if layout {
		if err := json.Unmarshal(result, &sizes); err != nil {
			return nil, nil, fmt.Errorf("读取 PDF 页面尺寸: %w", err)
		}
		if len(sizes) == 0 {
			return nil, nil, fmt.Errorf("PDF 没有可预览的页面")
		}
	}
	images := make(map[int]ui.ImageSource, len(pages))
	for _, page := range pages {
		if layout && page >= len(sizes) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(temp, strconv.Itoa(page)+".png"))
		if err != nil {
			return nil, nil, err
		}
		bitmap, err := ui.DecodeBitmap(data)
		if err != nil {
			return nil, nil, err
		}
		images[page] = bitmap
	}
	return sizes, images, nil
}

type desktopPDF struct {
	ctx                       context.Context
	cancel                    context.CancelFunc
	data                      []byte
	sizes                     []desktopPDFPageSize
	images                    map[int]ui.ImageSource
	list                      ui.ListState
	horizontal                ui.ScrollState
	current                   int
	pageInput                 string
	jump, settling            bool
	visibleFirst, visibleLast int
	rendering                 bool
	err                       error
}

func (w *desktopWorkspace) loadPDF(item *desktopPreview, query string) {
	page := 0
	if item.pdf != nil {
		page = item.pdf.current
		item.pdf.cancel()
	}
	ctx, cancel := context.WithCancel(w.ctx)
	pdf := &desktopPDF{ctx: ctx, cancel: cancel}
	item.pdf = pdf
	go func() {
		data, err := w.client.readBytes(ctx, "/api/v1/files/pdf?"+query, 60<<20)
		var sizes []desktopPDFPageSize
		var images map[int]ui.ImageSource
		if err == nil {
			sizes, images, err = renderDesktopPDF(ctx, data, []int{page, page + 1}, true)
		}
		if ctx.Err() != nil {
			return
		}
		w.window.Update(func() {
			if ctx.Err() != nil {
				return
			}
			item.busy = false
			if err != nil {
				item.err = err.Error()
			} else {
				pdf.data, pdf.sizes, pdf.images = data, sizes, images
				pdf.goTo(min(page, len(sizes)-1))
			}
			if item.window != nil {
				item.window.Invalidate()
			}
		})
	}()
}

func (pdf *desktopPDF) goTo(page int) {
	pdf.current = page
	pdf.pageInput = strconv.Itoa(page + 1)
	pdf.jump = true
}

func (pdf *desktopPDF) setCurrent(page int) {
	if page != pdf.current {
		pdf.current = page
		pdf.pageInput = strconv.Itoa(page + 1)
	}
}

func (pdf *desktopPDF) visiblePages() (int, int) {
	first, last := pdf.list.Visible()
	if pdf.jump {
		pdf.list.ScrollTo(pdf.current, ui.Start)
		pdf.jump, pdf.settling = false, true
		return pdf.current, pdf.current
	}
	if last < first {
		return pdf.current, pdf.current
	}
	changed := first != pdf.visibleFirst || last != pdf.visibleLast
	pdf.visibleFirst, pdf.visibleLast = first, last
	// A short final page may not scroll all the way to the top.
	if pdf.settling && pdf.current >= first && pdf.current <= last {
		pdf.settling = false
	} else if changed && !pdf.settling {
		page := first
		if first > 0 && pdf.list.AtEnd() {
			page = last
		}
		pdf.setCurrent(page)
	}
	return first, last
}

func (w *desktopWorkspace) renderPDFPages(item *desktopPreview, first, last int) {
	pdf := item.pdf
	if pdf.rendering || pdf.err != nil {
		return
	}
	var pages []int
	for page := first; page <= min(len(pdf.sizes)-1, last+2) && len(pages) < 4; page++ {
		if pdf.images[page] == nil {
			pages = append(pages, page)
		}
	}
	if first > 0 && len(pages) < 4 && pdf.images[first-1] == nil {
		pages = append(pages, first-1)
	}
	if len(pages) == 0 {
		return
	}
	pdf.rendering = true
	go func() {
		_, images, err := renderDesktopPDF(pdf.ctx, pdf.data, pages, false)
		if pdf.ctx.Err() != nil {
			return
		}
		w.window.Update(func() {
			if pdf.ctx.Err() != nil {
				return
			}
			pdf.rendering, pdf.err = false, err
			for page, bitmap := range images {
				pdf.images[page] = bitmap
			}
			visibleFirst, visibleLast := pdf.list.Visible()
			cacheSize := max(12, visibleLast-visibleFirst+5)
			if len(pdf.images) > cacheSize {
				keys := make([]int, 0, len(pdf.images))
				for page := range pdf.images {
					keys = append(keys, page)
				}
				distance := func(page int) int {
					return max(0, visibleFirst-page, page-visibleLast)
				}
				slices.SortFunc(keys, func(a, b int) int { return distance(a) - distance(b) })
				for _, page := range keys[cacheSize:] {
					delete(pdf.images, page)
				}
			}
			if item.window != nil {
				item.window.Invalidate()
			}
		})
	}()
}
