package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ampersand/internal/diagnostics"
	"ampersand/internal/update"
)

// Service — единственное, что видит webview. Специально не светим наружу
// менеджер, логгеры и ручки процессов: меньше поверхности для глупостей.
type Service struct {
	m *Manager
}

func NewService(m *Manager) *Service { return &Service{m: m} }

func (s *Service) GetState() State                             { return s.m.GetState() }
func (s *Service) GetProfiles() []ProfileView                  { return s.m.GetProfiles() }
func (s *Service) StartProxy() ActionResult                    { return s.m.StartProxy() }
func (s *Service) StopProxy() ActionResult                     { return s.m.StopProxy() }
func (s *Service) StartDPI(profileID string) ActionResult      { return s.m.StartDPI(profileID) }
func (s *Service) StopDPI() ActionResult                       { return s.m.StopDPI() }
func (s *Service) Toggle() ActionResult                        { return s.m.Toggle() }
func (s *Service) ApplyProfile(profileID string) SwitchResult  { return s.m.ApplyProfile(profileID) }
func (s *Service) RollbackProfile() SwitchResult               { return s.m.RollbackProfile() }
func (s *Service) RunDiagnostics(scope string) DiagnosticRun   { return s.m.RunDiagnostics(scope) }
func (s *Service) TestProfile(profileID string) ProfileTestRun { return s.m.TestProfile(profileID) }
func (s *Service) DiagnosticsHistory(limit int) []diagnostics.Record {
	return s.m.DiagnosticsHistory(limit)
}
func (s *Service) ExportDiagnostics() ExportResult             { return s.m.ExportDiagnostics() }
func (s *Service) SaveSettings(settings Settings) ActionResult { return s.m.SaveSettings(settings) }
func (s *Service) RotateSecret() ActionResult                  { return s.m.RotateSecret() }
func (s *Service) ProxyLink() string                           { return s.m.ProxyLink() }
func (s *Service) OpenDataFolder() ActionResult                { return s.m.OpenDataFolder() }
func (s *Service) DismissVPNNotice() ActionResult              { return s.m.DismissVPNNotice() }
func (s *Service) AutoConfigure() OnboardingResult             { return s.m.AutoConfigure() }
func (s *Service) SkipOnboarding() ActionResult                { return s.m.SkipOnboarding() }

func (s *Service) TelegramWebStatus() TelegramWebState { return s.m.TelegramWebStatus() }
func (s *Service) CheckTelegramWeb() TelegramWebState  { return s.m.CheckTelegramWeb() }
func (s *Service) ApplyTelegramWebFix() ActionResult   { return s.m.ApplyTelegramWebFix() }
func (s *Service) RevertTelegramWebFix() ActionResult  { return s.m.RevertTelegramWebFix() }
func (s *Service) OpenTelegramWeb() ActionResult       { return s.m.OpenTelegramWeb() }

type UpdateResult struct {
	OK            bool   `json:"ok"`
	Message       string `json:"message"`
	Current       string `json:"current,omitempty"`
	Latest        string `json:"latest,omitempty"`
	Available     bool   `json:"available"`
	CanAutoUpdate bool   `json:"canAutoUpdate"`
	URL           string `json:"url,omitempty"`
}

func (s *Service) InstallUpdate() UpdateResult { return s.m.InstallUpdate() }

func (s *Service) CheckUpdates() UpdateResult {
	url := strings.TrimSpace(os.Getenv("AND_UPDATE_URL"))
	if url == "" {
		url = update.ReleaseAPI
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	info, err := update.Check(ctx, s.m.version, url)
	if err != nil {
		return UpdateResult{OK: false, Message: "Проверка обновлений: " + err.Error()}
	}
	if info.Available {
		return UpdateResult{
			OK: true, Current: info.Current, Latest: info.Latest, Available: true,
			CanAutoUpdate: canAutoUpdate(), URL: info.URL,
			Message: "Доступна версия " + info.Latest + ".",
		}
	}
	return UpdateResult{OK: true, Current: info.Current, Message: "Установлена актуальная версия."}
}

func canAutoUpdate() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	path := strings.ToLower(filepath.Clean(exe))
	if strings.Contains(path, `\program files\`) || strings.Contains(path, `\program files (x86)\`) {
		return true
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(exe), "unins000.exe"))
	return err == nil
}
