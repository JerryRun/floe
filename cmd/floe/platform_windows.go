//go:build windows

package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"floe/internal/app"
	"golang.org/x/sys/windows"
)

//go:embed assets/floe.ico
var trayIcon []byte

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmAppTray       = 0x8001

	csDblClks = 0x0008

	imageIcon      = 1
	lrLoadFromFile = 0x0010
	lrDefaultSize  = 0x0040

	nimAdd        = 0x00000000
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004
	nifMessage    = 0x00000001
	nifIcon       = 0x00000002
	nifTip        = 0x00000004
	nifShowTip    = 0x00000080
	notifyVersion = 4

	mfString    = 0x00000000
	mfGray      = 0x00000001
	mfChecked   = 0x00000008
	mfSeparator = 0x00000800

	tpmRightButton  = 0x0002
	tpmReturnCmd    = 0x0100
	tdfUseHIconMain = 0x0002
	tdcbfOKButton   = 0x0001

	menuOpen        = 1001
	menuPowerShell  = 1002
	menuAbout       = 1003
	menuQuit        = 1004
	menuOpenAtStart = 1005
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	shell32                 = windows.NewLazySystemDLL("shell32.dll")
	comctl32                = windows.NewLazySystemDLL("comctl32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassEx     = user32.NewProc("RegisterClassExW")
	procCreateWindowEx      = user32.NewProc("CreateWindowExW")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procGetMessage          = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessage         = user32.NewProc("PostMessageW")
	procLoadImage           = user32.NewProc("LoadImageW")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenu          = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	procTaskDialogIndirect  = comctl32.NewProc("TaskDialogIndirect")
	procGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	activeTray              *nativeTray
)

type point struct {
	X int32
	Y int32
}

type message struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   point
	Private uint32
}

type windowClassEx struct {
	Size        uint32
	Style       uint32
	WindowProc  uintptr
	ClassExtra  int32
	WindowExtra int32
	Instance    uintptr
	Icon        uintptr
	Cursor      uintptr
	Background  uintptr
	MenuName    *uint16
	ClassName   *uint16
	SmallIcon   uintptr
}

type notifyIconData struct {
	Size        uint32
	Window      uintptr
	ID          uint32
	Flags       uint32
	Callback    uint32
	Icon        uintptr
	Tip         [128]uint16
	State       uint32
	StateMask   uint32
	Info        [256]uint16
	Version     uint32
	InfoTitle   [64]uint16
	InfoFlags   uint32
	GUID        windows.GUID
	BalloonIcon uintptr
}

type taskDialogConfig struct {
	Size                 uint32
	Parent               uintptr
	Instance             uintptr
	Flags                uint32
	CommonButtons        uint32
	WindowTitle          *uint16
	MainIcon             uintptr
	MainInstruction      *uint16
	Content              *uint16
	ButtonCount          uint32
	Buttons              uintptr
	DefaultButton        int32
	RadioButtonCount     uint32
	RadioButtons         uintptr
	DefaultRadioButton   int32
	VerificationText     *uint16
	ExpandedInformation  *uint16
	ExpandedControlText  *uint16
	CollapsedControlText *uint16
	FooterIcon           uintptr
	Footer               *uint16
	Callback             uintptr
	CallbackData         uintptr
	Width                uint32
}

type nativeTray struct {
	window      uintptr
	icon        uintptr
	server      *app.Server
	preferences *app.Preferences
	nid         notifyIconData
}

