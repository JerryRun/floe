package app

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"floe/internal/core"
)

const cliReadLimit = int64(256 << 20)

var Version = "0.1.0"

// RunCLI runs floectl against Floe's saved local configuration. Network
// commands establish their own short-lived connection and do not require the
// browser UI or Floe.exe to be running.
func RunCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("floectl", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", DefaultDataDir(), "Floe data directory")
	flags.Usage = func() { printCLIUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	remaining := flags.Args()
	if len(remaining) == 0 || remaining[0] == "help" {
		printCLIUsage(stdout)
		return 0
	}
	if remaining[0] == "version" {
		if len(remaining) != 1 {
			fmt.Fprintln(stderr, "version 不接受额外参数")
			return 2
		}
		fmt.Fprintf(stdout, "Floe %s %s/%s\n", Version, runtime.GOOS, runtime.GOARCH)
		return 0
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		fmt.Fprintln(stderr, "创建 Floe 数据目录失败:", err)
		return 1
	}
	store, err := newSessionStore(*dataDir)
	if err != nil {
		fmt.Fprintln(stderr, "读取 Floe 会话失败:", err)
		return 1
	}

	command := remaining[0]
	commandArgs := remaining[1:]
	switch command {
	case "sessions":
		err = cliSessions(store, commandArgs, stdout)
	case "session":
		err = cliSession(store, commandArgs, stdin, stdout, stderr)
	case "ls", "cat", "mkdir", "rm":
		err = cliRemoteCommand(store, command, commandArgs, stdin, stdout)
	case "get", "put":
		err = cliTransfer(store, *dataDir, command, commandArgs, stdin, stdout, stderr)
	case "logs":
		err = cliLogs(*dataDir, commandArgs, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "未知命令 %q\n\n", command)
		printCLIUsage(stderr)
		return 2
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "错误:", err)
		return 1
	}
	return 0
}

func printCLIUsage(w io.Writer) {
	fmt.Fprintln(w, `Floe CLI

用法:
  floe ctl version
  floe ctl help
  floe ctl [--data-dir DIR] logs [--limit N]
  floe ctl [--data-dir DIR] logs clear
  floe ctl [--data-dir DIR] sessions
  floe ctl [--data-dir DIR] session show <会话ID>
  floe ctl [--data-dir DIR] session add [选项]
  floe ctl [--data-dir DIR] session update <会话ID> [选项]
  floe ctl [--data-dir DIR] session delete <会话ID>
  floe ctl [--data-dir DIR] ls <会话ID> [远程目录]
  floe ctl [--data-dir DIR] cat <会话ID> <远程文件>
  floe ctl [--data-dir DIR] mkdir <会话ID> <远程目录>
  floe ctl [--data-dir DIR] rm <会话ID> <远程路径>
  floe ctl [--data-dir DIR] get [-j N] <源会话ID> <远程源文件> <本地目标>
  floe ctl [--data-dir DIR] put [-j N] <本地源文件> <目标会话ID> <远程目标>
  floe ctl [--data-dir DIR] get|put [-j N] <源会话ID> <远程源文件> <目标会话ID> <远程目标>

会话选项:
  --name NAME --protocol sftp|ftp --host HOST --port PORT --user USER
  --password VALUE | --password-stdin
  --auth password|key --private-key PATH --group GROUP
  --keepalive --alive-interval 60 --alive-count 3

get/put 复用 Floe 的分块并发传输引擎，每块写入后均回读并校验 SHA-256。`)
}

func cliSessions(store *sessionStore, args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return errors.New("sessions 不接受额外参数")
	}
	items := store.List()
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\t协议\t分组\t名称\t地址")
	for _, item := range items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", item.ID, strings.ToUpper(item.Kind), item.Group, item.Name, item.Location)
	}
	return w.Flush()
}

