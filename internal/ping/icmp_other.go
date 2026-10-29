//go:build !windows && !linux

package ping

func newICMP() Pinger { return nil }
