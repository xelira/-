//go:build android

package vpn

// Detect intentionally reports no third-party VPN. Android exposes only one
// active VpnService; our own tunnel must not trigger the desktop VPN warning.
func Detect() Status { return Status{} }
