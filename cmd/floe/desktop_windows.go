package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"floe/internal/app"
	"floe/internal/core"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type desktopWindow struct {
	url       string
	statePath string
	workspace *desktopWorkspace
	state     desktopSavedWorkspace
}

type desktopSavedWorkspace struct {
	Tabs        [2][]desktopSavedTab `json:"tabs"`
	Active      [2]int               `json:"active"`
	Grid        [2]bool              `json:"grid,omitempty"`
	QueueHeight float32              `json:"queue_height,omitempty"`
}

type desktopSavedTab struct {
	Provider    string `json:"provider"`
	Path        string `json:"path"`
	DisplayPath string `json:"display_path,omitempty"`
}

func newDesktopWindow(url, dataDir string) *desktopWindow {
	return &desktopWindow{url: url, statePath: filepath.Join(dataDir, "desktop-workspace.json")}
}

func (d *desktopWindow) restoreState() {
	data, err := os.ReadFile(d.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		var state desktopSavedWorkspace
		err = json.Unmarshal(data, &state)
		if err == nil {
			d.state = state
		}
	}
	if err != nil {
		log.Printf("load desktop workspace: %v", err)
	}
}

func (d *desktopWindow) saveState() {
	data, err := json.MarshalIndent(d.state, "", "  ")
	if err == nil {
		err = os.WriteFile(d.statePath+".tmp", data, 0o600)
	}
	if err == nil {
		err = os.Rename(d.statePath+".tmp", d.statePath)
	}
	if err != nil {
		log.Printf("save desktop workspace: %v", err)
	}
}

