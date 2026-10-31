//go:build windows

package platform

import "os/exec"

func OpenFolder(path string) error {
	return exec.Command("explorer", path).Start()
}
