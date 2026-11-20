package main

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"floe/internal/app"
	"floe/internal/core"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (w *desktopWorkspace) taskMenu(menu *ui.Menu, task core.TransferTask) {
	switch task.Status {
	case "running", "verifying":
		if menu.Item("暂停任务").Chosen() {
			w.changeTask(task, "pause")
		}
	case "paused", "failed":
		if menu.Item("继续任务").Chosen() {
			w.changeTask(task, "resume")
		}
	case "completed", "skipped":
		var tasks []app.TransferTemplateTask
		for _, item := range w.visibleQueue {
			if w.queueSelection.Has(item.ID) {
				tasks = append(tasks, templateItem(item))
			}
		}
		if len(tasks) < 2 {
			tasks = []app.TransferTemplateTask{templateItem(task)}
		}
		w.templateMenu(menu, tasks)
	}
	if menu.Item("删除任务").Chosen() {
		w.deleteQueueData("/api/v1/transfers/"+url.PathEscape(task.ID), "删除选中的传输任务？")
	}
}

func queueClearButton(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Height(23).Margin(0, 5, 0, 0).Padding(0, 6).FontSize(11).
		TextColor(c.Theme().TextMuted).Background(c.Theme().Background).Border(1, c.Theme().Border)
}

func desktopTaskCategory(task core.TransferTask) int {
	switch task.Status {
	case "completed", "skipped":
		return queueCompleted
	case "failed":
		return queueFailed
	default:
		return queueActive
	}
}

func desktopTaskRunning(task core.TransferTask) bool {
	return task.Status == "running" || task.Status == "verifying"
}

type desktopTransferMetric struct {
	transport, read, written int64
	readRate, writeRate      float64
	sampledAt                time.Time
}

func (w *desktopWorkspace) updateTransferMetrics(tasks []core.TransferTask, now time.Time) {
	metrics := make(map[string]desktopTransferMetric, len(tasks))
	for _, task := range tasks {
		transferred := max(task.BytesTransferred, task.BytesVerified)
		read, written := task.BytesRead, task.BytesWritten
		if read == 0 {
			read = transferred
		}
		if written == 0 {
			written = transferred
		}
		metric := desktopTransferMetric{transport: transferred, read: read, written: written, sampledAt: now}
		previous, found := w.queueMetrics[task.ID]
		switch {
		case desktopTaskRunning(task) && found:
			metric.readRate, metric.writeRate = previous.readRate, previous.writeRate
			if interval := now.Sub(previous.sampledAt).Seconds(); interval > 0 {
				metric.readRate = transferRate(previous.readRate, read-previous.read, interval)
				metric.writeRate = transferRate(previous.writeRate, written-previous.written, interval)
			}
		case task.Status == "running" && transferred > 0:
			elapsed := max(1, now.Sub(task.CreatedAt).Seconds())
			metric.readRate, metric.writeRate = float64(read)/elapsed, float64(written)/elapsed
		case task.Status == "completed":
			elapsed := max(1, task.UpdatedAt.Sub(task.CreatedAt).Seconds())
			metric.readRate, metric.writeRate = float64(read)/elapsed, float64(written)/elapsed
		}
		if found && previous.transport == transferred && previous.read == read && previous.written == written {
			metric.sampledAt = previous.sampledAt
		}
		metrics[task.ID] = metric
	}
	for id := range w.queueSelection.All() {
		if _, exists := metrics[id]; !exists {
			w.queueSelection.Remove(id)
		}
	}
	w.queueMetrics = metrics
}

func transferRate(previous float64, delta int64, seconds float64) float64 {
	if delta <= 0 {
		return previous
	}
	rate := float64(delta) / seconds
	if previous > 0 {
		return previous*.65 + rate*.35
	}
	return rate
}