func (d *desktopWindow) open() {
	if d.workspace != nil {
		window := d.workspace.window
		if window.IsMinimized() {
			window.Restore()
		}
		window.Show()
		window.Focus()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &desktopWorkspace{
		client: newDesktopClient(d.url),
		ctx:    ctx, dialogs: make(chan struct{}, 1),
		queueHeight: 160, previewIndex: -1,
	}
	d.restoreState()
	w.gridView = d.state.Grid
	if d.state.QueueHeight >= 90 {
		w.queueHeight = d.state.QueueHeight
	}
	for side, tabs := range d.state.Tabs {
		for _, tab := range tabs {
			if tab.Provider == "" || !strings.HasPrefix(tab.Path, "/") {
				continue
			}
			panel := newDesktopPanel(desktopLocation{tab.Provider, tab.Path})
			panel.displayPath = tab.DisplayPath
			w.tabs[side] = append(w.tabs[side], panel)
		}
		if len(w.tabs[side]) == 0 {
			w.tabs[side] = append(w.tabs[side], newDesktopPanel(desktopLocation{"local", "/"}))
		}
		w.active[side] = min(max(0, d.state.Active[side]), len(w.tabs[side])-1)
	}
	width, height := 1180, 780
	workArea := mygo.Screen.PrimaryDisplay().WorkArea
	if workArea.Width > 0 && workArea.Height > 0 {
		width = min(width, workArea.Width)
		height = min(height, workArea.Height)
	}
	w.window = mygo.NewWindow(mygo.WindowOptions{
		Title: "Floe · 文件工作区", Width: width, Height: height,
		MinWidth: min(860, width), MinHeight: min(480, height),
		Maximized: true, Content: ui.View(w.view),
	})
	d.workspace = w
	_ = w.window.SetIcon(desktopBrandPNG)
	closing, discardEdits := false, false
	w.window.OnClose(func(event *mygo.CloseEvent) {
		if discardEdits || !slices.ContainsFunc(w.previews, func(item *desktopPreview) bool {
			return item.code != nil && item.code.Buffer.Modified()
		}) {
			return
		}
		event.PreventDefault()
		if closing {
			return
		}
		closing = true
		go func() {
			answer, err := w.ask(mygo.MessageOptions{
				Type: mygo.MessageWarning, Message: "关闭窗口并放弃未保存的修改？", Buttons: []string{"取消", "关闭窗口"},
			})
			w.window.Update(func() {
				closing = false
				if err == nil && answer == 1 {
					discardEdits = true
					w.window.Close()
				}
			})
		}()
	})
	w.window.OnBlur(func() { w.window.Update(w.closeWorkbenchPopups) })
	w.window.OnFocus(func() {
		w.refreshProviders()
		w.refreshBookmarks()
	})
	w.window.OnClosed(func() {
		for side, tabs := range w.tabs {
			d.state.Tabs[side] = d.state.Tabs[side][:0]
			for _, tab := range tabs {
				d.state.Tabs[side] = append(d.state.Tabs[side], desktopSavedTab{
					Provider: tab.location.provider, Path: tab.location.path, DisplayPath: tab.displayPath,
				})
			}
			d.state.Active[side] = w.active[side]
		}
		d.state.Grid, d.state.QueueHeight = w.gridView, w.queueHeight
		d.saveState()
		d.workspace = nil
		cancel()
		for _, preview := range w.previews {
			if preview.window != nil {
				preview.window.Close()
			}
			preview.closeVideo()
		}
		w.client.http.CloseIdleConnections()
	})
	w.refreshProviders()
	w.refreshBookmarks()
	w.refreshTemplates()
	for side := range w.tabs {
		panel := w.panel(side)
		w.openLocation(panel, panel.location)
	}
	go w.watchTasks()
}

type desktopLocation struct {
	provider string
	path     string
}

type desktopPanel struct {
	location        desktopLocation
	address         string
	displayPath     string
	entries         []core.Entry
	selected        ui.Selection[string]
	list            ui.ListState
	grid            ui.GridState
	thumbnails      map[string]*desktopThumbnail
	terminalOpen    bool
	sort            ui.SortOrder
	row             int
	lastClickedPath string
	lastFileClick   time.Time
	busy            bool
	requested       desktopLocation
	ready           bool
	error           string
}

type desktopDrag struct {
	side        int
	location    desktopLocation
	displayPath string
	entries     []core.Entry
}

type desktopWorkspace struct {
	queueHeight         float32
	queueTop            float32
	queueWindowH        float32
	gridView            [2]bool
	activePane          int
	sidebarTab          int
	session             desktopSessionForm
	templates           []app.TransferTemplate
	template            desktopTemplateForm
	chooser             desktopTemplateChooser
	memory              desktopMemory
	toolbox             [6]desktopToolbox
	toolboxSelected     int
	toolboxClockPending bool

	previews         []*desktopPreview
	previewIndex     int
	previewOpen      bool
	previewPanel     bool
	previewTabScroll [2]ui.ScrollState
	previewReveal    *desktopPreview
	previewMax       bool
	queueCollapsed   bool
	queueExpanded    bool
	sessionQuery     string
	sessionGroups    map[string]*bool
	window           *mygo.Window
	client           *desktopClient
	ctx              context.Context
	dialogs          chan struct{}
	connectionMu     sync.Mutex
	tabs             [2][]*desktopPanel
	active           [2]int
	fileDialog       desktopFileDialog
	bookmarkMaps     map[string][]app.PathBookmark
	bookmarkPanel    *desktopPanel
	localTree        desktopLocalTree
	thumbnailJobs    int
	terminalTab      string
	conflict         desktopConflict
	bookmarkOpen     bool
	bookmarkBusy     bool
	bookmarkError    string
	providers        []core.ProviderInfo
	providerBusy     bool
	pendingSessions  map[string]bool
	providerError    string
	queue            []core.TransferTask
	queueList        ui.ListState

	queueSelection ui.Selection[string]
	queueMetrics   map[string]desktopTransferMetric
	queueFilter    int
	visibleQueue   []core.TransferTask
	activity       []desktopActivity
	queueOnline    bool
	queueRetrying  bool
	queueAction    bool
	transferring   bool
	notifications  []ui.Toast
}

func (w *desktopWorkspace) refreshProviders() {
	if w.providerBusy {
		return
	}
	w.providerBusy = true
	go func() {
		var providers []core.ProviderInfo
		err := w.client.request(w.ctx, http.MethodGet, "/api/v1/providers", nil, &providers)
		order := map[string]int{"local": 0, "sftp": 1, "ftp": 2}
		names := collate.New(language.English)
		slices.SortFunc(providers, func(a, b core.ProviderInfo) int {
			if result := cmp.Compare(order[a.Kind], order[b.Kind]); result != 0 {
				return result
			}
			return desktopCompareNames(names, a.Name, b.Name)
		})
		w.window.Update(func() {
			w.providerBusy = false
			w.providerError = ""
			if err != nil {
				w.providerError = "无法更新会话列表 · " + err.Error()
				return
			}
			w.providers = providers
		})
	}()
}

func providerLabel(provider core.ProviderInfo) string {
	if provider.Kind == "local" {
		return provider.Name
	}
	return strings.ToUpper(provider.Kind) + " · " + provider.Name
}

func newDesktopPanel(location desktopLocation) *desktopPanel {
	return &desktopPanel{location: location, address: location.path, row: -1, sort: ui.SortOrder{Column: "name"}}
}

func (w *desktopWorkspace) panel(side int) *desktopPanel {
	return w.tabs[side][w.active[side]]
}

func (w *desktopWorkspace) selectTab(side, index int) {
	w.active[side] = index
	panel := w.panel(side)
	w.openLocation(panel, panel.location)
}

func (w *desktopWorkspace) closeTabAt(side, index int) {
	if len(w.tabs[side]) <= 1 || (side == 0 && w.tabs[side][index].location.provider == "local") {
		return
	}
	active := w.active[side]
	w.tabs[side] = slices.Delete(w.tabs[side], index, index+1)
	if index == active {
		w.selectTab(side, max(0, index-1))
	} else if index < active {
		w.active[side]--
	}
}

func (w *desktopWorkspace) openLocation(panel *desktopPanel, location desktopLocation) {
	if panel.busy {
		return
	}
	panel.busy, panel.error, panel.requested = true, "", location
	savedDisplay := panel.displayPath
	restoreLocal := location == panel.location && strings.HasPrefix(location.provider, "local-") && filepath.IsAbs(savedDisplay)
	go func() {
		display, entries, err := w.loadLocation(location)
		var failure *desktopAPIError
		if restoreLocal && errors.As(err, &failure) && failure.Code == "PROVIDER_NOT_FOUND" {
			w.loadLocalFolder(panel, savedDisplay)
			return
		}
		w.finishLocation(panel, location, display, entries, err)
	}()
}

func (w *desktopWorkspace) finishLocation(panel *desktopPanel, location desktopLocation, display string, entries []core.Entry, err error) {
	w.window.Update(func() { panel.applyLocation(location, display, entries, err) })
}

func (panel *desktopPanel) applyLocation(location desktopLocation, display string, entries []core.Entry, err error) {
	panel.busy = false
	if err != nil {
		panel.error = err.Error()
		return
	}
	if panel.location != location {
		panel.selected.Clear()
		panel.list = ui.ListState{}
		panel.grid = ui.GridState{}
		panel.lastFileClick = time.Time{}
		panel.row = -1
	}
	panel.location, panel.address, panel.displayPath = location, location.path, display
	if display != "" {
		panel.address = display
	}
	if panel.selected.Len() > 0 {
		available := make(map[string]bool, len(entries))
		for _, entry := range entries {
			available[entry.Path] = true
		}
		for _, key := range slices.Collect(panel.selected.All()) {
			if !available[key] {
				panel.selected.Remove(key)
			}
		}
	}
	panel.entries, panel.ready, panel.error = entries, true, ""
	panel.thumbnails = nil
	panel.sortEntries()
}

func (p *desktopPanel) sortEntries() {
	names := collate.New(language.Chinese, collate.Numeric, collate.IgnoreCase, collate.IgnoreDiacritics)
	slices.SortStableFunc(p.entries, func(a, b core.Entry) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		order := 0
		switch p.sort.Column {
		case "size":
			order = cmp.Compare(a.Size, b.Size)
		case "modified":
			order = a.Modified.Compare(b.Modified)
		}
		if order == 0 {
			order = desktopCompareNames(names, a.Name, b.Name)
		}
		if p.sort.Descending {
			return -order
		}
		return order
	})
}

