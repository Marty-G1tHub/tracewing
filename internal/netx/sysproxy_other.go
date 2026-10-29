//go:build !windows

package netx

func readPlatformProxy() SysProxy { return SysProxy{Source: "none"} }
