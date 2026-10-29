//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	k32  = syscall.NewLazyDLL("kernel32.dll")
	u32  = syscall.NewLazyDLL("user32.dll")
	pAtt = k32.NewProc("AttachConsole")
	pMsg = u32.NewProc("MessageBoxW")
)

// GUI 子系统程序默认没有控制台；从 cmd/PowerShell 启动时附着到父控制台，使 --help/--version 的输出可见。
func attachConsole() {
	if r, _, _ := pAtt.Call(^uintptr(0)); r == 0 { // ATTACH_PARENT_PROCESS
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}

func fatal(msg string) {
	t, _ := syscall.UTF16PtrFromString("测速")
	m, _ := syscall.UTF16PtrFromString(msg)
	pMsg.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10)
	os.Exit(1)
}

// notify 弹一个不阻塞主流程的提示框（在独立 goroutine 里调用）。
func notify(msg string) {
	t, _ := syscall.UTF16PtrFromString("测速")
	m, _ := syscall.UTF16PtrFromString(msg)
	pMsg.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x40) // MB_ICONINFORMATION
}

func openBrowser(url string) error {
	// rundll32 打开默认浏览器，不需要 shell、不弹黑框
	c := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return c.Start()
}