func desktopCompareNames(names *collate.Collator, a, b string) int {
	left, _ := utf8.DecodeRuneInString(a)
	right, _ := utf8.DecodeRuneInString(b)
	rank := func(r rune) int {
		switch {
		case unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsDigit(r):
			return 0
		case unicode.Is(unicode.Han, r):
			return 1
		default:
			return 2
		}
	}
	// Align the script order with Web UI's Chinese collation.
	if order := cmp.Compare(rank(left), rank(right)); order != 0 {
		return order
	}
	return names.CompareString(a, b)
}

func (w *desktopWorkspace) loadLocation(location desktopLocation) (string, []core.Entry, error) {
	query := url.Values{"provider": {location.provider}, "path": {location.path}}
	endpoint := "/api/v1/files?" + query.Encode()
	var result struct {
		DisplayPath string       `json:"display_path"`
		Entries     []core.Entry `json:"entries"`
	}
	err := w.client.request(w.ctx, http.MethodGet, endpoint, nil, &result)
	var failure *desktopAPIError
	if !isLocalProvider(location.provider) && errors.As(err, &failure) &&
		(failure.Code == "PROVIDER_NOT_FOUND" || failure.Code == "CONNECTION_LOST") {
		if err = w.connect(location.provider); err == nil {
			err = w.client.request(w.ctx, http.MethodGet, endpoint, nil, &result)
		}
	}
	return result.DisplayPath, result.Entries, err
}

