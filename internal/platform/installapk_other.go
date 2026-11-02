//go:build !android

package platform

import "errors"

func InstallAPK(string) error {
	return errors.New("установка APK доступна только на Android")
}
