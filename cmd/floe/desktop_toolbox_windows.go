package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"maps"
	"math/big"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

var desktopToolNames = []string{"JWT", "Base64", "URL Encode", "Date / Time", "JSON", "JSON Diff"}

var desktopToolSubtitles = []string{"解析与生成", "编码与解码", "编码与解码", "日期时间转换", "解析与 jq 过滤", "结构差异比较"}

var desktopToolTitles = []string{"JWT 解析与生成", "Base64 编码与解码", "URL 编码与解码", "日期时间转换", "JSON 解析与 jq 过滤", "JSON 差异比较"}

type desktopToolbox struct {
	selected                                       int
	input, other, output, status, variant          string
	mode, jwtHeader, jwtPayload, algorithm, secret string
	dateMode, zone, jq                             string
	busy, onlyDiff, sortKeys, unwrapJSON           bool
	indent                                         float64
	statusKind                                     string
	claims                                         []desktopJWTClaim
	diffRows                                       []desktopDiffRow
	revision                                       uint64
	timer                                          *time.Timer
	cancel                                         context.CancelFunc
}

func (w *desktopWorkspace) toolboxView(c *ui.Context) {
	state := &w.toolbox[w.toolboxSelected]
	if state.algorithm == "" {
		state.selected = w.toolboxSelected
		state.algorithm, state.mode = "HS256", "解析 / 验签"
		state.dateMode, state.zone = "自动识别", "本地时区"
		state.indent, state.unwrapJSON, state.onlyDiff = 2, true, true
		state.jwtHeader = jwtDefaultHeader(state.algorithm)
		switch state.selected {
		case 1:
			state.variant = "标准 Base64"
		case 2:
			state.variant = "URL 参数值（%20）"
		}
	}
	width, height := c.Size()
	height = max(0, min(height, w.queueHeight)-32)
	navWidth, gutter, inset, toolbarGap := float32(150), float32(12), float32(10), float32(10)
	if w.queueExpanded {
		_, fullHeight := c.Size()
		height = fullHeight - 32
		navWidth, gutter, inset = 185, 22, 18
	}
	if width <= 850 {
		navWidth, gutter, inset, toolbarGap = 125, 8, 8, 6
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Background(c.Theme().Background).Children(func() {
		ui.Scroll(c).Width(navWidth).Shrink(0).MinHeight(0).Padding(8, 0).
			Background(ui.Hex("#f8fafb")).BorderWidth(0, 1, 0, 0).BorderColor(c.Theme().Border).
			Children(func() {
				for index, name := range desktopToolNames {
					selected := index == w.toolboxSelected
					button := ui.Button(c, "").Column().Label(name).FillWidth().Height(63).Shrink(0).
						Padding(9, 14).Gap(4).AlignItems(ui.Start).Justify(ui.Center).
						Background(ui.Transparent).BorderWidth(0, 0, 0, 3).
						BorderColor(ui.Transparent).AccessSelected(selected)
					color := c.Theme().Text
					if selected {
						button.Background(c.Theme().Selection).BorderColor(c.Theme().Accent)
						color = c.Theme().Accent
					} else if button.Hovered() {
						button.Background(c.Theme().SurfaceHover)
					}
					button.Children(func() {
						ui.Text(c, name).Font("Cascadia Code").FontSize(12).FontWeight(600).TextColor(color).SingleLine()
						ui.Text(c, desktopToolSubtitles[index]).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
					})
					if button.Clicked() && !selected {
						w.toolbox[w.toolboxSelected].cancelWork()
						w.toolboxSelected = index
						c.Invalidate()
					}
				}
			})
		ui.Column(c).Width(0).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Height(36).Shrink(0).Gap(toolbarGap).Padding(0, gutter).AlignItems(ui.Center).
				BorderWidth(0, 0, 1, 0).BorderColor(toolboxDivider).Children(func() {
				ui.Text(c, desktopToolTitles[state.selected]).FontSize(12).FontWeight(600).SingleLine()
				switch state.selected {
				case 0:
					if toolboxSelect(c, &state.mode, "JWT 操作", "解析 / 验签", "生成").Changed() {
						state.invalidate()
					}
				case 1:
					if toolboxSelect(c, &state.variant, "编码格式", "标准 Base64", "Base64URL").Changed() {
						state.invalidate()
					}
				case 2:
					if toolboxSelect(c, &state.variant, "编码格式", "URL 参数值（%20）", "表单参数值（+）").Changed() {
						state.invalidate()
					}
				}
				ui.Box(c).Grow(1)
				if toolboxButton(c, "清空").Clicked() {
					state.invalidate()
					state.input, state.other, state.secret, state.jwtPayload, state.jq = "", "", "", "", ""
					state.jwtHeader = jwtDefaultHeader(state.algorithm)
				}
				icon, label := iconMaximize, "展开工具箱"
				if w.queueExpanded {
					icon, label = iconClose, "退出全屏"
				}
				if desktopIconButton(c, icon, label, false).Size(27, 25).Clicked() {
					w.queueExpanded = !w.queueExpanded
				}
			})
			if state.selected >= 3 {
				w.toolboxDataView(c, state, max(0, height-62), width)
			} else {
				w.toolboxCodecView(c, state, max(0, height-67), gutter, inset)
			}
		})
	})
}

