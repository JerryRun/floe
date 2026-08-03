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
			args, err := buildTerminalArgs(test.request, "root@example", `C:\Floe\ssh.ps1`, `C:\Program Files\PowerShell\7\pwsh.exe`)
			if err != nil {
				t.Fatal(err)
			}
			if len(args) < len(test.wantStart) || !reflect.DeepEqual(args[:len(test.wantStart)], test.wantStart) {
				t.Fatalf("arguments start with %#v, want %#v", args, test.wantStart)
			}
			if got := args[len(args)-2:]; !reflect.DeepEqual(got, []string{"-File", `C:\Floe\ssh.ps1`}) {
				t.Fatalf("PowerShell arguments end with %#v", got)
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

func TestSSHArgumentsAreEmbeddedInScript(t *testing.T) {
	server := &Server{dataDir: t.TempDir()}
	scriptPath, err := server.writeSSHScript("", []string{"-o", "StrictHostKeyChecking=accept-new", "-p", "2222", "user's@host"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	want := "$sshArgs = @('-o', 'StrictHostKeyChecking=accept-new', '-p', '2222', 'user''s@host')"
	if !strings.Contains(script, want) || !strings.Contains(script, "& ssh.exe @sshArgs") {
		t.Fatalf("generated script = %q", script)
	}
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
