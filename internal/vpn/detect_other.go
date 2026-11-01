//go:build !linux && !windows && !android

package vpn

func Detect() Status { return Status{} }