func cliSession(store *sessionStore, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("用法: floe ctl session add|show|update|delete")
	}
	switch args[0] {
	case "show":
		if len(args) != 2 {
			return errors.New("用法: floe ctl session show <会话ID>")
		}
		return cliSessionShow(store, args[1], stdout)
	case "add":
		return cliSessionSave(store, "", args[1:], stdin, stdout, stderr)
	case "update":
		if len(args) < 2 {
			return errors.New("用法: floe ctl session update <会话ID> [选项]")
		}
		return cliSessionSave(store, args[1], args[2:], stdin, stdout, stderr)
	case "delete":
		if len(args) != 2 {
			return errors.New("用法: floe ctl session delete <会话ID>")
		}
		details, err := store.Details(args[1])
		if err != nil {
			return err
		}
		if err := store.Delete(args[1]); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "已删除会话: %s (%s)\n", details.Name, details.ID)
		return nil
	default:
		return fmt.Errorf("未知 session 子命令 %q", args[0])
	}
}

func cliSessionShow(store *sessionStore, id string, stdout io.Writer) error {
	details, err := store.Details(id)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "ID\t%s\n名称\t%s\n分组\t%s\n协议\t%s\n主机\t%s\n端口\t%d\n用户\t%s\n登录方式\t%s\n已保存密码/口令\t%s\n私钥\t%s\n主机指纹\t%s\n",
		details.ID, details.Name, details.Group, strings.ToUpper(details.Protocol), details.Host, details.Port,
		details.User, authMethodLabel(details.AuthMethod), yesNo(details.HasPassword), emptyDash(details.PrivateKey), emptyDash(details.Fingerprint))
	if details.Protocol == "sftp" {
		fmt.Fprintf(w, "SSH 心跳\t%s\n心跳间隔\t%d 秒\n允许失败次数\t%d\n",
			yesNo(details.SSHKeepAlive), details.ServerAliveInterval, details.ServerAliveCountMax)
	}
	return w.Flush()
}

type cliSessionOptions struct {
	name, protocol, host, user, password, auth, privateKey, group string
	port, aliveInterval, aliveCount                               int
	passwordStdin, clearPassword, keepAlive                       bool
}

func cliSessionSave(store *sessionStore, id string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	request := core.ConnectRequest{
		ID: id, Protocol: "sftp", Group: "我的会话", AuthMethod: "password",
		ServerAliveInterval: core.DefaultServerAliveInterval, ServerAliveCountMax: core.DefaultServerAliveCountMax,
	}
	if id != "" {
		var err error
		request, err = store.Request(id)
		if err != nil {
			return err
		}
	}
	options := cliSessionOptions{
		name: request.Name, protocol: request.Protocol, host: request.Host, port: request.Port,
		user: request.User, auth: request.AuthMethod, privateKey: request.PrivateKey, group: request.Group,
		keepAlive: request.SSHKeepAlive, aliveInterval: request.ServerAliveInterval, aliveCount: request.ServerAliveCountMax,
	}
	flags := flag.NewFlagSet("session", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.name, "name", options.name, "会话名称")
	flags.StringVar(&options.protocol, "protocol", options.protocol, "sftp 或 ftp")
	flags.StringVar(&options.host, "host", options.host, "服务器主机")
	flags.IntVar(&options.port, "port", options.port, "服务器端口")
	flags.StringVar(&options.user, "user", options.user, "用户名")
	flags.StringVar(&options.password, "password", "", "密码或私钥口令（可能出现在命令历史中）")
	flags.BoolVar(&options.passwordStdin, "password-stdin", false, "从标准输入读取密码或私钥口令")
	flags.BoolVar(&options.clearPassword, "clear-password", false, "清除已保存密码或私钥口令")
	flags.StringVar(&options.auth, "auth", options.auth, "password 或 key")
	flags.StringVar(&options.privateKey, "private-key", options.privateKey, "SSH 私钥文件路径")
	flags.StringVar(&options.group, "group", options.group, "会话分组")
	flags.BoolVar(&options.keepAlive, "keepalive", options.keepAlive, "启用 SSH 心跳")
	flags.IntVar(&options.aliveInterval, "alive-interval", options.aliveInterval, "SSH 心跳间隔秒数")
	flags.IntVar(&options.aliveCount, "alive-count", options.aliveCount, "SSH 心跳允许失败次数")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("无法识别的会话参数: %s", strings.Join(flags.Args(), " "))
	}
	if options.passwordStdin && options.password != "" {
		return errors.New("--password 与 --password-stdin 不能同时使用")
	}
	if options.clearPassword && (options.passwordStdin || options.password != "") {
		return errors.New("--clear-password 不能与密码输入选项同时使用")
	}
	if options.passwordStdin {
		value, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
		if err != nil {
			return err
		}
		options.password = strings.TrimRight(string(value), "\r\n")
	}
	request.Name, request.Protocol, request.Host, request.Port = options.name, options.protocol, options.host, options.port
	request.User, request.AuthMethod, request.PrivateKey, request.Group = options.user, options.auth, options.privateKey, options.group
	request.SSHKeepAlive, request.ServerAliveInterval, request.ServerAliveCountMax = options.keepAlive, options.aliveInterval, options.aliveCount
	if options.clearPassword {
		request.Password, request.ClearPassword = "", true
	} else if options.password != "" {
		request.Password = options.password
	} else {
		request.Password = "" // Save retains the existing encrypted secret when editing.
	}
	provider, err := store.Save(request)
	if err != nil {
		return err
	}
	action := "已添加会话"
	if id != "" {
		action = "已更新会话"
	}
	fmt.Fprintf(stdout, "%s: %s (%s)\n", action, provider.Name, provider.ID)
	return nil
}