func (w *desktopWorkspace) connect(provider string) error {
	w.connectionMu.Lock()
	defer w.connectionMu.Unlock()

	endpoint := "/api/v1/connections/" + url.PathEscape(provider)
	body := struct{ Fingerprint string }{}
	err := w.client.request(w.ctx, http.MethodPost, endpoint, body, nil)
	var failure *desktopAPIError
	if !errors.As(err, &failure) || failure.Code != "HOST_KEY_UNKNOWN" || failure.Fingerprint == "" {
		return err
	}
	answer, err := w.ask(mygo.MessageOptions{
		Type: mygo.MessageWarning, Message: "确认 SFTP 主机指纹",
		Detail:  "请通过服务器控制台或管理员核对 SHA-256 指纹。\n\n" + failure.Fingerprint,
		Buttons: []string{"取消", "指纹已核对，连接"}, DefaultButton: 0, CancelButton: 0,
	})
	if err != nil {
		return err
	}
	if answer != 1 {
		return errors.New("已取消连接")
	}
	body.Fingerprint = failure.Fingerprint
	return w.client.request(w.ctx, http.MethodPost, endpoint, body, nil)
}

func (w *desktopWorkspace) ask(options mygo.MessageOptions) (int, error) {
	select {
	case w.dialogs <- struct{}{}:
		defer func() { <-w.dialogs }()
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	options.Parent = w.window
	result, err := mygo.Dialog.Message(options)
	return result.Button, err
}

func (w *desktopWorkspace) loadLocalFolder(panel *desktopPanel, folder string) {
	var provider core.ProviderInfo
	err := w.client.request(w.ctx, http.MethodPost, "/api/v1/local/tabs", map[string]string{"path": folder}, &provider)
	if err != nil {
		w.finishLocation(panel, desktopLocation{}, "", nil, err)
		return
	}
	location := desktopLocation{provider: provider.ID, path: "/"}
	display, entries, err := w.loadLocation(location)
	w.finishLocation(panel, location, display, entries, err)
	w.window.Update(w.refreshProviders)
}

func isLocalProvider(id string) bool {
	return id == "local" || strings.HasPrefix(id, "local-")
}

func (w *desktopWorkspace) watchTasks() {
	for {
		_ = w.client.events(w.ctx, func(tasks []core.TransferTask) {
			w.window.Update(func() { w.updateQueue(tasks) })
		}, func(logs []desktopActivity) {
			w.window.Update(func() { w.activity = logs })
		})
		if w.ctx.Err() != nil {
			return
		}
		w.window.Update(func() { w.queueOnline, w.queueRetrying = false, true })
		timer := time.NewTimer(time.Second)
		select {
		case <-timer.C:
		case <-w.ctx.Done():
			timer.Stop()
			return
		}
	}
}

func (w *desktopWorkspace) updateQueue(tasks []core.TransferTask) {
	initial := !w.queueOnline && !w.queueRetrying
	w.queueOnline, w.queueRetrying = true, false
	previous := make(map[string]string, len(w.queue))
	for _, task := range w.queue {
		previous[task.ID] = task.Status
	}
	w.updateTransferMetrics(tasks, time.Now())
	w.queue = tasks
	w.filterQueue()
	if initial {
		return
	}
	for side := range w.tabs {
		panel := w.panel(side)
		if panel.busy || !panel.ready {
			continue
		}
		for _, task := range tasks {
			if task.Status == "completed" && previous[task.ID] != "completed" &&
				task.TargetProvider == panel.location.provider && path.Dir(task.TargetPath) == panel.location.path {
				w.openLocation(panel, panel.location)
				break
			}
		}
	}
}

func (w *desktopWorkspace) filterQueue() {
	w.visibleQueue = w.visibleQueue[:0]
	for _, task := range w.queue {
		if desktopTaskCategory(task) == w.queueFilter {
			w.visibleQueue = append(w.visibleQueue, task)
		}
	}
	slices.SortStableFunc(w.visibleQueue, func(a, b core.TransferTask) int { return b.CreatedAt.Compare(a.CreatedAt) })
}

func (w *desktopWorkspace) copyEntries(to int, drag desktopDrag) {
	panel := w.panel(to)
	target, targetDisplayPath := panel.location, panel.displayPath
	if drag.side == to || len(drag.entries) == 0 || w.transferring || !panel.ready || panel.busy {
		return
	}
	w.transferring = true
	go func() {
		var accepted, skipped, failed int
		var lastError error
		type submission struct {
			entry   core.Entry
			request core.TransferRequest
			queued  bool
			err     error
		}
		submissions := make([]submission, 0, len(drag.entries))
		verify, preserve := true, true
		for _, entry := range drag.entries {
			toPath := path.Join(target.path, entry.Name)
			if sameCopyLocation(drag, entry, target, toPath, targetDisplayPath) {
				skipped++
				continue
			}
			submissions = append(submissions, submission{entry: entry, request: core.TransferRequest{
				SourceProvider: drag.location.provider, SourcePath: entry.Path,
				TargetProvider: target.provider, TargetPath: toPath,
				Concurrency: 4, ConflictPolicy: core.ConflictAsk, Verify: &verify, PreserveStructure: &preserve,
			}})
		}
		var workers sync.WaitGroup
		workersCount := min(8, len(submissions))
		for worker := range workersCount {
			workers.Go(func() {
				for index := worker; index < len(submissions); index += workersCount {
					item := &submissions[index]
					item.queued, item.err = w.enqueue(item.request)
				}
			})
		}
		workers.Wait()
		batchPolicy := ""
		for _, item := range submissions {
			var conflict *desktopAPIError
			if errors.As(item.err, &conflict) && conflict.Status == http.StatusConflict {
				choice := desktopConflictDecision{policy: batchPolicy}
				item.err = nil
				if batchPolicy == "" {
					choice, item.err = w.askTransferConflict(item.entry, conflict)
					if choice.always {
						batchPolicy = choice.policy
					}
				}
				if item.err == nil {
					item.request.ConflictPolicy = choice.policy
					item.queued, item.err = w.enqueue(item.request)
				}
			}
			switch {
			case item.err != nil:
				failed++
				lastError = item.err
			case item.queued:
				accepted++
			default:
				skipped++
			}
		}

		w.window.Update(func() {
			w.transferring = false
			message := fmt.Sprintf("已提交 %d 项 · 跳过 %d 项 · 失败 %d 项", accepted, skipped, failed)
			if len(submissions) == 0 {
				message = "所选项目的源和目标位置相同"
			} else if lastError != nil {
				message += " · " + lastError.Error()
			}
			w.notify(message, failed > 0 || len(submissions) == 0)
			if accepted > 0 {
				w.setQueueTab(queueActive)
				if panel.location == target {
					w.openLocation(panel, target)
				}
			}
		})
	}()
}

func sameCopyLocation(drag desktopDrag, entry core.Entry, target desktopLocation, toPath, targetDisplayPath string) bool {
	if isLocalProvider(drag.location.provider) && isLocalProvider(target.provider) &&
		filepath.IsAbs(drag.displayPath) && filepath.IsAbs(targetDisplayPath) {
		source := filepath.Join(drag.displayPath, entry.Name)
		target := filepath.Join(targetDisplayPath, entry.Name)
		if resolved, err := filepath.EvalSymlinks(source); err == nil {
			source = resolved
		}
		if resolved, err := filepath.EvalSymlinks(target); err == nil {
			target = resolved
		} else if resolved, err := filepath.EvalSymlinks(targetDisplayPath); err == nil {
			target = filepath.Join(resolved, entry.Name)
		}
		relative, err := filepath.Rel(source, target)
		return err == nil && (relative == "." || (entry.IsDir && relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator))))
	}
	return drag.location.provider == target.provider &&
		(toPath == entry.Path || (entry.IsDir && strings.HasPrefix(toPath, strings.TrimRight(entry.Path, "/")+"/")))
}

func (w *desktopWorkspace) enqueue(request core.TransferRequest) (bool, error) {
	var result struct {
		Status string `json:"status"`
	}
	err := w.client.request(w.ctx, http.MethodPost, "/api/v1/transfers", request, &result)
	return err == nil && result.Status != "skipped", err
}

func (w *desktopWorkspace) changeTask(task core.TransferTask, action string) {
	w.queueAction = true
	go func() {
		endpoint := "/api/v1/transfers/" + url.PathEscape(task.ID) + "/" + action
		err := w.client.request(w.ctx, http.MethodPost, endpoint, struct{}{}, nil)
		w.window.Update(func() {
			w.queueAction = false
			if err != nil {
				w.notify(err.Error(), true)
			}
		})
	}()
}

func (w *desktopWorkspace) openBrowser() {
	go func() {
		if err := app.OpenBrowser(w.client.origin + "/"); err != nil {
			w.window.Update(func() {
				w.notify("无法打开浏览器 · "+err.Error(), true)
			})
		}
	}()
}
