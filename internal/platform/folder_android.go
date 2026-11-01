//go:build android

package platform

import "errors"

func OpenFolder(string) error {
	return errors.New("проводник на Android не поддерживается")
}
