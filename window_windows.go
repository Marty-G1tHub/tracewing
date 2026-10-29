//go:build windows

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows/registry"
)

// WebView2 的创建与消息循环必须在同一个 OS 线程上；把 main goroutine 固定在启动线程。
func init() { runtime.LockOSThread() }

const (
	winW, winH   = 1120, 780 // 默认窗口（96 DPI 下的像素）
	minW, minH   = 880, 600  // 最小窗口
	runtimeGUID  = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	dwmDarkMode  = 20 // DWMWA_USE_IMMERSIVE_DARK_MODE
	dwmBorder    = 34 // DWMWA_BORDER_COLOR（Win11）
	dwmCaption   = 35 // DWMWA_CAPTION_COLOR（Win11）
	dwmTextColor = 36 // DWMWA_TEXT_COLOR（Win11）
)

var (
	dwm             = syscall.NewLazyDLL("dwmapi.dll")
	pDwmSet         = dwm.NewProc("DwmSetWindowAttribute")
	pDpiSys         = u32.NewProc("GetDpiForSystem")
	pSysMetrics     = u32.NewProc("GetSystemMetrics")
	pFindWindow     = u32.NewProc("FindWindowW")
	pDestroyWindow  = u32.NewProc("DestroyWindow")
	closeWindowHook = func() {}
)

// windowAvailable 报告本机是否装有 WebView2 运行时（Win11 自带，Win10 多数已随 Edge 安装）。
func windowAvailable() bool {
	for _, k := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + runtimeGUID},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + runtimeGUID},
		{registry.CURRENT_USER, `Software\Microsoft\EdgeUpdate\Clients\` + runtimeGUID},
	} {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := key.GetStringValue("pv")
		key.Close()
		if err == nil && v != "" && v != "0.0.0.0" {
			return true
		}
	}
	return false
}

func appsUseLightTheme() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err != nil || v != 0
}

func dwmSet(hwnd uintptr, attr uintptr, v uint32) {
	pDwmSet.Call(hwnd, attr, uintptr(unsafe.Pointer(&v)), 4)
}

// closeWindow 请求关闭窗口（线程安全：PostMessage）；窗口关闭 → 消息循环结束 → 程序退出。
func closeWindow() { closeWindowHook() }

// openWindow 创建独立的 WebView2 窗口并阻塞到它被关闭。失败（运行时损坏等）返回 false，由调用方回退到浏览器。
func openWindow(url string) bool {
	scale := 1.0
	if d, _, _ := pDpiSys.Call(); d != 0 {
		scale = float64(d) / 96
	}
	sw, _, _ := pSysMetrics.Call(0) // SM_CXSCREEN
	sh, _, _ := pSysMetrics.Call(1)
	w, h := int(winW*scale), int(winH*scale)
	if sw > 0 && w > int(sw)*95/100 {
		w = int(sw) * 95 / 100
	}
	if sh > 0 && h > int(sh)*92/100 {
		h = int(sh) * 92 / 100
	}
	data := filepath.Join(os.Getenv("LOCALAPPDATA"), "Tracewing", "WebView2")
	if os.Getenv("LOCALAPPDATA") == "" {
		data = filepath.Join(os.TempDir(), "Tracewing-WebView2")
	}
	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  data,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: "迹翼 Tracewing", Width: uint(w), Height: uint(h), IconId: 1, Center: true,
		},
	})
	if wv == nil {
		// 创建窗口之后才发现运行时不可用：把遗留的空白窗口销毁，别留一个假死的窗口。
		cls, _ := syscall.UTF16PtrFromString("webview")
		if hw, _, _ := pFindWindow.Call(uintptr(unsafe.Pointer(cls)), 0); hw != 0 {
			pDestroyWindow.Call(hw)
		}
		return false
	}
	wv.SetSize(int(minW*scale), int(minH*scale), webview2.HintMin)
	closeWindowHook = wv.Destroy

	// 标题栏与页面同色（Win11），并跟随系统深浅色；Win10 上这些属性被忽略，不影响功能。
	hwnd := uintptr(wv.Window())
	if appsUseLightTheme() {
		dwmSet(hwnd, dwmDarkMode, 0)
		dwmSet(hwnd, dwmCaption, 0xF3EEEC)
		dwmSet(hwnd, dwmBorder, 0xF3EEEC)
		dwmSet(hwnd, dwmTextColor, 0x30231C)
	} else {
		dwmSet(hwnd, dwmDarkMode, 1)
		dwmSet(hwnd, dwmCaption, 0x181412)
		dwmSet(hwnd, dwmBorder, 0x181412)
		dwmSet(hwnd, dwmTextColor, 0xEFE9E6)
	}
	wv.Navigate(url)
	wv.Run()
	return true
}