func (w *desktopWorkspace) toolboxCodecView(c *ui.Context, state *desktopToolbox, height, gutter, inset float32) {
	jwt := state.selected == 0
	generate := jwt && state.mode == "生成"
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Width(0).Grow(1).MinWidth(0).MinHeight(0).
			BorderWidth(0, 0, 0, 1).BorderColor(toolboxDivider).Children(func() {
			minimum := float32(140)
			if jwt {
				minimum += 45
			}
			if generate {
				minimum += 50
			}
			ui.Column(c).FillWidth().Height(max(height, minimum)).Padding(inset, gutter).Gap(8).
				AlignItems(ui.Stretch).Children(func() {
				if generate {
					toolboxEditor(c, "Header · JSON 对象", &state.jwtHeader, "", 58, .65, state.invalidate)
					toolboxEditor(c, "Payload · JSON 对象", &state.jwtPayload, `{"sub":"user-123"}`, 75, 1, state.invalidate)
				} else {
					placeholder := "输入待转换的文本"
					if jwt {
						placeholder = "粘贴 JWT：eyJ… . eyJ… . …"
					}
					toolboxEditor(c, "输入", &state.input, placeholder, 95, 1, state.invalidate)
				}
				if jwt {
					ui.Row(c).Gap(8).Children(func() {
						ui.Column(c).Gap(4).Children(func() {
							toolboxFieldLabel(c, "算法")
							if toolboxSelect(c, &state.algorithm, "签名算法", "HS256", "HS384", "HS512").Changed() {
								state.updateJWTHeader()
								state.invalidate()
							}
						})
						ui.Column(c).Width(0).Grow(1).MinWidth(0).Gap(4).AlignItems(ui.Stretch).Children(func() {
							toolboxFieldLabel(c, "密钥 · UTF-8")
							if ui.TextInput(c, &state.secret).Password().Height(25).Padding(0, 6).
								Font("Cascadia Code").FontSize(12).Label("JWT 签名密钥").Placeholder("生成或验签时填写").Changed() {
								state.invalidate()
							}
						})
					})
				}
				ui.Row(c).Gap(6).Children(func() {
					action, label := "编码", "编码"
					if jwt {
						action, label = "解析", "解析"
					}
					if generate {
						action, label = "生成", "生成 JWT"
					}
					if ui.PrimaryButton(c, label).Height(25).MinWidth(68).Padding(3, 9).
						FontSize(11).Disabled(state.busy).Clicked() {
						w.runTool(state, action, 0)
					}
					if jwt && !generate && toolboxButton(c, "验证签名").Disabled(state.busy).Clicked() {
						w.runTool(state, "验证签名", 0)
					}
					if !jwt && toolboxButton(c, "解码").Disabled(state.busy).Clicked() {
						w.runTool(state, "解码", 0)
					}
				})
			})
		})
		ui.Scroll(c).Width(0).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Height(max(height, 128+2*inset+float32(len(state.claims))*18)).
				Padding(inset, gutter).Gap(6).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Height(25).Gap(6).AlignItems(ui.Center).Children(func() {
					label := "转换结果"
					if jwt {
						label = "解析结果"
					}
					if generate {
						label = "JWT"
					}
					ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted).Grow(1)
					if (!jwt || generate) && toolboxButton(c, "结果作为输入").Clicked() && state.output != "" {
						state.input = state.output
						if generate {
							state.mode = "解析 / 验签"
						}
						state.invalidate()
						c.Invalidate()
					}
					if toolboxButton(c, "复制结果").Clicked() && state.output != "" {
						c.WriteClipboard(state.output)
						w.notify("结果已复制", false)
					}
				})
				toolboxOutput(c, &state.output, "处理结果将显示在这里")
				for _, claim := range state.claims {
					color := c.Theme().TextMuted
					if claim.warning {
						color = ui.Hex("#906010")
					}
					ui.Text(c, claim.text).Font("Cascadia Code").FontSize(11).LineHeight(1.65).TextColor(color)
				}
				toolboxStatus(c, state, false)
			})
		})
	})
	help := "URL 参数值模式：空格编码为 %20，解码时保留字面量 +。请输入单个参数值。"
	switch {
	case generate:
		help = "Header 和 Payload 使用 JSON 对象；密钥按 UTF-8 文本处理。"
	case jwt:
		help = "解析可查看任意 JWT 的内容；选择预期算法并填写密钥后可验证 HMAC 签名。"
	case state.selected == 1:
		help = "按 UTF-8 转换文本，支持中文和 Emoji；Base64URL 使用 -、_，编码结果省略末尾填充。"
	case state.variant == "表单参数值（+）":
		help = "表单参数值模式：空格编码为 +，字面量 + 编码为 %2B；解码时 + 表示空格。"
	}
	vertical := float32(7)
	if w.queueExpanded {
		vertical = 9
	}
	ui.Text(c, help).FillWidth().Shrink(0).Padding(vertical, gutter).LineHeight(1.5).
		BorderWidth(1, 0, 0, 0).BorderColor(toolboxDivider).FontSize(11).TextColor(c.Theme().TextMuted)
}

