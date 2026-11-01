//go:build android

package platform

import "errors"

// Android autostart is intentionally not implemented: VPN consent cannot be
// granted silently and the product requirement is manual start.
func SetAutostart(bool) error {
	return errors.New("автозапуск на Android не поддерживается")
}

func AutostartSupported() bool { return false }
