//go:build linux

package platform

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

const autostartName = "and.desktop"

func SetAutostart(enabled bool) error {
	uid, gid, home := autostartUser()
	if home == "" {
		return fmt.Errorf("не удалось определить домашний каталог пользователя")
	}
	dir := filepath.Join(home, ".config", "autostart")
	path := filepath.Join(dir, autostartName)
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	quoted := strings.ReplaceAll(exe, `\`, `\\`)
	quoted = strings.ReplaceAll(quoted, `"`, `\"`)
	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=&
Comment=Telegram Proxy and DPI
Exec="%s" --hidden
Terminal=false
X-GNOME-Autostart-enabled=true
`, quoted)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chown(dir, uid, gid); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Chown(path, uid, gid)
}

func autostartUser() (int, int, string) {
	uidText := os.Getenv("PKEXEC_UID")
	if uidText == "" {
		uidText = os.Getenv("SUDO_UID")
	}
	if uidText != "" && uidText != "0" {
		u, err := user.LookupId(uidText)
		if err == nil {
			uid, _ := strconv.Atoi(u.Uid)
			gid, _ := strconv.Atoi(u.Gid)
			return uid, gid, u.HomeDir
		}
	}
	if u, err := user.Current(); err == nil {
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		return uid, gid, u.HomeDir
	}
	return 0, 0, ""
}
