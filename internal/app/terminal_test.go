package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"floe/internal/core"
)

func TestBuildTerminalArgs(t *testing.T) {
	tests := []struct {
		name      string
		request   terminalRequest
		wantStart []string
	}{
		{name: "new tab", request: terminalRequest{Placement: "new-tab"}, wantStart: []string{"-w", "0", "new-tab"}},
		{name: "current right", request: terminalRequest{Placement: "split-right"}, wantStart: []string{"-w", "0", "split-pane", "-V"}},
		{name: "current down", request: terminalRequest{Placement: "split-down"}, wantStart: []string{"-w", "0", "split-pane", "-H"}},
		{name: "specific tab", request: terminalRequest{Placement: "tab-right", Tab: 3}, wantStart: []string{"-w", "0", "focus-tab", "--target", "2", ";", "split-pane", "-V"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args, err := buildTerminalArgs(test.request, "生产服务器", `C:\Floe\ssh.ps1`, `C:\Program Files\PowerShell\7\pwsh.exe`)
			if err != nil {
				t.Fatal(err)
			}
			if len(args) < len(test.wantStart) || !reflect.DeepEqual(args[:len(test.wantStart)], test.wantStart) {
				t.Fatalf("arguments start with %#v, want %#v", args, test.wantStart)
			}
			if got := args[len(args)-2:]; !reflect.DeepEqual(got, []string{"-File", `C:\Floe\ssh.ps1`}) {
				t.Fatalf("PowerShell arguments end with %#v", got)
			}
			if !containsSequence(args, []string{"--title", "生产服务器"}) {
				t.Fatalf("PowerShell arguments do not include terminal title: %#v", args)
			}
		})
	}
	if _, err := buildTerminalArgs(terminalRequest{Placement: "tab-down"}, "x", "x.ps1", "pwsh.exe"); err == nil {
		t.Fatal("missing target tab was accepted")
	}
}

func TestAskPassTokenIsLoopbackAndSingleUse(t *testing.T) {
	server := &Server{askPassTokens: make(map[string]askPassSecret)}
	server.origin = "http://127.0.0.1:32100"
	token, endpoint := server.issueAskPass("s3cret")
	request := httptest.NewRequest(http.MethodGet, endpoint, nil)
	request.RemoteAddr = "127.0.0.1:54321"
	request.SetPathValue("token", token)
	response := httptest.NewRecorder()
	server.askPass(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "s3cret" {
		t.Fatalf("first response status=%d body=%q", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	server.askPass(response, request)
	if response.Code != http.StatusGone {
		t.Fatalf("reused token status=%d, want %d", response.Code, http.StatusGone)
	}
}

func TestTerminalAskPassTokenIsReusableUntilRevoked(t *testing.T) {
	server := &Server{askPassTokens: make(map[string]askPassSecret)}
	server.origin = "http://127.0.0.1:32100"
	token, _ := server.issueTerminalAskPass("s3cret")
	for index := 0; index < 2; index++ {
		password, ok := server.consumeAskPass(token)
		if !ok || password != "s3cret" {
			t.Fatalf("consume #%d = %q, %v", index+1, password, ok)
		}
	}
	server.revokeAskPass(token)
	if password, ok := server.consumeAskPass(token); ok || password != "" {
		t.Fatalf("revoked token returned %q, %v", password, ok)
	}
}

func TestSSHArgumentsAreEmbeddedInScript(t *testing.T) {
	server := &Server{dataDir: t.TempDir()}
	scriptPath, err := server.writeSSHScript("生产服务器", "http://localhost:32100/askpass/token", []string{"-o", "StrictHostKeyChecking=accept-new", "-p", "2222", "user's@host"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("generated script does not start with UTF-8 BOM: % x", data[:min(len(data), 3)])
	}
	script := string(data)
	want := "$sshArgs = @('-o', 'StrictHostKeyChecking=accept-new', '-p', '2222', 'user''s@host')"
	for _, fragment := range []string{
		want,
		"$Host.UI.RawUI.WindowTitle = '生产服务器'",
		"& ssh.exe @sshArgs",
		"Write-Host '按 R 立即重连；按 Enter、Esc 或其他键结束。' -NoNewline",
		"$key = [Console]::ReadKey($true)",
		"if ($key.Key -ne [ConsoleKey]::R) { break }",
		"$revokeAskPassURL = 'http://localhost:32100/askpass/token/revoke'",
		"Invoke-WebRequest -UseBasicParsing -Method Post -Uri $revokeAskPassURL",
		"Remove-Item Env:\\SSH_ASKPASS",
	} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("generated script does not contain %q: %q", fragment, script)
		}
	}
}

func TestTerminalTitlePrefersSessionName(t *testing.T) {
	title := terminalTitle(core.ConnectRequest{Name: "airych.xyz"}, core.ConnectionTarget{User: "root", Host: "airych.xyz"})
	if title != "airych.xyz" {
		t.Fatalf("terminal title = %q", title)
	}
	fallback := terminalTitle(core.ConnectRequest{}, core.ConnectionTarget{User: "root", Host: "airych.xyz"})
	if fallback != "root@airych.xyz" {
		t.Fatalf("fallback title = %q", fallback)
	}
}

func containsSequence(items, sequence []string) bool {
	for i := 0; i+len(sequence) <= len(items); i++ {
		if reflect.DeepEqual(items[i:i+len(sequence)], sequence) {
			return true
		}
	}
	return false
}

func TestBuildSSHArgsIncludesOptionalKeepAlive(t *testing.T) {
	target := core.ConnectionTarget{Host: "example.test", Port: 2222, User: "root"}
	enabled := core.ConnectRequest{
		PrivateKey: `C:\Users\test\.ssh\id_ed25519`, SSHKeepAlive: true,
		ServerAliveInterval: 60, ServerAliveCountMax: 3,
	}
	args := strings.Join(buildSSHArgs(enabled, target), " ")
	for _, want := range []string{"ServerAliveInterval=60", "ServerAliveCountMax=3", "-p 2222", "root@example.test"} {
		if !strings.Contains(args, want) {
			t.Fatalf("SSH arguments %q do not contain %q", args, want)
		}
	}

	disabled := buildSSHArgs(core.ConnectRequest{}, target)
	if got := strings.Join(disabled, " "); strings.Contains(got, "ServerAlive") {
		t.Fatalf("disabled SSH keepalive arguments = %q", got)
	}
}

func TestRunAskPassRejectsNonLoopbackEndpoint(t *testing.T) {
	t.Setenv("FLOE_ASKPASS_URL", "http://192.0.2.10/secret")
	var stdout, stderr bytes.Buffer
	handled, code := RunAskPass(&stdout, &stderr)
	if !handled || code == 0 || stderr.Len() == 0 {
		t.Fatalf("handled=%v code=%d stdout=%q stderr=%q", handled, code, stdout.String(), stderr.String())
	}
}