func acquireSingleInstance() (func(), bool, error) {
	name, err := windows.UTF16PtrFromString(`Local\Floe-Core-9B8D42D1-58E5-4C09-ACD2-D973C220E78C`)
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateMutex(nil, true, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return func() {}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return func() { _ = windows.CloseHandle(handle) }, true, nil
}

func platformAlreadyRunning() {
	messageBox("Floe 已在运行。\n\n请从 Windows 托盘区打开 Floe。", windows.MB_OK|windows.MB_ICONINFORMATION)
}

func platformFatal(message string) {
	messageBox(message, windows.MB_OK|windows.MB_ICONERROR)
}

func messageBox(value string, style uint32) {
	text, _ := windows.UTF16PtrFromString(value)
	title, _ := windows.UTF16PtrFromString("Floe")
	_, _ = windows.MessageBox(0, text, title, style)
}

func runPlatform(server *app.Server, initialURL string, done <-chan error, noOpen bool, preferences *app.Preferences) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	tray, err := newNativeTray(server, preferences)
	if err != nil {
		fail("Floe 托盘初始化失败", err)
		return
	}
	defer tray.close()
	activeTray = tray

	if !noOpen && preferences.OpenBrowserOnStartup() {
		go func() {
			if err := app.OpenBrowser(initialURL); err != nil {
				log.Printf("open browser: %v", err)
			}
		}()
	}
	go func() {
		if err := <-done; err != nil {
			app.LogServeError(err)
		}
		_, _, _ = procPostMessage.Call(tray.window, wmClose, 0, 0)
	}()

	var msg message
	for {
		result, _, callErr := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) == -1 {
			log.Printf("Windows message loop: %v", callErr)
			break
		}
		if result == 0 {
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func newNativeTray(server *app.Server, preferences *app.Preferences) (*nativeTray, error) {
	iconPath, err := writeTrayIcon()
	if err != nil {
		return nil, err
	}
	iconName, _ := windows.UTF16PtrFromString(iconPath)
	icon, _, loadErr := procLoadImage.Call(0, uintptr(unsafe.Pointer(iconName)), imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
	if icon == 0 {
		return nil, fmt.Errorf("load tray icon: %w", loadErr)
	}

	instance, _, instanceErr := procGetModuleHandle.Call(0)
	if instance == 0 {
		return nil, fmt.Errorf("get module handle: %w", instanceErr)
	}
	className, _ := windows.UTF16PtrFromString("FloeTrayWindow")
	wc := windowClassEx{
		Size: uint32(unsafe.Sizeof(windowClassEx{})), Style: csDblClks,
		WindowProc: windows.NewCallback(trayWindowProc), Instance: instance,
		Icon: icon, ClassName: className, SmallIcon: icon,
	}
	registered, _, registerErr := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	if registered == 0 {
		_, _, _ = procDestroyIcon.Call(icon)
		return nil, fmt.Errorf("register tray window: %w", registerErr)
	}
	windowName, _ := windows.UTF16PtrFromString("Floe")
	window, _, createErr := procCreateWindowEx.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowName)), 0,
		0, 0, 0, 0, 0, 0, instance, 0,
	)
	if window == 0 {
		_, _, _ = procDestroyIcon.Call(icon)
		return nil, fmt.Errorf("create tray window: %w", createErr)
	}
	tray := &nativeTray{window: window, icon: icon, server: server, preferences: preferences}
	tray.nid = notifyIconData{
		Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: window, ID: 1,
		Flags: nifMessage | nifIcon | nifTip | nifShowTip, Callback: wmAppTray, Icon: icon,
	}
	copyUTF16(tray.nid.Tip[:], "Floe · Remote file workspace")
	activeTray = tray
	added, _, addErr := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&tray.nid)))
	if added == 0 {
		tray.close()
		return nil, fmt.Errorf("add tray icon: %w", addErr)
	}
	tray.nid.Version = notifyVersion
	_, _, _ = procShellNotifyIcon.Call(nimSetVersion, uintptr(unsafe.Pointer(&tray.nid)))
	return tray, nil
}

func writeTrayIcon() (string, error) {
	dir := filepath.Join(os.TempDir(), "Floe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	iconPath := filepath.Join(dir, "floe.ico")
	if err := os.WriteFile(iconPath, trayIcon, 0o600); err != nil {
		return "", err
	}
	return iconPath, nil
}

func (t *nativeTray) close() {
	if t == nil {
		return
	}
	if t.nid.Window != 0 {
		_, _, _ = procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&t.nid)))
		t.nid.Window = 0
	}
	if t.icon != 0 {
		_, _, _ = procDestroyIcon.Call(t.icon)
		t.icon = 0
	}
}