func authMethodLabel(value string) string {
	if value == "key" {
		return "SSH 密钥"
	}
	return "密码"
}

func yesNo(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func cliRemoteCommand(store *sessionStore, command string, args []string, stdin io.Reader, stdout io.Writer) error {
	minimum := 2
	if command == "ls" {
		minimum = 1
	}
	if len(args) < minimum || len(args) > 2 {
		return fmt.Errorf("用法: floectl %s <会话ID> <%s>", command, map[string]string{"ls": "远程目录(可选)", "cat": "远程文件", "mkdir": "远程目录", "rm": "远程路径"}[command])
	}
	manager, provider, err := connectCLI(store, args[0], stdin, stdout)
	if err != nil {
		return err
	}
	defer manager.Close()
	remotePath := "/"
	if len(args) == 2 {
		remotePath = args[1]
	}
	switch command {
	case "ls":
		entries, err := provider.List(remotePath)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "类型\t大小\t修改时间\t名称")
		for _, entry := range entries {
			kind := "文件"
			name := entry.Name
			if entry.IsDir {
				kind, name = "目录", entry.Name+"/"
			}
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", kind, entry.Size, entry.Modified.Local().Format("2006-01-02 15:04:05"), name)
		}
		return w.Flush()
	case "cat":
		data, err := provider.ReadFile(remotePath, cliReadLimit)
		if err != nil {
			return err
		}
		_, err = stdout.Write(data)
		return err
	case "mkdir":
		if err := provider.Mkdir(remotePath); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "目录已创建:", remotePath)
	case "rm":
		if err := provider.Remove(remotePath); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "已删除:", remotePath)
	}
	return nil
}

func connectCLI(store *sessionStore, id string, stdin io.Reader, stdout io.Writer) (*core.Manager, core.FileSystem, error) {
	manager := core.NewManager()
	provider, err := connectCLIProvider(store, manager, id, bufio.NewReader(stdin), stdout)
	if err != nil {
		_ = manager.Close()
		return nil, nil, err
	}
	return manager, provider, nil
}

