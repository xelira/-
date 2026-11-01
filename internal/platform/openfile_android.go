//go:build android

package platform

import "errors"

func OpenFile(string) error {
	return errors.New("открытие файлов на Android не поддерживается")
}
