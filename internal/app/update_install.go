package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"ampersand/internal/update"
)

func (m *Manager) InstallUpdate() UpdateResult {
	if !canAutoUpdate() {
		return UpdateResult{OK: false, Message: "Обнови вручную: открой страницу релиза на GitHub."}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	info, err := update.Check(ctx, m.version, update.ReleaseAPI)
	if err != nil {
		return UpdateResult{OK: false, Message: "Проверка обновлений: " + err.Error()}
	}
	if !info.Available {
		return UpdateResult{OK: true, Current: info.Current, Message: "Установлена актуальная версия."}
	}
	exe, err := os.Executable()
	if err != nil {
		return UpdateResult{OK: false, Message: "Не удалось определить путь приложения: " + err.Error()}
	}
	temp := os.TempDir()
	installer := filepath.Join(temp, "and-update-setup.exe")
	shaFile := installer + ".sha256"
	script := filepath.Join(temp, "and-update.ps1")
	logs := filepath.Join(m.dataDir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		return UpdateResult{OK: false, Message: err.Error()}
	}
	logPath := filepath.Join(logs, "update.log")
	if err := os.WriteFile(script, []byte(updateScript), 0o700); err != nil {
		return UpdateResult{OK: false, Message: "Не удалось подготовить обновление: " + err.Error()}
	}
	base := "https://github.com/xelira/-/releases/download/" + info.Tag
	args := []string{
		"-AppPid", strconv.Itoa(os.Getpid()),
		"-InstallerUrl", base + "/and-Windows-x64-Setup.exe",
		"-ShaUrl", base + "/and-Windows-x64-Setup.exe.sha256",
		"-InstallerPath", installer,
		"-ShaPath", shaFile,
		"-AppPath", exe,
		"-LogPath", logPath,
	}
	if err := startUpdateHelper(script, args); err != nil {
		return UpdateResult{OK: false, Message: err.Error()}
	}
	go func() {
		time.Sleep(900 * time.Millisecond)
		m.Close()
		os.Exit(0)
	}()
	return UpdateResult{OK: true, Current: info.Current, Latest: info.Latest, Available: true, CanAutoUpdate: true, Message: "Обновление запущено. Приложение перезапустится само."}
}

const updateScript = `param(
  [Parameter(Mandatory=$true)][int]$AppPid,
  [Parameter(Mandatory=$true)][string]$InstallerUrl,
  [Parameter(Mandatory=$true)][string]$ShaUrl,
  [Parameter(Mandatory=$true)][string]$InstallerPath,
  [Parameter(Mandatory=$true)][string]$ShaPath,
  [Parameter(Mandatory=$true)][string]$AppPath,
  [Parameter(Mandatory=$true)][string]$LogPath
)
$ErrorActionPreference = 'Continue'
function Log([string]$Message) {
  try { "$(Get-Date -Format o) $Message" | Out-File -FilePath $LogPath -Append -Encoding utf8 } catch {}
}
Log 'update requested'
try {
  $deadline = (Get-Date).AddSeconds(45)
  while ((Get-Process -Id $AppPid -ErrorAction SilentlyContinue) -and ((Get-Date) -lt $deadline)) {
    Start-Sleep -Milliseconds 300
  }
  Get-Process winws -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
  sc.exe stop windivert | Out-Null
  Start-Sleep -Seconds 1
  Get-Process python -ErrorAction SilentlyContinue | Where-Object { $_.Path -and $_.Path -like '*\and\resources\engines\*' } | Stop-Process -Force -ErrorAction SilentlyContinue
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
  Invoke-WebRequest -UseBasicParsing -Uri $InstallerUrl -OutFile $InstallerPath
  Invoke-WebRequest -UseBasicParsing -Uri $ShaUrl -OutFile $ShaPath
  $expected = ((Get-Content $ShaPath -Raw).Trim() -split '\s+')[0].ToLower()
  $actual = (Get-FileHash -Algorithm SHA256 $InstallerPath).Hash.ToLower()
  if ($expected -ne $actual) { throw 'SHA-256 установщика не совпадает' }
  Log 'installer verified'
  $process = Start-Process -FilePath $InstallerPath -ArgumentList '/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/CLOSEAPPLICATIONS' -Wait -PassThru
  Log ("installer exit " + $process.ExitCode)
} catch {
  Log ("error: " + $_.Exception.Message)
} finally {
  Remove-Item $InstallerPath -Force -ErrorAction SilentlyContinue
  Remove-Item $ShaPath -Force -ErrorAction SilentlyContinue
  Start-Sleep -Milliseconds 800
  if (Test-Path $AppPath) { Start-Process -FilePath $AppPath }
}
`