func (t *desktopToolbox) invalidate() {
	t.cancelWork()
	t.busy, t.status, t.statusKind, t.output = false, "", "", ""
	t.claims, t.diffRows = nil, nil
}

func (t *desktopToolbox) cancelWork() {
	t.revision++
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	t.busy = false
}

func (w *desktopWorkspace) runTool(state *desktopToolbox, action string, delay time.Duration) {
	state.invalidate()
	if (state.selected == 4 || state.selected == 5) && strings.TrimSpace(state.input) == "" ||
		state.selected == 5 && strings.TrimSpace(state.other) == "" {
		return
	}
	ctx, cancel := context.WithCancel(w.ctx)
	state.cancel = cancel
	snapshot := *state
	run := func() {
		defer cancel()
		var result, status, kind string
		var claims []desktopJWTClaim
		var diff []desktopDiffRow
		var err error
		switch snapshot.selected {
		case 0:
			result, status, kind, claims, err = snapshot.jwt(action)
		case 1, 2:
			result, err = snapshot.codec(action)
			status, kind = "转换完成", "success"
		case 4:
			result, err = w.toolJSON(ctx, snapshot, action == "压缩")
			status, kind = "格式化完成", "success"
			if strings.TrimSpace(snapshot.jq) != "" && strings.TrimSpace(snapshot.jq) != "." {
				status = "过滤完成"
				if result == "" {
					status = "过滤完成 · 无匹配结果"
				}
			}
		case 5:
			diff, err = snapshot.diffJSON()
			status, kind = "比较完成", "success"
		}
		w.window.Update(func() {
			if state.revision != snapshot.revision {
				return
			}
			state.busy, state.timer, state.cancel = false, nil, nil
			if err != nil {
				state.status, state.statusKind = err.Error(), "error"
				return
			}
			state.output, state.status, state.statusKind = result, status, kind
			state.claims, state.diffRows = claims, diff
		})
	}
	if delay > 0 {
		state.timer = time.AfterFunc(delay, run)
	} else {
		state.busy = true
		if state.selected < 3 {
			state.status = "正在处理…"
		} else if state.selected == 4 && strings.TrimSpace(state.jq) != "" && strings.TrimSpace(state.jq) != "." {
			state.status = "正在执行 jq…"
		}
		go run()
	}
}

