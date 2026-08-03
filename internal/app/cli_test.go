package app

import (
	"bytes"
	"strings"
	"testing"

	"floe/internal/core"
)

func TestCLISessionsAndShowDoNotExposePassword(t *testing.T) {
	directory := t.TempDir()
	store, err := newSessionStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := store.Save(core.ConnectRequest{
		Name: "Build server", Protocol: "sftp", Host: "192.0.2.10", Port: 22,
		User: "builder", Password: "never-print-this", Group: "测试",
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := RunCLI([]string{"--data-dir", directory, "sessions"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("sessions code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Build server") || strings.Contains(stdout.String(), "never-print-this") {
		t.Fatalf("sessions output = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := RunCLI([]string{"--data-dir", directory, "session", "show", provider.ID}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("session show code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "已保存密码") || !strings.Contains(stdout.String(), "是") || strings.Contains(stdout.String(), "never-print-this") {
		t.Fatalf("session show output = %q", stdout.String())
	}
}

func TestCLILogsClear(t *testing.T) {
	directory := t.TempDir()
	newActivityLog(directory).Add("error", "ftp", "连接失败", "timeout")
	var stdout, stderr bytes.Buffer
	if code := RunCLI([]string{"--data-dir", directory, "logs", "clear"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("logs clear code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1") || len(newActivityLog(directory).List(10)) != 0 {
		t.Fatalf("logs clear output=%q", stdout.String())
	}
}

func TestCLIVersionAndHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunCLI([]string{"version"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("version code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Floe "+Version) {
		t.Fatalf("version output=%q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := RunCLI([]string{"help"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("help code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "session add") || !strings.Contains(stdout.String(), "get|put") {
		t.Fatalf("help output=%q", stdout.String())
	}
}

func TestCLISessionAddUpdateDelete(t *testing.T) {
	directory := t.TempDir()
	var stdout, stderr bytes.Buffer
	add := []string{
		"--data-dir", directory, "session", "add", "--name", "CLI server", "--host", "192.0.2.20",
		"--user", "builder", "--password-stdin", "--keepalive", "--alive-interval", "45", "--alive-count", "5",
	}
	if code := RunCLI(add, strings.NewReader("secret\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("session add code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	store, err := newSessionStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	items := store.List()
	if len(items) != 1 {
		t.Fatalf("saved sessions=%#v", items)
	}
	id := items[0].ID
	request, err := store.Request(id)
	if err != nil {
		t.Fatal(err)
	}
	if request.Password != "secret" || !request.SSHKeepAlive || request.ServerAliveInterval != 45 || request.ServerAliveCountMax != 5 {
		t.Fatalf("added session=%#v", request)
	}

	stdout.Reset()
	stderr.Reset()
	update := []string{"--data-dir", directory, "session", "update", id, "--name", "Updated server", "--clear-password"}
	if code := RunCLI(update, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("session update code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	store, err = newSessionStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	request, err = store.Request(id)
	if err != nil {
		t.Fatal(err)
	}
	if request.Name != "Updated server" || request.Password != "" {
		t.Fatalf("updated session=%#v", request)
	}

	stdout.Reset()
	stderr.Reset()
	if code := RunCLI([]string{"--data-dir", directory, "session", "delete", id}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("session delete code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	store, err = newSessionStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.List()) != 0 {
		t.Fatal("deleted CLI session reappeared")
	}
}

func TestParseCLITransferEndpoints(t *testing.T) {
	tests := []struct {
		command string
		values  []string
		source  cliTransferEndpoint
		target  cliTransferEndpoint
	}{
		{"get", []string{"source", "/remote.bin", `C:\Downloads\remote.bin`}, cliTransferEndpoint{session: "source", path: "/remote.bin"}, cliTransferEndpoint{local: true, path: `C:\Downloads\remote.bin`}},
		{"put", []string{`C:\build\file.bin`, "target", "/release/file.bin"}, cliTransferEndpoint{local: true, path: `C:\build\file.bin`}, cliTransferEndpoint{session: "target", path: "/release/file.bin"}},
		{"get", []string{"source", "/remote.bin", "target", "/copy.bin"}, cliTransferEndpoint{session: "source", path: "/remote.bin"}, cliTransferEndpoint{session: "target", path: "/copy.bin"}},
		{"put", []string{"source", "/remote.bin", "target", "/copy.bin"}, cliTransferEndpoint{session: "source", path: "/remote.bin"}, cliTransferEndpoint{session: "target", path: "/copy.bin"}},
	}
	for _, test := range tests {
		source, target, err := parseCLITransferEndpoints(test.command, test.values)
		if err != nil {
			t.Fatal(err)
		}
		if source != test.source || target != test.target {
			t.Fatalf("%s %#v => source=%#v target=%#v", test.command, test.values, source, target)
		}
	}
	if _, _, err := parseCLITransferEndpoints("get", []string{"too", "short"}); err == nil {
		t.Fatal("invalid transfer arguments were accepted")
	}
}
