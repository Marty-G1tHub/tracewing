//go:build !windows

package main

// 非 Windows 平台没有 WebView2：始终使用默认浏览器。
func windowAvailable() bool  { return false }
func openWindow(string) bool { return false }
func closeWindow()           {}