func (t desktopToolbox) jwt(action string) (string, string, string, []desktopJWTClaim, error) {
	hashers := map[string]func() hash.Hash{"HS256": sha256.New, "HS384": sha512.New384, "HS512": sha512.New}
	hashFunction := hashers[t.algorithm]
	base := base64.RawURLEncoding
	sign := func(message string) []byte {
		mac := hmac.New(hashFunction, []byte(t.secret))
		_, _ = mac.Write([]byte(message))
		return mac.Sum(nil)
	}
	if action == "生成" {
		if t.secret == "" || hashFunction == nil {
			return "", "", "error", nil, errors.New("请选择算法并填写签名密钥")
		}
		var header, payload map[string]json.RawMessage
		if json.Unmarshal([]byte(t.jwtHeader), &header) != nil || header == nil ||
			json.Unmarshal([]byte(t.jwtPayload), &payload) != nil || payload == nil {
			return "", "", "error", nil, errors.New("Header 和 Payload 必须是 JSON 对象")
		}
		var metadata struct {
			Algorithm string          `json:"alg"`
			Critical  json.RawMessage `json:"crit"`
			Unencoded json.RawMessage `json:"b64"`
		}
		if json.Unmarshal([]byte(t.jwtHeader), &metadata) != nil || metadata.Algorithm != t.algorithm {
			return "", "", "error", nil, errors.New("Header 的 alg 必须与选择的算法一致")
		}
		if len(metadata.Critical) > 0 || len(metadata.Unencoded) > 0 {
			return "", "", "error", nil, errors.New("暂不支持 crit 或 b64 扩展 Header")
		}
		algorithm, _ := json.Marshal(t.algorithm)
		header["alg"] = algorithm
		encodedHeader, _ := json.Marshal(header)
		encodedPayload, _ := json.Marshal(payload)
		token := base.EncodeToString(encodedHeader) + "." + base.EncodeToString(encodedPayload)
		return token + "." + base.EncodeToString(sign(token)), "JWT 已生成", "success", nil, nil
	}
	token := strings.TrimSpace(t.input)
	parts := strings.Split(token, ".")
	if strings.ContainsAny(token, " \t\r\n") || len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return "", "", "error", nil, errors.New("JWT 应为 Header.Payload.Signature 三段式文本")
	}
	for _, part := range parts {
		if strings.ContainsAny(part, "=+/") {
			return "", "", "error", nil, errors.New("JWT 分段必须使用无填充的 Base64URL 编码")
		}
	}
	header, err := base.DecodeString(parts[0])
	if err != nil || !json.Valid(header) || !jsonObjectPayload(header) {
		return "", "", "error", nil, errors.New("JWT Header 无效")
	}
	payload, err := base.DecodeString(parts[1])
	if err != nil || !json.Valid(payload) || !jsonObjectPayload(payload) {
		return "", "", "error", nil, errors.New("JWT Payload 无效")
	}
	status, kind := "解析成功 · 签名尚未验证", "info"
	if action == "验证签名" {
		var metadata struct {
			Algorithm string `json:"alg"`
		}
		if json.Unmarshal(header, &metadata) != nil || metadata.Algorithm != t.algorithm || hashFunction == nil || t.secret == "" {
			return "", "", "error", nil, errors.New("签名算法不匹配或密钥为空")
		}
		signature, err := base.DecodeString(parts[2])
		if err != nil || !hmac.Equal(signature, sign(parts[0]+"."+parts[1])) {
			return "", "", "error", nil, errors.New("签名无效")
		}
		status, kind = "签名验证通过", "success"
	}
	var claimsObject map[string]any
	if json.Unmarshal(payload, &claimsObject) != nil {
		return "", "", "error", nil, errors.New("JWT Payload 无效")
	}
	claims := jwtTimeClaims(claimsObject)
	for _, claim := range claims {
		if claim.warning && kind != "error" {
			status += " · 时间声明异常，见上方"
			kind = "warning"
			break
		}
	}
	result, _ := json.MarshalIndent(map[string]any{
		"header": json.RawMessage(header), "payload": json.RawMessage(payload),
	}, "", "  ")
	return string(result), status, kind, claims, nil
}

func jsonObjectPayload(data []byte) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(data, &value) == nil && value != nil
}

