//go:build windows

package netx

import "golang.org/x/sys/windows/registry"

func readPlatformProxy() SysProxy {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return SysProxy{Source: "none"}
	}
	defer k.Close()
	enable, _, _ := k.GetIntegerValue("ProxyEnable")
	server, _, _ := k.GetStringValue("ProxyServer")
	override, _, _ := k.GetStringValue("ProxyOverride")
	pac, _, _ := k.GetStringValue("AutoConfigURL")
	auto, _, _ := k.GetIntegerValue("AutoDetect")

	sp := SysProxy{Source: "none"}
	if enable == 1 && server != "" {
		sp = parseWinProxy(server, override)
	}
	sp.PAC = pac
	sp.AutoDet = auto == 1
	if pac != "" {
		sp.Warnings = append(sp.Warnings, "系统设置了 PAC 脚本，本程序不执行 PAC：“跟随系统”将按直连处理（TUN 仍会接管）。需要走代理请用“自定义代理”。")
	}
	return sp
}
