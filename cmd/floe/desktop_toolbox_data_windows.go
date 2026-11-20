package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

var toolboxDivider = ui.Hex("#e5e8ea")

func toolboxFieldLabel(c *ui.Context, label string) {
	ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
}

func toolboxButton(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Height(25).Shrink(0).Padding(3, 9).FontSize(11).
		Background(c.Theme().Background).Radius(0)
}

func toolboxSelect(c *ui.Context, value *string, label string, options ...string) ui.Element {
	return ui.Select(c, value, options).Height(25).MaxWidth(190).Padding(2, 5).FontSize(11).
		Background(c.Theme().Background).Radius(0).Label(label)
}

func toolboxStatus(c *ui.Context, state *desktopToolbox, footer bool) {
	color := c.Theme().TextMuted
	switch state.statusKind {
	case "error":
		color = c.Theme().Danger
	case "success":
		color = c.Theme().Accent
	case "warning":
		color = ui.Hex("#906010")
	}
	status := ui.Text(c, state.status).FillWidth().MinHeight(16).Shrink(0).FontSize(11).
		LineHeight(1.5).TextColor(color)
	if footer {
		status.MinHeight(26).Padding(5, 12).BorderWidth(1, 0, 0, 0).BorderColor(toolboxDivider)
	}
}

func toolboxHint(c *ui.Context, text string) {
	ui.Text(c, text).FillWidth().Shrink(0).FontSize(11).LineHeight(1.6).TextColor(c.Theme().TextMuted)
}

func toolboxEditor(c *ui.Context, label string, value *string, placeholder string, minHeight, grow float32, changed func()) {
	ui.Column(c).Width(0).FillWidth().Grow(grow).MinHeight(minHeight).MinWidth(0).Gap(6).
		AlignItems(ui.Stretch).Children(func() {
		toolboxFieldLabel(c, label)
		if ui.TextArea(c, value).Grow(1).MinHeight(40).Label(label).Placeholder(placeholder).
			Padding(8, 9).Radius(0).Font("Cascadia Code").FontSize(12).LineHeight(1.6).
			Background(c.Theme().Background).Changed() {
			changed()
		}
	})
}

func toolboxOutput(c *ui.Context, value *string, placeholder string) {
	ui.TextArea(c, value).Grow(1).FillWidth().MinHeight(75).ReadOnly(true).Label("处理结果").
		Placeholder(placeholder).Padding(8, 9).Radius(0).Font("Cascadia Code").FontSize(12).
		LineHeight(1.6).Background(ui.Hex("#f8fafb"))
}

func (w *desktopWorkspace) toolboxDataView(c *ui.Context, state *desktopToolbox, height, width float32) {
	vertical, horizontal := float32(12), float32(12)
	if w.queueExpanded {
		vertical, horizontal = 20, 24
	}
	ui.Scroll(c).Key(state.selected).Grow(1).MinHeight(0).Children(func() {
		switch state.selected {
		case 3:
			w.toolboxDateView(c, state, width, vertical, horizontal)
		case 4:
			w.toolboxJSONView(c, state, height, vertical, horizontal)
		case 5:
			w.toolboxDiffView(c, state, height, vertical, horizontal)
		}
	})
	toolboxStatus(c, state, true)
}

