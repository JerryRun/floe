//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

const attachParentProcess = ^uint32(0)

func prepareCLIConsole() {
	stdinValid := standardHandleValid(windows.STD_INPUT_HANDLE)
	stdoutValid := standardHandleValid(windows.STD_OUTPUT_HANDLE)
	stderrValid := standardHandleValid(windows.STD_ERROR_HANDLE)
	attachConsole := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	_, _, _ = attachConsole.Call(uintptr(attachParentProcess))
	ensureConsoleFile(stdinValid, windows.STD_INPUT_HANDLE, "CONIN$", os.O_RDONLY, &os.Stdin)
	ensureConsoleFile(stdoutValid, windows.STD_OUTPUT_HANDLE, "CONOUT$", os.O_WRONLY, &os.Stdout)
	ensureConsoleFile(stderrValid, windows.STD_ERROR_HANDLE, "CONOUT$", os.O_WRONLY, &os.Stderr)
}

func standardHandleValid(kind uint32) bool {
	handle, err := windows.GetStdHandle(kind)
	return err == nil && handle != 0 && handle != windows.InvalidHandle
}

func ensureConsoleFile(inherited bool, kind uint32, name string, flag int, target **os.File) {
	if inherited {
		return // Preserve redirected files and inherited pipes.
	}
	file, err := os.OpenFile(name, flag, 0)
	if err != nil {
		return
	}
	*target = file
	_ = windows.SetStdHandle(kind, windows.Handle(file.Fd()))
}
