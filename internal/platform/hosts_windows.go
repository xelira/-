//go:build windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func SystemHostsPath() string {
	if p := os.Getenv("AND_HOSTS_PATH"); p != "" {
		return p
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "drivers", "etc", "hosts")
}

func FlushDNSCache() error {
	cmd := exec.Command("ipconfig", "/flushdns")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()
	return nil
}
