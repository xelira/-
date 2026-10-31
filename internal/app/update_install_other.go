//go:build !windows

package app

import "errors"

func startUpdateHelper(string, []string) error {
	return errors.New("автообновление доступно только в установленной Windows-версии")
}