func (w *desktopWorkspace) toolboxDateView(c *ui.Context, state *desktopToolbox, width, vertical, horizontal float32) {
	narrow := width <= 1050
	labelWidth, gap := float32(185), float32(8)
	if narrow {
		labelWidth, gap = 145, 5
	}
	ui.Column(c).FillWidth().Padding(vertical, horizontal).Gap(8).AlignItems(ui.Stretch).Children(func() {
		controls := ui.Row(c).Gap(8).AlignItems(ui.Center)
		if narrow {
			controls.Wrap()
		}
		controls.Children(func() {
			field := ui.TextInput(c, &state.input).Height(30).MinWidth(120).Font("Cascadia Code").
				FontSize(12).Label("日期或时间戳").Placeholder("留空实时刷新当前时间，或输入日期 / 时间戳")
			if narrow {
				field.FillWidth()
			} else {
				field.Width(0).Grow(1)
			}
			toolboxSelect(c, &state.dateMode, "输入日期格式", "自动识别", "Unix 时间戳（秒）", "时间戳（毫秒）", "日期字符串", "Mongo ObjectID", "Excel 日期序列").Height(30)
			toolboxSelect(c, &state.zone, "日期时区", "本地时区", "UTC").Height(30)
			if toolboxButton(c, "当前时间").Clicked() {
				state.input = ""
				c.Invalidate()
			}
		})
		rows, dateContext, err := desktopDateFormats(*state)
		state.status, state.statusKind = "", ""
		if err != nil {
			state.status, state.statusKind = err.Error(), "error"
		}
		toolboxStatus(c, state, false)
		toolboxHint(c, dateContext)
		ui.Column(c).FillWidth().MaxWidth(1000).Gap(7).Padding(6, 0).AlignItems(ui.Stretch).Children(func() {
			for _, row := range rows {
				value := row.value
				ui.Row(c).Height(30).Gap(gap).AlignItems(ui.Center).Children(func() {
					ui.Text(c, row.label).Width(labelWidth).TextAlign(ui.End).SingleLine().
						FontSize(12).TextColor(c.Theme().TextMuted)
					ui.TextInput(c, &value).Width(0).Grow(1).MinWidth(0).Height(30).ReadOnly(true).
						Padding(5, 8).Font("Cascadia Code").FontSize(12).Label(row.label).Placeholder("超出此格式的有效范围")
					if toolboxButton(c, "复制").Height(30).Disabled(value == "").Clicked() {
						c.WriteClipboard(value)
						w.notify("结果已复制", false)
					}
				})
			}
		})
		toolboxHint(c, "无时区的日期按所选时区解释；带偏移的日期和时间戳表示固定时刻。ObjectID 输出仅保留时间前缀，其余补零。")
	})
	if strings.TrimSpace(state.input) == "" && !w.toolboxClockPending {
		w.toolboxClockPending = true
		time.AfterFunc(time.Second/60, func() {
			if w.ctx.Err() == nil {
				w.window.Update(func() { w.toolboxClockPending = false })
			}
		})
	}
}

func (w *desktopWorkspace) toolboxJSONView(c *ui.Context, state *desktopToolbox, height, vertical, horizontal float32) {
	format := func() { w.runTool(state, "格式化 / 过滤", 0) }
	schedule := func() { w.runTool(state, "格式化 / 过滤", 300*time.Millisecond) }
	ui.Column(c).FillWidth().Height(max(height, 220+2*vertical)).Gap(10).Padding(vertical, horizontal).
		AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			if ui.Checkbox(c, &state.sortKeys, "排序键").FontSize(11).Changed() {
				format()
			}
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				toolboxFieldLabel(c, "缩进")
				if ui.NumberInput(c, &state.indent, 0, 8, 1).Width(52).Height(25).Label("JSON 缩进空格数").Changed() {
					schedule()
				}
			})
			if ui.Checkbox(c, &state.unwrapJSON, "解析转义的 JSON 字符串").FontSize(11).Changed() {
				format()
			}
			if toolboxButton(c, "格式化 / 过滤").Clicked() {
				format()
			}
			if toolboxButton(c, "压缩").Clicked() {
				w.runTool(state, "压缩", 0)
			}
		})
		ui.Row(c).Height(42).Gap(8).Padding(6, 8).Border(1, toolboxDivider).
			Background(ui.Hex("#f8fafb")).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "jq").Font("Cascadia Code").FontSize(12).FontWeight(600).TextColor(c.Theme().Accent)
			if ui.TextInput(c, &state.jq).Width(0).Grow(1).MinWidth(50).Height(28).
				Font("Cascadia Code").FontSize(12).Label("jq 过滤表达式").
				Placeholder(".  或  .items[] | select(.active == true) | .name").Changed() {
				schedule()
			}
			if toolboxButton(c, "示例").Clicked() {
				state.input = `{"items":[{"name":"Floe","active":true},{"name":"Other","active":false}]}`
				state.jq = ".items[] | select(.active == true) | .name"
				format()
				c.Invalidate()
			}
		})
		ui.Row(c).Grow(1).MinHeight(130).Gap(12).AlignItems(ui.Stretch).Children(func() {
			toolboxEditor(c, "原始 JSON", &state.input, `{"hello":"world","items":[{"name":"Floe","active":true}]}`, 130, 1, schedule)
			ui.Column(c).Width(0).Grow(1).MinWidth(0).Gap(6).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Height(25).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "格式化 / 过滤结果").FontSize(11).TextColor(c.Theme().TextMuted).Grow(1)
					if toolboxButton(c, "复制结果").Clicked() && state.output != "" {
						c.WriteClipboard(state.output)
						w.notify("JSON 结果已复制", false)
					}
				})
				toolboxOutput(c, &state.output, "")
			})
		})
		toolboxHint(c, "支持 .field、.[0]、.[]、管道、select、map、keys、length 等 jq 语法。留空表示只格式化；多项输出按 JSON 流逐项显示。大整数保留精度。")
	})
}