func trayWindowProc(window uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmAppTray:
		event := uint32(lParam & 0xffff)
		if event == wmLButtonDblClk && activeTray != nil {
			go activeTray.openUI()
		} else if event == wmRButtonUp && activeTray != nil {
			activeTray.showMenu()
		}
		return 0
	case wmDestroy:
		if activeTray != nil {
			activeTray.close()
		}
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProc.Call(window, uintptr(message), wParam, lParam)
	return result
}

func (t *nativeTray) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	appendMenu(menu, mfString|mfGray, 0, "Floe Core 已连接")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, menuOpen, "打开 Floe")
	appendMenu(menu, mfString, menuPowerShell, "打开 PowerShell")
	startupFlags := uint32(mfString)
	if t.preferences.OpenBrowserOnStartup() {
		startupFlags |= mfChecked
	}
	appendMenu(menu, startupFlags, menuOpenAtStart, "启动时自动打开浏览器")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, menuAbout, "关于 Floe")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, menuQuit, "退出 Floe")

	var cursor point
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	_, _, _ = procSetForegroundWindow.Call(t.window)
	command, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd, uintptr(cursor.X), uintptr(cursor.Y), 0, t.window, 0)
	switch command {
	case menuOpen:
		go t.openUI()
	case menuPowerShell:
		go func() {
			if err := app.LaunchPowerShell(""); err != nil {
				log.Printf("open PowerShell: %v", err)
				platformFatal("无法打开 Windows Terminal。\n\n" + err.Error())
			}
		}()
	case menuOpenAtStart:
		next := !t.preferences.OpenBrowserOnStartup()
		if err := t.preferences.SetOpenBrowserOnStartup(next); err != nil {
			log.Printf("save preferences: %v", err)
			platformFatal("无法保存启动设置。\n\n" + err.Error())
		}
	case menuAbout:
		t.showAbout()
	case menuQuit:
		_, _, _ = procPostMessage.Call(t.window, wmClose, 0, 0)
	}
}

func (t *nativeTray) showAbout() {
	title, _ := windows.UTF16PtrFromString("关于 Floe")
	name, _ := windows.UTF16PtrFromString("Floe")
	details, _ := windows.UTF16PtrFromString("远程文件工作区\n\n作者：Jerry\n版本：" + app.Version)
	config := taskDialogConfig{
		Size: uint32(unsafe.Sizeof(taskDialogConfig{})), Parent: t.window,
		Flags: tdfUseHIconMain, CommonButtons: tdcbfOKButton,
		WindowTitle: title, MainIcon: t.icon, MainInstruction: name, Content: details,
	}
	result, _, callErr := procTaskDialogIndirect.Call(uintptr(unsafe.Pointer(&config)), 0, 0, 0)
	if int32(result) < 0 {
		log.Printf("show Floe about dialog: %v (HRESULT 0x%x)", callErr, result)
		messageBox("Floe\n\n远程文件工作区\n作者：Jerry\n版本："+app.Version, windows.MB_OK|windows.MB_ICONINFORMATION)
	}
}

func (t *nativeTray) openUI() {
	if err := app.OpenBrowser(t.server.LaunchURL()); err != nil {
		log.Printf("open browser: %v", err)
		platformFatal("无法打开默认浏览器。\n\n" + err.Error())
	}
}

func appendMenu(menu uintptr, flags uint32, id uintptr, label string) {
	var text *uint16
	if label != "" {
		text, _ = windows.UTF16PtrFromString(label)
	}
	_, _, _ = procAppendMenu.Call(menu, uintptr(flags), id, uintptr(unsafe.Pointer(text)))
}

func copyUTF16(target []uint16, value string) {
	encoded, _ := windows.UTF16FromString(value)
	copy(target, encoded)
}
