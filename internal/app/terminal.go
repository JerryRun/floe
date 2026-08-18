package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"floe/internal/core"
)

const (
	askPassLifetime         = 90 * time.Second
	terminalAskPassLifetime = 12 * time.Hour
)

type askPassSecret struct {
	Password     string
	ExpiresAt    time.Time
	ConsumeOnUse bool
}

type terminalRequest struct {
	Kind      string `json:"kind"`
	Provider  string `json:"provider"`
	CWD       string `json:"cwd"`
	Placement string `json:"placement"`
	Tab       int    `json:"tab"`
}

func (s *Server) issueAskPass(password string) (string, string) {
	return s.issueAskPassToken(password, askPassLifetime, true)
}

func (s *Server) issueTerminalAskPass(password string) (string, string) {
	return s.issueAskPassToken(password, terminalAskPassLifetime, false)
}

func (s *Server) issueAskPassToken(password string, lifetime time.Duration, consumeOnUse bool) (string, string) {
	now := time.Now()
	token := randomToken(32)
	s.askPassMu.Lock()
	for key, secret := range s.askPassTokens {
		if !secret.ExpiresAt.After(now) {
			delete(s.askPassTokens, key)
		}
	}
	s.askPassTokens[token] = askPassSecret{Password: password, ExpiresAt: now.Add(lifetime), ConsumeOnUse: consumeOnUse}
	s.askPassMu.Unlock()
	return token, s.origin + "/askpass/" + token
}

func (s *Server) revokeAskPass(token string) {
	s.askPassMu.Lock()
	delete(s.askPassTokens, token)
	s.askPassMu.Unlock()
}

func (s *Server) consumeAskPass(token string) (string, bool) {
	s.askPassMu.Lock()
	defer s.askPassMu.Unlock()
	secret, ok := s.askPassTokens[token]
	if !ok || !secret.ExpiresAt.After(time.Now()) {
		delete(s.askPassTokens, token)
		return "", false
	}
	if secret.ConsumeOnUse {
		delete(s.askPassTokens, token)
	}
	return secret.Password, true
}

func (s *Server) askPass(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "loopback access required", http.StatusForbidden)
		return
	}
	password, ok := s.consumeAskPass(r.PathValue("token"))
	if !ok {
		http.Error(w, "invalid or expired askpass token", http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, password)
}

func (s *Server) revokeAskPassHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "loopback access required", http.StatusForbidden)
		return
	}
	s.revokeAskPass(r.PathValue("token"))
	w.WriteHeader(http.StatusNoContent)
}

// RunAskPass serves the short-lived SSH_ASKPASS child-process mode. It must be
// called before flag parsing and single-instance checks in Floe.exe.
func RunAskPass(stdout, stderr io.Writer) (handled bool, exitCode int) {
	endpoint := strings.TrimSpace(os.Getenv("FLOE_ASKPASS_URL"))
	if endpoint == "" {
		return false, 0
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" {
		fmt.Fprintln(stderr, "Floe AskPass endpoint is invalid")
		return true, 1
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			fmt.Fprintln(stderr, "Floe AskPass only permits loopback endpoints")
			return true, 1
		}
	}
	prompt := strings.ToLower(strings.Join(os.Args[1:], " "))
	if strings.Contains(prompt, "yes/no") || strings.Contains(prompt, "authenticity") {
		return true, 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "Floe AskPass request failed:", err)
		return true, 1
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "Floe AskPass token is invalid or expired")
		return true, 1
	}
	password, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		fmt.Fprintln(stderr, "Floe AskPass response failed:", err)
		return true, 1
	}
	_, _ = fmt.Fprintln(stdout, string(password))
	return true, 0
}

