//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const taskName = "AmpersandNetworkUtility"

func SetAutostart(enabled bool) error {
	if !enabled {
		_ = exec.Command("schtasks", "/Delete", "/TN", taskName, "/F").Run()
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	token := windows.GetCurrentProcessToken()
	u, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	sid := u.User.Sid.String()
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers><LogonTrigger><UserId>%s</UserId></LogonTrigger></Triggers>
  <Principals><Principal id="Author"><UserId>%s</UserId><RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
  </Settings>
  <Actions Context="Author"><Exec><Command>%s</Command><Arguments>--hidden</Arguments></Exec></Actions>
</Task>`, sid, sid, escapeXML(exe))
	dir := filepath.Join(os.Getenv("ProgramData"), "Ampersand")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "autostart.xml")
	if err := os.WriteFile(path, []byte(xml), 0o600); err != nil {
		return err
	}
	out, err := exec.Command("schtasks", "/Create", "/TN", taskName, "/XML", path, "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func escapeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