func (t desktopToolbox) codec(action string) (string, error) {
	if t.selected == 1 {
		encoding := base64.StdEncoding
		if t.variant == "Base64URL" {
			encoding = base64.RawURLEncoding
		}
		if action == "编码" {
			return encoding.EncodeToString([]byte(t.input)), nil
		}
		encodedInput := strings.Join(strings.Fields(t.input), "")
		decoded, err := encoding.DecodeString(encodedInput)
		if err != nil && t.variant == "标准 Base64" && len(encodedInput)%4 != 1 {
			decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(encodedInput, "="))
		}
		if err != nil && t.variant == "Base64URL" {
			decoded, err = base64.URLEncoding.DecodeString(strings.TrimSpace(t.input))
		}
		if err != nil {
			return "", fmt.Errorf("无效的 %s 编码，请检查字符和末尾填充", t.variant)
		}
		if !utf8.Valid(decoded) {
			return "", errors.New("解码结果不是有效的 UTF-8 文本，此工具用于文本转换")
		}
		return string(decoded), nil
	}
	if action == "解码" {
		var decoded string
		var err error
		if t.variant == "表单参数值（+）" {
			decoded, err = url.QueryUnescape(t.input)
		} else {
			decoded, err = url.PathUnescape(t.input)
		}
		if err != nil {
			return "", errors.New("URL 编码无效，请检查 % 后的十六进制字符及 UTF-8 字节")
		}
		if !utf8.ValidString(decoded) {
			return "", errors.New("URL 编码无效，请检查 % 后的十六进制字符及 UTF-8 字节")
		}
		return decoded, nil
	}
	if !utf8.ValidString(t.input) {
		return "", errors.New("输入包含无效的 Unicode 字符")
	}
	var encoded strings.Builder
	for _, value := range []byte(t.input) {
		character := byte(value)
		allowed := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("-_.!~*'()", rune(character))
		if t.variant == "表单参数值（+）" && strings.ContainsRune("!'()~", rune(character)) {
			allowed = false
		}
		if character == ' ' && t.variant == "表单参数值（+）" {
			encoded.WriteByte('+')
		} else if allowed {
			encoded.WriteByte(character)
		} else {
			fmt.Fprintf(&encoded, "%%%02X", character)
		}
	}
	return encoded.String(), nil
}

func (w *desktopWorkspace) toolJSON(ctx context.Context, t desktopToolbox, compact bool) (string, error) {
	document := strings.TrimSpace(t.input)
	if t.unwrapJSON && strings.HasPrefix(document, "\"") {
		var nested string
		if json.Unmarshal([]byte(document), &nested) == nil && json.Valid([]byte(nested)) {
			document = nested
		}
	}
	if !json.Valid([]byte(document)) {
		return "", errors.New("输入不是有效的 JSON")
	}
	items := []string{document}
	if query := strings.TrimSpace(t.jq); query != "" && query != "." {
		var result struct{ Results []string }
		body := map[string]any{"input": json.RawMessage(document), "query": query}
		if err := w.client.request(ctx, http.MethodPost, "/api/v1/tools/jq", body, &result); err != nil {
			return "", err
		}
		items = result.Results
	}
	for i, item := range items {
		data := []byte(item)
		if t.sortKeys {
			var decoded any
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			if err := decoder.Decode(&decoded); err != nil {
				return "", err
			}
			data, _ = json.Marshal(decoded)
		}
		var output bytes.Buffer
		if compact {
			_ = json.Compact(&output, data)
		} else {
			_ = json.Indent(&output, data, "", strings.Repeat(" ", min(8, max(0, int(t.indent)))))
		}
		items[i] = output.String()
	}
	return strings.Join(items, "\n"), nil
}