func (w *desktopWorkspace) toolboxDiffView(c *ui.Context, state *desktopToolbox, height, vertical, horizontal float32) {
	editorHeight := float32(150)
	if w.queueExpanded {
		editorHeight = max(130, (height-2*vertical)*.32)
	}
	compare := func() { w.runTool(state, "比较", 0) }
	schedule := func() { w.runTool(state, "比较", 300*time.Millisecond) }
	ui.Column(c).FillWidth().Height(max(height, editorHeight+180+2*vertical)).Padding(vertical, horizontal).
		Gap(10).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c, &state.onlyDiff, "只显示差异").FontSize(11)
			if toolboxButton(c, "比较").Clicked() {
				compare()
			}
			if toolboxButton(c, "交换左右").Clicked() {
				state.input, state.other = state.other, state.input
				compare()
				c.Invalidate()
			}
			if toolboxButton(c, "复制比较结果").Clicked() && state.diffRows != nil {
				data, _ := json.MarshalIndent(state.visibleDiffRows(), "", "  ")
				c.WriteClipboard(string(data))
				w.notify("比较结果已复制", false)
			}
			if state.diffRows != nil {
				counts := make(map[string]int, 3)
				for _, row := range state.diffRows {
					counts[row.Kind]++
				}
				toolboxFieldLabel(c, fmt.Sprintf("新增 %d · 删除 %d · 修改 %d", counts["added"], counts["removed"], counts["changed"]))
			}
		})
		ui.Row(c).Height(editorHeight).Gap(12).AlignItems(ui.Stretch).Children(func() {
			toolboxEditor(c, "原始 JSON", &state.input, `{"a":1,"c":false}`, 130, 1, schedule)
			toolboxEditor(c, "比较 JSON", &state.other, `{"a":2,"b":1}`, 130, 1, schedule)
		})
		w.toolboxDiffTable(c, state)
		toolboxHint(c, "忽略对象键顺序，按数组索引比较；区分 null、缺失、类型变化。绿色为新增，红色为删除，琥珀色为修改。")
	})
}

func (w *desktopWorkspace) toolboxDiffTable(c *ui.Context, state *desktopToolbox) {
	ui.Column(c).Grow(1).MinHeight(100).Border(1, c.Theme().Border).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Height(30).Shrink(0).Background(c.Theme().Surface).AlignItems(ui.Stretch).Children(func() {
			for i, label := range []string{"状态 / JSON 路径", "原始值", "比较值"} {
				ui.Text(c, label).Width(0).Grow([]float32{30, 35, 35}[i]).
					Padding(7, 9).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			rows := state.visibleDiffRows()
			if state.diffRows != nil && len(rows) == 0 {
				ui.Text(c, "两个 JSON 的内容相同").FillWidth().Padding(7, 9).
					TextAlign(ui.Center).FontSize(11).TextColor(c.Theme().TextMuted)
			}
			for _, row := range rows {
				toolboxDiffRow(c, row)
			}
		})
	})
}

func toolboxDiffRow(c *ui.Context, row desktopDiffRow) {
	label := "相同"
	badgeBG, badgeText := c.Theme().Surface, c.Theme().Text
	leftBG, rightBG := c.Theme().Background, c.Theme().Background
	leftText, rightText := c.Theme().Text, c.Theme().Text
	switch row.Kind {
	case "added":
		label, badgeBG, badgeText = "新增", ui.Hex("#e5f3ec"), ui.Hex("#276649")
		rightBG, rightText = badgeBG, badgeText
	case "removed":
		label, badgeBG, badgeText = "删除", ui.Hex("#fbeae8"), ui.Hex("#9e3b32")
		leftBG, leftText = badgeBG, badgeText
	case "changed":
		label, badgeBG, badgeText = "修改", ui.Hex("#fcf0d9"), ui.Hex("#875c19")
		leftBG, rightBG = ui.Hex("#fff4f2"), ui.Hex("#eef8f2")
	}
	ui.Row(c).FillWidth().AlignItems(ui.Stretch).BorderWidth(0, 0, 1, 0).BorderColor(toolboxDivider).Children(func() {
		ui.Row(c).Width(0).Grow(30).MinWidth(0).Padding(7, 9).Gap(7).AlignItems(ui.Start).Children(func() {
			ui.Text(c, label).Shrink(0).FontSize(11).Radius(2).Padding(1, 5).Background(badgeBG).TextColor(badgeText)
			ui.Text(c, row.Path).Width(0).Grow(1).MinWidth(0).Font("Cascadia Code").FontSize(11).LineHeight(1.6)
		})
		for i, value := range []*string{row.Before, row.After} {
			text := "（不存在）"
			if value != nil {
				text = *value
			}
			ui.Text(c, text).Width(0).Grow(35).MinWidth(0).Padding(7, 9).Font("Cascadia Code").
				FontSize(11).LineHeight(1.6).Background([]ui.Color{leftBG, rightBG}[i]).
				TextColor([]ui.Color{leftText, rightText}[i])
		}
	})
}

