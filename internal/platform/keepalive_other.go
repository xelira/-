//go:build !android

package platform

func KeepAlive(bool) error { return nil }
