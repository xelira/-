//go:build windows

package platform

import "os/exec"

func OpenFile(path string) error { return exec.Command("notepad.exe", path).Start() }