type desktopDateRow struct {
	label string
	value string
}

func desktopDateFormats(state desktopToolbox) ([]desktopDateRow, string, error) {
	input := strings.TrimSpace(state.input)
	zone := time.Local
	zoneName := zone.String()
	if state.zone == "UTC" {
		zone, zoneName = time.UTC, "UTC"
	}
	value := time.Now()
	if input != "" {
		mode := state.dateMode
		number, numberErr := strconv.ParseFloat(input, 64)
		if mode == "自动识别" && numberErr == nil {
			mode = "Unix 时间戳（秒）"
			if math.Abs(number) >= 1e11 {
				mode = "时间戳（毫秒）"
			}
		}
		if mode == "自动识别" && isMongoObjectID(input) {
			mode = "Mongo ObjectID"
		}
		switch mode {
		case "Unix 时间戳（秒）", "时间戳（毫秒）", "Excel 日期序列":
			if numberErr != nil || math.IsInf(number, 0) || math.IsNaN(number) {
				return nil, "", errors.New("时间戳必须是数字")
			}
			switch mode {
			case "Unix 时间戳（秒）":
				value = time.UnixMilli(int64(number * 1000))
			case "时间戳（毫秒）":
				value = time.UnixMilli(int64(number))
			case "Excel 日期序列":
				if number < 1 || number >= 2958466 {
					return nil, "", errors.New("Excel 日期序列值应在 1 到 2958466 之间（1900 日期系统）")
				}
				if math.Floor(number) == 60 {
					return nil, "", errors.New("Excel 序列 60 对应不存在的 1900-02-29")
				}
				adjusted := number
				if adjusted < 60 {
					adjusted++
				}
				epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
				days := int(math.Floor(adjusted))
				milliseconds := math.Round((adjusted - float64(days)) * 86400000)
				instant := epoch.AddDate(0, 0, days).Add(time.Duration(milliseconds) * time.Millisecond)
				if state.zone != "UTC" {
					wall := instant.UTC()
					value = time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), zone)
				} else {
					value = instant
				}
			}
		case "Mongo ObjectID":
			if !isMongoObjectID(input) {
				return nil, "", errors.New("Mongo ObjectID 应为 24 位十六进制字符串")
			}
			seconds, _ := strconv.ParseUint(input[:8], 16, 32)
			value = time.Unix(int64(seconds), 0)
		default:
			parsed, err := desktopParseDate(input, zone)
			if err != nil {
				return nil, "", err
			}
			value = parsed
		}
	}
	local := value.In(zone)
	utc := value.UTC()
	_, offset := local.Zone()
	offsetText := fmt.Sprintf("%+03d:%02d", offset/3600, int(math.Abs(float64(offset/60)))%60)
	iso := local.Format("2006-01-02T15:04:05")
	if local.Nanosecond() != 0 {
		iso += fmt.Sprintf(".%03d", local.Nanosecond()/1e6)
	}
	iso += offsetText
	if zone == time.UTC {
		iso = utc.Format("2006-01-02T15:04:05")
		if utc.Nanosecond() != 0 {
			iso += fmt.Sprintf(".%03d", utc.Nanosecond()/1e6)
		}
		iso += "Z"
	}
	objectID := ""
	if value.Unix() >= 0 && value.Unix() <= math.MaxUint32 {
		objectID = fmt.Sprintf("%08x0000000000000000", value.Unix())
	}
	excel := ""
	if local.Year() >= 1900 && local.Year() <= 9999 {
		epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		wall := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC)
		wholeDays := wall.Unix()/86400 - epoch.Unix()/86400
		fraction := float64(local.Hour()*3600+local.Minute()*60+local.Second())/86400 + float64(local.Nanosecond())/86400000000000
		serial := float64(wholeDays) + fraction
		if serial < 61 {
			serial--
		}
		excel = strconv.FormatFloat(serial, 'f', -1, 64)
	}
	rows := []desktopDateRow{
		{"JS locale date string", local.Format("Mon Jan 02 2006 15:04:05 GMT-0700 (MST)")},
		{"ISO 8601", iso}, {"ISO 9075", local.Format("2006-01-02 15:04:05")}, {"RFC 3339", iso},
		{"RFC 7231", utc.Format(time.RFC1123)}, {"Unix timestamp · 秒", strconv.FormatInt(int64(math.Floor(float64(value.UnixMilli())/1000)), 10)},
		{"Timestamp · 毫秒", strconv.FormatInt(value.UnixMilli(), 10)}, {"UTC format", utc.Format("Mon, 02 Jan 2006 15:04:05 GMT")},
		{"Mongo ObjectID · 时间前缀", objectID}, {"Excel date/time · 1900", excel},
	}
	context := fmt.Sprintf("%s · %s", zoneName, map[bool]string{true: "转换结果", false: "当前时间 · 实时刷新"}[input != ""])
	return rows, context, nil
}

