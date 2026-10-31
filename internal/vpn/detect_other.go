//go:build !linux && !windows

package vpn

func Detect() Status { return Status{} }
