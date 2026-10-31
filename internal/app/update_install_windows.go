//go:build windows

package app

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

func startUpdateHelper(script string, args []string) error {
	file, _ := syscall.UTF16PtrFromString("powershell.exe")
	params, _ := syscall.UTF16PtrFromString(powerShellArgs(script, args))
	verb, _ := syscall.UTF16PtrFromString("runas")
	shell32 := syscall.NewLazyDLL("shell32.dll")
	proc := shell32.NewProc("ShellExecuteW")
	ret, _, _ := proc.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(params)), 0, 0)
	if ret <= 32 {
		return fmt.Errorf("запуск обновления от администратора отменён (код %d)", ret)
	}
	return nil
}

func powerShellArgs(script string, args []string) string {
	all := append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", script}, args...)
	quoted := make([]string, len(all))
	for i, arg := range all {
		quoted[i] = `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return strings.Join(quoted, " ")
}