func connectCLIProvider(store *sessionStore, manager *core.Manager, id string, input *bufio.Reader, stdout io.Writer) (core.FileSystem, error) {
	if provider, ok := manager.Get(id); ok {
		return provider, nil
	}
	request, err := store.Request(id)
	if err != nil {
		return nil, err
	}
	_, err = manager.Connect(request)
	candidateFingerprint := ""
	var unknown *core.UnknownHostKeyError
	if errors.As(err, &unknown) {
		fmt.Fprintf(stdout, "首次连接主机指纹: %s\n确认信任并保存？[y/N] ", unknown.Fingerprint)
		candidateFingerprint = unknown.Fingerprint
	}
	var changed *core.HostKeyChangedError
	if errors.As(err, &changed) {
		fmt.Fprintf(stdout, "警告：服务器主机密钥已变化。\n原指纹: %s\n新指纹: %s\n请先通过可信渠道核对。确认替换并连接？[y/N] ", changed.Expected, changed.Received)
		candidateFingerprint = changed.Received
	}
	if candidateFingerprint != "" {
		answer, readErr := input.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, readErr
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			return nil, errors.New("未信任主机指纹")
		}
		request.Fingerprint = candidateFingerprint
		_, err = manager.Connect(request)
		if err == nil {
			if trustErr := store.TrustFingerprint(id, candidateFingerprint); trustErr != nil {
				return nil, trustErr
			}
		}
	}
	if err != nil {
		return nil, err
	}
	provider, ok := manager.Get(id)
	if !ok {
		return nil, errors.New("连接成功但 Provider 不存在")
	}
	return provider, nil
}

func cliTransfer(store *sessionStore, dataDir, command string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	concurrency := 4
	flags.IntVar(&concurrency, "j", 4, "并发分块数 (1-8)")
	flags.IntVar(&concurrency, "concurrency", 4, "并发分块数 (1-8)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	values := flags.Args()
	if concurrency < 1 || concurrency > 8 {
		return errors.New("并发分块数必须在 1–8 之间")
	}
	source, target, err := parseCLITransferEndpoints(command, values)
	if err != nil {
		return err
	}
	manager := core.NewManager()
	defer manager.Close()
	input := bufio.NewReader(stdin)
	remoteProviders := make(map[string]core.FileSystem)
	resolveRemote := func(endpoint cliTransferEndpoint) (core.FileSystem, error) {
		if provider := remoteProviders[endpoint.session]; provider != nil {
			return provider, nil
		}
		provider, err := connectCLIProvider(store, manager, endpoint.session, input, stdout)
		if err == nil {
			remoteProviders[endpoint.session] = provider
		}
		return provider, err
	}

	var sourceProvider core.FileSystem
	sourcePath := source.path
	if source.local {
		localPath, err := filepath.Abs(source.path)
		if err != nil {
			return err
		}
		info, err := os.Stat(localPath)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return errors.New("本地源路径必须是文件")
		}
		local, err := core.NewLocalFSWithKind("cli-source-local", "CLI source", filepath.Dir(localPath), "local", "CLI")
		if err != nil {
			return err
		}
		manager.Add(local)
		sourceProvider, sourcePath = local, "/"+filepath.ToSlash(filepath.Base(localPath))
	} else {
		sourceProvider, err = resolveRemote(source)
		if err != nil {
			return err
		}
	}
	sourceName := path.Base(sourcePath)

	var targetProvider core.FileSystem
	targetPath := target.path
	if target.local {
		localPath, err := filepath.Abs(target.path)
		if err != nil {
			return err
		}
		if info, statErr := os.Stat(localPath); statErr == nil && info.IsDir() {
			localPath = filepath.Join(localPath, sourceName)
		}
		local, err := core.NewLocalFSWithKind("cli-target-local", "CLI target", filepath.Dir(localPath), "local", "CLI")
		if err != nil {
			return err
		}
		manager.Add(local)
		targetProvider, targetPath = local, "/"+filepath.ToSlash(filepath.Base(localPath))
	} else {
		targetProvider, err = resolveRemote(target)
		if err != nil {
			return err
		}
		if info, statErr := targetProvider.Stat(targetPath); statErr == nil && info.IsDir {
			targetPath = path.Join(targetPath, sourceName)
		}
	}
	request := core.TransferRequest{
		SourceProvider: sourceProvider.ID(), SourcePath: sourcePath,
		TargetProvider: targetProvider.ID(), TargetPath: targetPath, Concurrency: concurrency,
	}
	taskDir, err := os.MkdirTemp(dataDir, "cli-transfer-")
	if err != nil {
		taskDir, err = os.MkdirTemp("", "floe-cli-transfer-")
	}
	if err != nil {
		return err
	}
	defer os.RemoveAll(taskDir)
	engine := core.NewTransferEngine(manager, filepath.Join(taskDir, "tasks.json"))
	task, err := engine.Create(request)
	if err != nil {
		return err
	}
	if err := waitCLITransfer(engine, task.ID, stderr); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "完成: %s → %s（SHA-256 分块校验通过）\n", request.SourcePath, request.TargetPath)
	return nil
}

