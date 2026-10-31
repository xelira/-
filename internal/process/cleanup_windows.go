//go:build windows

package process

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// KillStale завершает процессы, запущенные из указанного каталога движка.
// Нужен после обновления/падения приложения: старый winws/python может
// держать порт или висеть отдельным консольным окном.
func KillStale(dir string) []int {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	prefix := strings.ToLower(abs) + string(os.PathSeparator)
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var killed []int
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		pid := entry.ProcessID
		if pid == 0 || int(pid) == os.Getpid() {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, pid)
		if err != nil {
			continue
		}
		buf := make([]uint16, 32768)
		size := uint32(len(buf))
		err = windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
		if err == nil {
			path := strings.ToLower(windows.UTF16ToString(buf[:size]))
			if strings.HasPrefix(path, prefix) && windows.TerminateProcess(h, 1) == nil {
				killed = append(killed, int(pid))
			}
		}
		windows.CloseHandle(h)
	}
	return killed
}
