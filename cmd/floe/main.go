package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"

	"floe/internal/app"
)

const defaultListenAddress = "127.0.0.1:7577"

func main() {
	if handled, exitCode := app.RunAskPass(os.Stdout, os.Stderr); handled {
		os.Exit(exitCode)
	}
	if len(os.Args) > 1 && strings.EqualFold(os.Args[1], "ctl") {
		prepareCLIConsole()
		os.Exit(app.RunCLI(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	listen := flag.String("listen", defaultListenAddress, "loopback address for the Floe web UI")
	dataDir := flag.String("data-dir", app.DefaultDataDir(), "Floe data directory")
	noOpen := flag.Bool("no-open", false, "do not open the browser automatically")
	flag.Parse()
	initLogging(*dataDir)

	release, first, err := acquireSingleInstance()
	if err != nil {
		fail("无法建立 Floe 单实例锁", err)
		return
	}
	if !first {
		platformAlreadyRunning()
		return
	}
	defer release()

	server, err := app.New(*dataDir)
	if err != nil {
		fail("Floe Core 初始化失败", err)
		return
	}
	url, done, err := server.Start(*listen)
	if err != nil && *listen == defaultListenAddress {
		log.Printf("Floe preferred port 7577 is unavailable, falling back to a random loopback port: %v", err)
		url, done, err = server.Start("127.0.0.1:0")
	}
	if err != nil {
		fail("Floe 本地服务启动失败", err)
		return
	}
	log.Printf("Floe is ready at %s", url)
	runPlatform(server, url, done, *noOpen)
}

func initLogging(dataDir string) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(dataDir, "floe.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		log.SetOutput(file)
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	}
}

func fail(message string, err error) {
	log.Printf("%s: %v", message, err)
	platformFatal(message + "\n\n" + err.Error())
}