func (w *desktopWorkspace) taskRow(c *ui.Context, task core.TransferTask) {
	theme := c.Theme()
	route := task.SourcePath + " → " + task.TargetPath
	transferred := max(task.BytesTransferred, task.BytesVerified)
	end := c.Now()
	if desktopTaskCategory(task) != queueActive {
		end = task.UpdatedAt
	}
	metric := w.queueMetrics[task.ID]
	speed := metric.writeRate
	if speed <= 0 {
		speed = metric.readRate
	}
	remaining := "--"
	if desktopTaskRunning(task) && speed > 0 {
		remaining = queueDuration(float64(task.Size-transferred) / speed)
	}
	progress, verified := float32(100), float32(100)
	if task.Size > 0 {
		progress = float32(100 * min(1, float64(transferred)/float64(task.Size)))
		verified = float32(100 * min(1, float64(task.BytesVerified)/float64(task.Size)))
	}
	direction := "传输"
	left, right := w.panel(0).location.provider, w.panel(1).location.provider
	if task.SourceProvider == left && task.TargetProvider == right {
		direction = "上传"
	} else if task.SourceProvider == right && task.TargetProvider == left {
		direction = "下载"
	}
	ui.Row(c).Height(32).FillWidth().Padding(0, 8).Gap(8).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(ui.Hex("#e5e8ea")).
		PopoverMenu(func(menu *ui.Menu) { w.taskMenu(menu, task) }).Children(func() {
		ui.Row(c).Width(0).MinWidth(190).Grow(1).AlignItems(ui.Center).Children(func() {
			if desktopTaskCategory(task) == queueCompleted {
				w.taskCheckbox(c, task.ID)
			}
			ui.Text(c, direction+"　"+route).Width(0).MinWidth(0).Grow(1).
				SingleLine().FontSize(11).Tooltip(route)
		})
		ui.Box(c).Size(130, 6).Shrink(0).Clip().Background(ui.Hex("#e4e7e9")).
			Tooltip(fmt.Sprintf("%s · 已传输 %.1f%% · 已校验 %.1f%%", queueTaskStatus(task.Status), progress, verified)).
			Children(func() {
				ui.Box(c).Absolute().Top(0).Left(0).Height(6).WidthPercent(progress).Background(ui.Hex("#8fc7cd"))
				ui.Box(c).Absolute().Bottom(0).Left(0).Height(3).WidthPercent(verified).Background(ui.Hex("#126f7b"))
			})
		ui.Text(c, queueBytes(float64(transferred))+" / "+queueBytes(float64(task.Size))).
			Width(130).Shrink(0).SingleLine().Font("Cascadia Code").FontSize(11).TextColor(theme.TextMuted)
		ui.Text(c, "读 "+queueRate(metric.readRate)+"　写 "+queueRate(metric.writeRate)).
			Width(150).Shrink(0).SingleLine().Font("Cascadia Code").FontSize(10).
			TextColor(theme.TextMuted).Tooltip("读取和写入平均速度")
		for i, value := range []string{"剩余 " + remaining, "已用 " + queueDuration(end.Sub(task.CreatedAt).Seconds())} {
			ui.Text(c, value).Width([]float32{100, 82}[i]).Shrink(0).SingleLine().
				Font("Cascadia Code").FontSize(11).TextColor(theme.TextMuted).
				Tooltip([]string{"预计剩余时间", "已经过时间"}[i])
		}
	})
}

func (w *desktopWorkspace) taskCheckbox(c *ui.Context, id string) {
	checked := w.queueSelection.Has(id)
	theme := c.Theme()
	button := ui.Button(c, "").Role(ui.RoleCheckBox).Label("选择任务").Checked(checked).
		Size(13, 13).Shrink(0).Padding(0).Margin(0, 6, 0, 0).
		Radius(2).Background(theme.Background).Border(1, ui.Hex("#767676"))
	if checked {
		button.Background(theme.Accent).BorderColor(theme.Accent)
		button.Children(func() {
			ui.Text(c, "✓").FontSize(10).FontWeight(700).TextColor(theme.Background).SingleLine()
		})
	}
	if button.Clicked() {
		if checked {
			w.queueSelection.Remove(id)
		} else {
			w.queueSelection.Add(id)
		}
		c.Invalidate()
	}
}

func queueTaskStatus(status string) string {
	if label := (map[string]string{"running": "传输", "verifying": "校验", "paused": "暂停", "failed": "失败", "completed": "完成", "skipped": "已跳过"})[status]; label != "" {
		return label
	}
	return status
}

func queueDuration(seconds float64) string {
	total := int(max(0, math.Round(seconds)))
	if total >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", total/3600, total/60%60, total%60)
	}
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

func queueBytes(value float64) string {
	if value < 1024 {
		return fmt.Sprintf("%g B", value)
	}
	for _, unit := range []string{"KB", "MB", "GB", "TB"} {
		value /= 1024
		if value < 1024 || unit == "TB" {
			places := 2
			if value >= 10 {
				places = 1
			}
			return fmt.Sprintf("%.*f %s", places, value, unit)
		}
	}
	return ""
}

func queueRate(value float64) string {
	if value <= 0 {
		return "--"
	}
	return queueBytes(value) + "/s"
}

func templateItem(task core.TransferTask) app.TransferTemplateTask {
	policy := task.ConflictPolicy
	if policy == "" || policy == core.ConflictAsk {
		policy = core.ConflictOverwrite
	}
	return app.TransferTemplateTask{
		SourceProvider: task.SourceProvider, SourcePath: task.SourcePath,
		SourceProviderName: task.SourceProviderName, SourceProviderKind: task.SourceProviderKind, SourceProviderLocation: task.SourceProviderLocation,
		TargetProvider: task.TargetProvider, TargetPath: task.TargetPath,
		TargetProviderName: task.TargetProviderName, TargetProviderKind: task.TargetProviderKind, TargetProviderLocation: task.TargetProviderLocation,
		ConflictPolicy: policy, Concurrency: task.Concurrency, Verify: task.Verify, PreserveStructure: true,
	}
}

type desktopTemplateChooser struct {
	open, busy bool
	index      int
	tasks      []app.TransferTemplateTask
	err        string
}