type cliTransferEndpoint struct {
	local   bool
	session string
	path    string
}

func parseCLITransferEndpoints(command string, values []string) (cliTransferEndpoint, cliTransferEndpoint, error) {
	usage := fmt.Sprintf("用法: floe ctl %s [-j N] <本地路径> <会话ID> <远程路径>，或 <源会话ID> <远程源路径> <目标会话ID> <远程目标路径>", command)
	if len(values) == 4 {
		return cliTransferEndpoint{session: values[0], path: values[1]}, cliTransferEndpoint{session: values[2], path: values[3]}, nil
	}
	if len(values) != 3 {
		return cliTransferEndpoint{}, cliTransferEndpoint{}, errors.New(usage)
	}
	if command == "get" {
		return cliTransferEndpoint{session: values[0], path: values[1]}, cliTransferEndpoint{local: true, path: values[2]}, nil
	}
	if command == "put" {
		return cliTransferEndpoint{local: true, path: values[0]}, cliTransferEndpoint{session: values[1], path: values[2]}, nil
	}
	return cliTransferEndpoint{}, cliTransferEndpoint{}, errors.New(usage)
}

func waitCLITransfer(engine *core.TransferEngine, id string, stderr io.Writer) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastPercent := -1
	for {
		var current *core.TransferTask
		for _, task := range engine.List() {
			if task.ID == id {
				copy := task
				current = &copy
				break
			}
		}
		if current == nil {
			return errors.New("传输任务意外消失")
		}
		percent := 100
		if current.Size > 0 {
			percent = int(current.BytesVerified * 100 / current.Size)
		}
		if percent != lastPercent {
			fmt.Fprintf(stderr, "\r%s %3d%%  %d/%d bytes", cliTransferStatus(current.Status), percent, current.BytesVerified, current.Size)
			lastPercent = percent
		}
		switch current.Status {
		case "completed":
			fmt.Fprintln(stderr)
			return nil
		case "failed":
			fmt.Fprintln(stderr)
			return errors.New(current.Error)
		case "paused":
			fmt.Fprintln(stderr)
			return errors.New("传输已暂停")
		}
		<-ticker.C
	}
}

func cliTransferStatus(status string) string {
	if status == "verifying" {
		return "校验"
	}
	return "传输"
}

func cliLogs(dataDir string, args []string, stdout, stderr io.Writer) error {
	activity := newActivityLog(dataDir)
	if len(args) == 1 && args[0] == "clear" {
		fmt.Fprintf(stdout, "已清空 %d 条日志\n", activity.Clear())
		return nil
	}
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	limit := flags.Int("limit", 100, "maximum number of entries")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *limit < 1 || *limit > activityLogLimit {
		return fmt.Errorf("用法: floectl logs [--limit 1-%d]", activityLogLimit)
	}
	for _, entry := range activity.List(*limit) {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", entry.Time.Local().Format(time.RFC3339), strings.ToUpper(entry.Level), entry.Category, entry.Message)
		if entry.Detail != "" {
			fmt.Fprintln(stdout, "  "+entry.Detail)
		}
	}
	return nil
}
