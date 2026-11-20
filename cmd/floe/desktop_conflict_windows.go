package main

import (
	"path"

	"floe/internal/core"

	"github.com/egoist/mygo/ui"
)

type desktopConflictDecision struct {
	policy string
	always bool
}

type desktopConflict struct {
	open           bool
	source, target desktopConflictFile
	decision       desktopConflictDecision
	result         chan desktopConflictDecision
}

func (w *desktopWorkspace) askTransferConflict(entry core.Entry, failure *desktopAPIError) (desktopConflictDecision, error) {
	select {
	case w.dialogs <- struct{}{}:
		defer func() { <-w.dialogs }()
	case <-w.ctx.Done():
		return desktopConflictDecision{}, w.ctx.Err()
	}
	result := make(chan desktopConflictDecision, 1)
	source := desktopConflictFile{Path: entry.Path, Name: entry.Name, Size: &entry.Size, Modified: entry.Modified}
	target := desktopConflictFile{Path: failure.TargetPath, Name: path.Base(failure.TargetPath)}
	if failure.Source != nil {
		source = *failure.Source
	}
	if failure.Target != nil {
		target = *failure.Target
	}
	w.window.Update(func() {
		w.conflict = desktopConflict{open: true, source: source, target: target, result: result,
			decision: desktopConflictDecision{policy: core.ConflictOverwrite}}
	})
	select {
	case choice := <-result:
		return choice, nil
	case <-w.ctx.Done():
		return desktopConflictDecision{}, w.ctx.Err()
	}
}

func (w *desktopWorkspace) transferConflictDialog(c *ui.Context) {
	dialog := &w.conflict
	if !dialog.open {
		return
	}
	choice := desktopConflictDecision{policy: core.ConflictSkip}
	desktopDialog(c, &dialog.open, "处理文件冲突", 520, false, func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Width(0).MinWidth(0).Grow(1).Gap(8).AlignItems(ui.Stretch).Children(func() {
				desktopConflictCard(c, "源文件", dialog.source, false)
				desktopConflictCard(c, "目标文件（已存在）", dialog.target, true)
			})
			w.transferConflictOptions(c, dialog)
		})
		ui.Checkbox(c, &dialog.decision.always, "本批次后续冲突都使用该操作").FontSize(12)
	}, func() {
		if desktopDialogButton(c, "跳过此文件", false, false).Clicked() {
			dialog.open = false
		}
		if desktopDialogButton(c, "继续处理", true, false).Clicked() {
			choice, dialog.open = dialog.decision, false
		}
	})
	if !dialog.open {
		dialog.result <- choice
		dialog.result = nil
	}
}

func (w *desktopWorkspace) transferConflictOptions(c *ui.Context, dialog *desktopConflict) {
	ui.Column(c).Width(145).Gap(5).AlignItems(ui.Stretch).Children(func() {
		for _, option := range []struct{ policy, label string }{
			{core.ConflictOverwrite, "覆盖"}, {core.ConflictSkip, "跳过已存在"},
			{core.ConflictIfNewer, "仅源文件更新时覆盖"}, {core.ConflictRename, "自动重命名"},
		} {
			selected := option.policy == dialog.decision.policy
			button := ui.Button(c, "").Label(option.label).AccessSelected(selected).
				Height(32).Padding(0, 11).Radius(0).Border(1, c.Theme().Border).Background(c.Theme().Background)
			color := c.Theme().Text
			if selected || button.Hovered() {
				button.Background(ui.Hex("#e8f3f4")).BorderColor(c.Theme().Accent)
				color = c.Theme().Accent
			}
			button.Children(func() { ui.Text(c, option.label).FontSize(12).Grow(1).TextColor(color) })
			if button.Clicked() {
				dialog.decision.policy = option.policy
			}
		}
	})
}

func desktopConflictCard(c *ui.Context, label string, file desktopConflictFile, target bool) {
	border, background, caption := c.Theme().Border, c.Theme().Surface, c.Theme().TextMuted
	if target {
		border, background, caption = ui.Hex("#d8b4ae"), ui.Hex("#fcf7f6"), c.Theme().Danger
	}
	ui.Column(c).Width(0).MinWidth(0).Grow(1).AlignItems(ui.Stretch).Padding(8, 9).
		Border(1, border).Background(background).Children(func() {
		ui.Text(c, label).FontSize(11).FontWeight(650).TextColor(caption).Margin(0, 0, 5)
		ui.Text(c, file.Name).FontWeight(650).SingleLine().Tooltip(file.Name)
		ui.Text(c, file.Path).Font("Cascadia Code").FontSize(10).TextColor(c.Theme().TextMuted).Margin(3, 0, 0)
		size, modified := "未知", "未知"
		if file.Size != nil {
			size = desktopSize(*file.Size)
		}
		if !file.Modified.IsZero() {
			modified = desktopFileTime(file.Modified)
		}
		ui.Text(c, "大小："+size+"\n更新时间："+modified).FontSize(10).TextColor(c.Theme().TextMuted).Margin(6, 0, 0)
	})
}