func isMongoObjectID(input string) bool {
	if len(input) != 24 {
		return false
	}
	_, err := hex.DecodeString(input)
	return err == nil
}

func desktopParseDate(input string, zone *time.Location) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if value, err := time.Parse(layout, input); err == nil && hasExplicitTimezone(input) {
			return value, nil
		}
		if value, err := time.ParseInLocation(layout, input, zone); err == nil {
			return value, nil
		}
	}
	if strings.HasPrefix(input, "Mon ") || strings.HasPrefix(input, "Mon,") ||
		strings.HasPrefix(input, "Tue ") || strings.HasPrefix(input, "Tue,") ||
		strings.HasPrefix(input, "Wed ") || strings.HasPrefix(input, "Wed,") ||
		strings.HasPrefix(input, "Thu ") || strings.HasPrefix(input, "Thu,") ||
		strings.HasPrefix(input, "Fri ") || strings.HasPrefix(input, "Fri,") ||
		strings.HasPrefix(input, "Sat ") || strings.HasPrefix(input, "Sat,") ||
		strings.HasPrefix(input, "Sun ") || strings.HasPrefix(input, "Sun,") {
		if value, err := http.ParseTime(input); err == nil {
			return value, nil
		}
	}
	return time.Time{}, errors.New("请输入 ISO 日期、RFC 日期或数字时间戳，例如 2026-10-20 13:53:28")
}

func hasExplicitTimezone(input string) bool {
	if strings.HasSuffix(strings.ToUpper(input), "Z") {
		return true
	}
	return len(input) > 10 && (strings.Contains(input[10:], "+") || strings.Contains(input[10:], "-"))
}

func (t *desktopToolbox) visibleDiffRows() []desktopDiffRow {
	rows := make([]desktopDiffRow, 0, len(t.diffRows))
	for _, row := range t.diffRows {
		if t.onlyDiff && row.Kind == "same" {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func jwtDefaultHeader(algorithm string) string {
	data, _ := json.MarshalIndent(map[string]string{"alg": algorithm, "typ": "JWT"}, "", "  ")
	return string(data)
}

func (t *desktopToolbox) updateJWTHeader() {
	var header map[string]json.RawMessage
	if json.Unmarshal([]byte(t.jwtHeader), &header) != nil || header == nil {
		return
	}
	algorithm, _ := json.Marshal(t.algorithm)
	header["alg"] = algorithm
	encoded, err := json.MarshalIndent(header, "", "  ")
	if err == nil {
		t.jwtHeader = string(encoded)
	}
}

type desktopJWTClaim struct {
	text    string
	warning bool
}

func jwtTimeClaims(payload map[string]any) []desktopJWTClaim {
	labels := map[string]string{"iat": "签发时间", "nbf": "生效时间", "exp": "过期时间"}
	now := float64(time.Now().UnixNano()) / 1e9
	var claims []desktopJWTClaim
	for _, key := range []string{"iat", "nbf", "exp"} {
		value, ok := payload[key]
		if !ok {
			continue
		}
		number, valid := value.(float64)
		if !valid {
			if integer, yes := value.(json.Number); yes {
				number, _ = integer.Float64()
				valid = true
			}
		}
		warning := !valid || math.IsInf(number, 0) || math.IsNaN(number) || math.Abs(number) > 8.64e12
		formatted := "无效的 Unix 秒时间戳"
		if !warning {
			at := time.UnixMilli(int64(number * 1000))
			if key == "exp" && number <= now {
				warning = true
			}
			if key == "nbf" && number > now {
				warning = true
			}
			formatted = at.Local().Format("2006/01/02 15:04:05")
			if key == "exp" && number <= now {
				formatted += " · 已过期"
			}
			if key == "nbf" && number > now {
				formatted += " · 尚未生效"
			}
		}
		claims = append(claims, desktopJWTClaim{
			text: fmt.Sprintf("%s · %s：%s", key, labels[key], formatted), warning: warning,
		})
	}
	return claims
}