func (t desktopToolbox) diffJSON() ([]desktopDiffRow, error) {
	left, err := decodeDiffJSON(t.input)
	if err != nil {
		return nil, errors.New("原始 JSON 不是有效的 JSON")
	}
	right, err := decodeDiffJSON(t.other)
	if err != nil {
		return nil, errors.New("比较 JSON 不是有效的 JSON")
	}
	var rows []desktopDiffRow
	var walk func(string, any, any, bool, bool) error
	walk = func(path string, before, after any, hasBefore, hasAfter bool) error {
		if len(rows) >= 20000 {
			return errors.New("比较结果超过 20000 项，请缩小输入范围")
		}
		if !hasBefore {
			rows = append(rows, desktopDiffRow{Kind: "added", Path: path, After: diffValuePointer(after)})
			return nil
		}
		if !hasAfter {
			rows = append(rows, desktopDiffRow{Kind: "removed", Path: path, Before: diffValuePointer(before)})
			return nil
		}
		leftObject, leftIsObject := before.(map[string]any)
		rightObject, rightIsObject := after.(map[string]any)
		leftArray, leftIsArray := before.([]any)
		rightArray, rightIsArray := after.([]any)
		if leftIsObject && rightIsObject {
			keys := make(map[string]bool, len(leftObject)+len(rightObject))
			for key := range leftObject {
				keys[key] = true
			}
			for key := range rightObject {
				keys[key] = true
			}
			if len(keys) == 0 {
				rows = append(rows, desktopDiffRow{Kind: "same", Path: path, Before: diffValuePointer(before), After: diffValuePointer(after)})
				return nil
			}
			for _, key := range slices.Sorted(maps.Keys(keys)) {
				old, oldOK := leftObject[key]
				next, nextOK := rightObject[key]
				child := diffJSONPath(path, key)
				if err := walk(child, old, next, oldOK, nextOK); err != nil {
					return err
				}
			}
			return nil
		}
		if leftIsArray && rightIsArray {
			if max(len(leftArray), len(rightArray)) == 0 {
				rows = append(rows, desktopDiffRow{Kind: "same", Path: path, Before: diffValuePointer(before), After: diffValuePointer(after)})
				return nil
			}
			for index := 0; index < max(len(leftArray), len(rightArray)); index++ {
				var old, next any
				oldOK, nextOK := index < len(leftArray), index < len(rightArray)
				if oldOK {
					old = leftArray[index]
				}
				if nextOK {
					next = rightArray[index]
				}
				if err := walk(fmt.Sprintf("%s[%d]", path, index), old, next, oldOK, nextOK); err != nil {
					return err
				}
			}
			return nil
		}
		kind := "changed"
		if diffValuesEqual(before, after) {
			kind = "same"
		}
		rows = append(rows, desktopDiffRow{Kind: kind, Path: path, Before: diffValuePointer(before), After: diffValuePointer(after)})
		return nil
	}
	if err := walk("$", left, right, true, true); err != nil {
		return nil, err
	}
	return rows, nil
}

func decodeDiffJSON(document string) (any, error) {
	if !json.Valid([]byte(document)) {
		return nil, errors.New("invalid JSON")
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(document))
	decoder.UseNumber()
	return value, decoder.Decode(&value)
}

func diffJSONPath(path, key string) string {
	if key != "" {
		first := key[0]
		valid := first == '_' || first == '$' || first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z'
		for index := 1; valid && index < len(key); index++ {
			value := key[index]
			valid = value == '_' || value == '$' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
		}
		if valid {
			return path + "." + key
		}
	}
	return path + "[" + strconv.Quote(key) + "]"
}

func diffValuesEqual(left, right any) bool {
	leftNumber, leftIsNumber := left.(json.Number)
	rightNumber, rightIsNumber := right.(json.Number)
	if leftIsNumber && rightIsNumber {
		leftSign, leftDigits, leftExponent := normalizedJSONNumber(string(leftNumber))
		rightSign, rightDigits, rightExponent := normalizedJSONNumber(string(rightNumber))
		return leftSign == rightSign && leftDigits == rightDigits && leftExponent.Cmp(rightExponent) == 0
	}
	return reflect.DeepEqual(left, right)
}

func normalizedJSONNumber(value string) (string, string, *big.Int) {
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign, value = "-", value[1:]
	}
	parts := strings.SplitN(value, "e", 2)
	if len(parts) == 1 {
		parts = strings.SplitN(value, "E", 2)
	}
	exponent := new(big.Int)
	if len(parts) == 2 {
		exponent.SetString(parts[1], 10)
	}
	mantissa := parts[0]
	fraction := 0
	if point := strings.IndexByte(mantissa, '.'); point >= 0 {
		fraction = len(mantissa) - point - 1
		mantissa = mantissa[:point] + mantissa[point+1:]
	}
	digits := strings.TrimLeft(mantissa, "0")
	if digits == "" {
		return "", "0", new(big.Int)
	}
	trimmed := strings.TrimRight(digits, "0")
	exponent.Sub(exponent, big.NewInt(int64(fraction)))
	exponent.Add(exponent, big.NewInt(int64(len(digits)-len(trimmed))))
	return sign, trimmed, exponent
}

func diffValuePointer(value any) *string {
	text := diffValue(value)
	return &text
}

type desktopDiffRow struct {
	Kind   string
	Path   string
	Before *string
	After  *string
}

func diffValue(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}