func (s *Server) openTerminal(w http.ResponseWriter, r *http.Request) {
	var req terminalRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Kind == "powershell" {
		if err := LaunchPowerShell(req.CWD); err != nil {
			writeError(w, http.StatusBadGateway, "TERMINAL_FAILED", "无法启动 Windows Terminal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if req.Kind != "ssh" {
		writeError(w, http.StatusBadRequest, "TERMINAL_KIND_INVALID", "不支持的 Terminal 类型", "")
		return
	}
	target, connected := s.manager.Target(req.Provider)
	if !connected {
		writeError(w, http.StatusBadRequest, "SSH_TARGET_MISSING", "该面板不是已连接的 SFTP 会话", "")
		return
	}
	saved, err := s.sessionStore.Request(req.Provider)
	if err != nil || saved.Protocol != "sftp" {
		writeError(w, http.StatusBadRequest, "SSH_SESSION_MISSING", "找不到该 SSH 会话配置", errorDetail(err))
		return
	}
	// The Core has already connected with the session's trusted SHA-256 host
	// fingerprint. Let OpenSSH add a previously unknown key to its own
	// known_hosts, while still rejecting a changed key on later connections.
	sshArgs := buildSSHArgs(saved, target)

	var token, endpoint string
	if boolValue(saved.TerminalAutoPassword, true) && saved.Password != "" {
		token, endpoint = s.issueTerminalAskPass(saved.Password)
	}
	title := terminalTitle(saved, target)
	scriptPath, err := s.writeSSHScript(title, endpoint, sshArgs)
	if err != nil {
		if token != "" {
			s.revokeAskPass(token)
		}
		writeError(w, http.StatusInternalServerError, "TERMINAL_SCRIPT_FAILED", "无法准备 SSH 启动脚本", err.Error())
		return
	}
	args, err := buildTerminalArgs(req, title, scriptPath, preferredPowerShell())
	if err != nil {
		_ = os.Remove(scriptPath)
		if token != "" {
			s.revokeAskPass(token)
		}
		writeError(w, http.StatusBadRequest, "TERMINAL_PLACEMENT_INVALID", "终端打开位置无效", err.Error())
		return
	}
	if err := startWindowsTerminal(args); err != nil {
		_ = os.Remove(scriptPath)
		if token != "" {
			s.revokeAskPass(token)
		}
		s.activity.Add("error", "terminal", "SSH 终端启动失败", err.Error())
		writeError(w, http.StatusBadGateway, "TERMINAL_FAILED", "无法启动 Windows Terminal", err.Error())
		return
	}
	s.activity.Add("info", "terminal", "已打开 SSH 终端", target.User+"@"+target.Host)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "askpass": token != ""})
}

func terminalTitle(saved core.ConnectRequest, target core.ConnectionTarget) string {
	if name := strings.TrimSpace(saved.Name); name != "" {
		return name
	}
	return target.User + "@" + target.Host
}

func buildSSHArgs(saved core.ConnectRequest, target core.ConnectionTarget) []string {
	args := []string{"-o", "StrictHostKeyChecking=accept-new"}
	if saved.SSHKeepAlive {
		interval := saved.ServerAliveInterval
		if interval == 0 {
			interval = core.DefaultServerAliveInterval
		}
		countMax := saved.ServerAliveCountMax
		if countMax == 0 {
			countMax = core.DefaultServerAliveCountMax
		}
		args = append(args,
			"-o", "ServerAliveInterval="+strconv.Itoa(interval),
			"-o", "ServerAliveCountMax="+strconv.Itoa(countMax),
		)
	}
	if target.Port != 22 {
		args = append(args, "-p", strconv.Itoa(target.Port))
	}
	if saved.PrivateKey != "" && (saved.AuthMethod == "key" || saved.AuthMethod == "") {
		args = append(args, "-i", saved.PrivateKey)
	}
	return append(args, target.User+"@"+target.Host)
}