func (w *desktopWorkspace) templateMenu(menu *ui.Menu, tasks []app.TransferTemplateTask) {
	label := "保存为模板…"
	if len(tasks) > 1 {
		label = fmt.Sprintf("保存选中的 %d 个任务为模板…", len(tasks))
	}
	if menu.Item(label).Chosen() {
		name := fmt.Sprintf("发布任务（%d项）", len(tasks))
		if len(tasks) == 1 {
			name = tasks[0].SourceProvider + " → " + tasks[0].TargetProvider
		}
		w.editTemplate(app.TransferTemplate{Name: name, Tasks: tasks})
	}
	if menu.Item("添加到模板").Chosen() {
		if len(w.templates) == 0 {
			w.notify("还没有发布模板，请先保存一个模板", true)
		} else {
			w.chooser = desktopTemplateChooser{open: true, tasks: tasks}
		}
	}
}

func (w *desktopWorkspace) templateChooser(c *ui.Context) {
	chooser := &w.chooser
	if !chooser.open {
		return
	}
	labels := make([]string, len(w.templates))
	for index, template := range w.templates {
		labels[index] = template.Name
	}
	desktopDialog(c, &chooser.open, "添加到模板", 400, chooser.busy, func() {
		ui.Column(c).FillWidth().Children(func() {
			desktopFieldLabel(c, "选择发布模板")
			selectBox := ui.SelectBase(c, &chooser.index)
			selectBox.Trigger.Label("选择发布模板").FillWidth().Height(29).Disabled(chooser.busy).
				Padding(0, 7).Background(c.Theme().Background).Border(1, c.Theme().Border).Children(func() {
				label := ""
				if chooser.index >= 0 && chooser.index < len(labels) {
					label = labels[chooser.index]
				}
				ui.Text(c, label).SingleLine().Grow(1)
				ui.Icon(c, iconDown).FontSize(14).TextColor(c.Theme().TextMuted)
			})
			selectBox.Popup(func(popup ui.Element) {
				styleDirectoryPopup(popup, 370)
				ui.Scroll(c).MaxHeight(250).Children(func() {
					ui.Column(c).FillWidth().Children(func() {
						for index, label := range labels {
							item := selectBox.Item(index).Label(label).FillWidth().Height(28).Padding(0, 8)
							if item.Highlighted() {
								item.Background(c.Theme().Selection)
							}
							item.Children(func() { ui.Text(c, label).SingleLine() })
						}
					})
				})
			})
		})
		if chooser.err != "" {
			ui.Text(c, chooser.err).TextColor(c.Theme().Danger)
		}
	}, func() {
		if desktopDialogButton(c, "取消", false, chooser.busy).Clicked() {
			chooser.open = false
		}
		if desktopDialogButton(c, "添加", true, chooser.busy || chooser.index < 0 || chooser.index >= len(w.templates)).Clicked() {
			w.appendTemplateTasks(w.templates[chooser.index])
		}
	})
}

func (w *desktopWorkspace) appendTemplateTasks(template app.TransferTemplate) {
	chooser := &w.chooser
	key := func(item app.TransferTemplateTask) [4]string {
		return [4]string{item.SourceProvider, item.SourcePath, item.TargetProvider, item.TargetPath}
	}
	existing := make(map[[4]string]bool)
	tasks := append([]app.TransferTemplateTask(nil), templateTasks(template)...)
	for _, item := range tasks {
		existing[key(item)] = true
	}
	original := len(tasks)
	for _, item := range chooser.tasks {
		id := key(item)
		if !existing[id] {
			existing[id] = true
			tasks = append(tasks, item)
		}
	}
	added := len(tasks) - original
	if added == 0 {
		chooser.open = false
		w.notify("所选发布项已经在模板中", false)
		return
	}
	template.Tasks = tasks
	chooser.busy, chooser.err = true, ""
	go func() {
		err := w.client.request(w.ctx, http.MethodPost, "/api/v1/transfer-templates", template, nil)
		w.window.Update(func() {
			chooser.busy = false
			if err != nil {
				chooser.err = err.Error()
				return
			}
			chooser.open = false
			w.notify(fmt.Sprintf("已添加 %d 个发布项到模板“%s”", added, template.Name), false)
			w.refreshTemplates()
		})
	}()
}

func (w *desktopWorkspace) deleteQueueData(endpoint, question string) {
	if w.queueAction {
		return
	}
	w.queueAction = true
	go func() {
		answer, err := w.ask(mygo.MessageOptions{
			Type: mygo.MessageWarning, Message: question, Buttons: []string{"取消", "确认"},
			DestructiveButtons: []int{1},
		})
		if err == nil && answer == 1 {
			err = w.client.request(w.ctx, http.MethodDelete, endpoint, nil, nil)
		}
		w.window.Update(func() {
			w.queueAction = false
			if err != nil {
				w.notify(err.Error(), true)
			}
		})
	}()
}