func buildTerminalArgs(req terminalRequest, title, scriptPath, shellPath string) ([]string, error) {
	args := []string{"-w", "0"}
	switch req.Placement {
	case "", "new-tab":
		args = append(args, "new-tab")
	case "split-right":
		args = append(args, "split-pane", "-V")
	case "split-down":
		args = append(args, "split-pane", "-H")
	case "tab-right", "tab-down":
		if req.Tab < 1 || req.Tab > 100 {
			return nil, errors.New("指定标签编号必须在 1–100 之间")
		}
		orientation := "-V"
		if req.Placement == "tab-down" {
			orientation = "-H"
		}
		args = append(args, "focus-tab", "--target", strconv.Itoa(req.Tab-1), ";", "split-pane", orientation)
	default:
		return nil, fmt.Errorf("unsupported placement %q", req.Placement)
	}
	if req.CWD != "" {
		args = append(args, "-d", req.CWD)
	}
	args = append(args, "--title", title, shellPath, "-NoLogo", "-NoExit", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	return args, nil
}

func (s *Server) writeSSHScript(title, askPassURL string, sshArgs []string) (string, error) {
	directory := filepath.Join(s.dataDir, "terminal")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	entries, _ := os.ReadDir(directory)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "ssh-") || !strings.HasSuffix(entry.Name(), ".ps1") {
			continue
		}
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(directory, entry.Name()))
		}
	}
	file, err := os.CreateTemp(directory, "ssh-*.ps1")
	if err != nil {
		return "", err
	}
	path := file.Name()
	_ = file.Chmod(0o600)
	executable, err := os.Executable()
	if err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	var script strings.Builder
	script.WriteString("\uFEFF")
	script.WriteString("$ErrorActionPreference = 'Stop'\r\n")
	if title != "" {
		script.WriteString("$Host.UI.RawUI.WindowTitle = '")
		script.WriteString(powerShellSingleQuoted(title))
		script.WriteString("'\r\n")
	}
	script.WriteString("$revokeAskPassURL = ''\r\n")
	if askPassURL != "" {
		script.WriteString("$env:SSH_ASKPASS = '")
		script.WriteString(powerShellSingleQuoted(executable))
		script.WriteString("'\r\n$env:SSH_ASKPASS_REQUIRE = 'force'\r\n$env:DISPLAY = 'floe'\r\n$env:FLOE_ASKPASS_URL = '")
		script.WriteString(powerShellSingleQuoted(askPassURL))
		script.WriteString("'\r\n$revokeAskPassURL = '")
		script.WriteString(powerShellSingleQuoted(askPassURL + "/revoke"))
		script.WriteString("'\r\n")
	}
	script.WriteString("$sshArgs = @(")
	for index, arg := range sshArgs {
		if index > 0 {
			script.WriteString(", ")
		}
		script.WriteString("'")
		script.WriteString(powerShellSingleQuoted(arg))
		script.WriteString("'")
	}
	script.WriteString(")\r\n")
	script.WriteString("try {\r\n")
	script.WriteString("  while ($true) {\r\n")
	script.WriteString("    & ssh.exe @sshArgs\r\n")
	script.WriteString("    $exitCode = $LASTEXITCODE\r\n")
	script.WriteString("    Write-Host ''\r\n")
	script.WriteString("    if ($exitCode -eq 0) { Write-Host 'SSH 会话已结束。' } else { Write-Host \"SSH 连接已断开或退出，退出码 $exitCode。\" }\r\n")
	script.WriteString("    Write-Host '按 R 立即重连；按 Enter、Esc 或其他键结束。' -NoNewline\r\n")
	script.WriteString("    $key = [Console]::ReadKey($true)\r\n")
	script.WriteString("    Write-Host ''\r\n")
	script.WriteString("    if ($key.Key -ne [ConsoleKey]::R) { break }\r\n")
	script.WriteString("  }\r\n")
	script.WriteString("} finally {\r\n")
	script.WriteString("  if ($revokeAskPassURL) { try { Invoke-WebRequest -UseBasicParsing -Method Post -Uri $revokeAskPassURL | Out-Null } catch {} }\r\n")
	script.WriteString("  Remove-Item Env:\\SSH_ASKPASS -ErrorAction SilentlyContinue\r\n")
	script.WriteString("  Remove-Item Env:\\SSH_ASKPASS_REQUIRE -ErrorAction SilentlyContinue\r\n")
	script.WriteString("  Remove-Item Env:\\DISPLAY -ErrorAction SilentlyContinue\r\n")
	script.WriteString("  Remove-Item Env:\\FLOE_ASKPASS_URL -ErrorAction SilentlyContinue\r\n")
	script.WriteString("  Remove-Item -LiteralPath $PSCommandPath -Force -ErrorAction SilentlyContinue\r\n")
	script.WriteString("}\r\n")
	if _, err := io.WriteString(file, script.String()); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func powerShellSingleQuoted(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func startWindowsTerminal(args []string) error {
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("wt.exe"); err != nil {
			return errors.New("Windows Terminal is unavailable")
		}
	}
	return exec.Command("wt.exe", args...).Start()
}

func preferredPowerShell() string {
	if path, err := exec.LookPath("pwsh.exe"); err == nil {
		return path
	}
	if runtime.GOOS == "windows" {
		seen := make(map[string]bool)
		for _, variable := range []string{"ProgramW6432", "ProgramFiles"} {
			root := strings.TrimSpace(os.Getenv(variable))
			if root == "" || seen[strings.ToLower(root)] {
				continue
			}
			seen[strings.ToLower(root)] = true
			candidate := filepath.Join(root, "PowerShell", "7", "pwsh.exe")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return "powershell.exe"
}

func LaunchPowerShell(cwd string) error {
	args := []string{"-w", "0", "new-tab"}
	if cwd != "" {
		args = append(args, "-d", cwd)
	}
	args = append(args, "--title", "PowerShell", preferredPowerShell(), "-NoLogo")
	return startWindowsTerminal(args)
}
