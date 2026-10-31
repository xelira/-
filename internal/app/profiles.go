package app

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"time"

	"ampersand/internal/config"
	"ampersand/internal/platform/core"
)

func (m *Manager) StartDPI(profileID string) ActionResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setBusy(true)
	defer m.setBusy(false)
	if m.dpi.Running() {
		return ActionResult{OK: true, Message: "DPI уже запущен", State: m.state()}
	}
	if profileID == "" {
		cfg := m.store.Snapshot()
		profileID = cfg.DPI.ActiveProfile
		if profileID == "" {
			profileID = cfg.DPI.LastKnownGoodProfile
		}
	}
	if profileID == "" {
		p := m.firstCompatibleProfile()
		if p == "" {
			return ActionResult{OK: false, Message: "Нет доступного DPI-профиля для этой платформы", State: m.state()}
		}
		profileID = p
	}
	checks, err := m.startDPILocked(context.Background(), profileID)
	if err != nil {
		m.setDPIError(err.Error())
		report := m.failureReport("DPI", profileID, err, false)
		return ActionResult{OK: false, Message: withReport("DPI: "+err.Error(), report), State: m.state()}
	}
	m.setDPIError("")
	_ = m.store.Update(func(c *config.Config) error {
		c.DPI.ActiveProfile = profileID
		c.DPI.LastKnownGoodProfile = profileID
		return nil
	})
	m.emitState()
	return ActionResult{OK: true, Message: readinessMessage(checks), State: m.state()}
}

func (m *Manager) StopDPI() ActionResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setBusy(true)
	defer m.setBusy(false)
	if m.dpi == nil {
		return ActionResult{OK: false, Message: "DPI недоступен на этой платформе", State: m.state()}
	}
	if !m.dpi.Running() {
		return ActionResult{OK: true, Message: "DPI уже выключен", State: m.state()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.dpi.Stop(ctx); err != nil {
		m.setDPIError(err.Error())
		return ActionResult{OK: false, Message: "Остановка DPI: " + err.Error(), State: m.state()}
	}
	m.mu.Lock()
	m.dpiRuntime = nil
	m.dpiErr = ""
	m.mu.Unlock()
	m.emitState()
	return ActionResult{OK: true, Message: "DPI остановлен. Правила nftables удалены.", State: m.state()}
}

func (m *Manager) ApplyProfile(profileID string) SwitchResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setBusy(true)
	defer m.setBusy(false)
	return m.switchProfileLocked(profileID, true)
}

func (m *Manager) RollbackProfile() SwitchResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setBusy(true)
	defer m.setBusy(false)
	cfg := m.store.Snapshot()
	prev := cfg.DPI.PreviousProfile
	if prev == "" {
		return SwitchResult{OK: false, Message: "Нет предыдущего профиля для отката", State: m.state()}
	}
	oldActive := cfg.DPI.ActiveProfile
	result := m.switchProfileLocked(prev, false)
	result.PreviousProfile = oldActive
	if result.OK {
		_ = m.store.Update(func(c *config.Config) error {
			c.DPI.PreviousProfile = oldActive
			return nil
		})
		result.State = m.state()
	}
	return result
}

func (m *Manager) switchProfileLocked(profileID string, updatePrevious bool) SwitchResult {
	profile, ok := m.catalog.Find(profileID)
	if !ok {
		return SwitchResult{OK: false, Message: "Профиль не найден: " + profileID, State: m.state()}
	}
	if !profile.Supports(runtime.GOOS) {
		return SwitchResult{OK: false, Message: fmt.Sprintf("Профиль %s не поддерживает %s", profile.Name, runtime.GOOS), State: m.state()}
	}
	cfg := m.store.Snapshot()
	previous := ""
	if m.dpi.Running() {
		m.mu.RLock()
		rt := m.dpiRuntime
		m.mu.RUnlock()
		if rt != nil {
			previous = rt.ProfileID()
		}
	}
	if previous == "" {
		previous = cfg.DPI.LastKnownGoodProfile
	}
	if m.dpi.Running() && previous == profileID {
		return SwitchResult{OK: true, Message: "Профиль уже активен", ProfileID: profileID, PreviousProfile: cfg.DPI.PreviousProfile, State: m.state()}
	}
	if updatePrevious {
		_ = m.store.Update(func(c *config.Config) error {
			c.DPI.PreviousProfile = previous
			return nil
		})
	}
	if m.dpi.Running() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := m.dpi.Stop(ctx)
		cancel()
		if err != nil {
			m.setDPIError("Не удалось остановить текущий DPI: " + err.Error())
			return SwitchResult{OK: false, Message: m.dpiErr, ProfileID: profileID, PreviousProfile: previous, State: m.state()}
		}
		m.mu.Lock()
		m.dpiRuntime = nil
		m.mu.Unlock()
	}
	checks, err := m.startDPILocked(context.Background(), profileID)
	if err != nil {
		m.setDPIError(err.Error())
		result := SwitchResult{OK: false, Message: "Профиль не применён: " + err.Error(), ProfileID: profileID, PreviousProfile: previous, Checks: checks}
		if previous != "" && previous != profileID {
			if restoreChecks, restoreErr := m.startDPILocked(context.Background(), previous); restoreErr != nil {
				result.RollbackError = restoreErr.Error()
				result.Message += ". Восстановление предыдущего профиля не удалось: " + restoreErr.Error()
				m.setDPIError(result.RollbackError)
			} else {
				result.RolledBack = true
				result.Checks = append(result.Checks, restoreChecks...)
				result.Message += ". Предыдущий профиль восстановлен"
				m.setDPIError("")
				_ = m.store.Update(func(c *config.Config) error {
					c.DPI.ActiveProfile = previous
					return nil
				})
			}
		}
		if report := m.failureReport("DPI", profileID, err, false); report != "" {
			result.Message += "\nОтчёт: " + report
		}
		result.State = m.state()
		return result
	}
	m.setDPIError("")
	savedPrevious := previous
	if savedPrevious == profileID {
		savedPrevious = ""
	}
	if err := m.store.Update(func(c *config.Config) error {
		c.DPI.ActiveProfile = profileID
		c.DPI.LastKnownGoodProfile = profileID
		if updatePrevious {
			c.DPI.PreviousProfile = savedPrevious
		}
		return nil
	}); err != nil {
		m.logger.Error("save active profile failed", "error", err)
		return SwitchResult{OK: true, Message: "Профиль запущен, но конфигурация не сохранена: " + err.Error(), ProfileID: profileID, PreviousProfile: previous, Checks: checks, State: m.state()}
	}
	m.emitState()
	return SwitchResult{OK: true, Message: readinessMessage(checks), ProfileID: profileID, PreviousProfile: previous, Checks: checks, State: m.state()}
}

func (m *Manager) startDPILocked(ctx context.Context, profileID string) ([]core.Check, error) {
	profile, ok := m.catalog.Find(profileID)
	if !ok {
		return nil, errors.New("профиль не найден")
	}
	if !profile.Supports(runtime.GOOS) {
		return nil, fmt.Errorf("профиль %s не поддерживает %s", profile.Name, runtime.GOOS)
	}
	cfg := m.store.Snapshot()
	opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	m.refreshProbe(opCtx)
	listsDir, err := prepareRuntimeLists(m.resources, m.dataDir, cfg.DPI.CustomAddresses)
	if err != nil {
		return nil, fmt.Errorf("подготовка списков: %w", err)
	}
	rt, err := m.dpi.Start(opCtx, profile, core.Options{
		Resources: m.resources,
		DataDir:   m.dataDir,
		Interface: cfg.DPI.Interface,
		ListsDir:  listsDir,
		Logger:    m.logger,
	})
	if err != nil {
		return nil, err
	}
	health := rt.Health(opCtx)
	m.mu.Lock()
	m.dpiRuntime = rt
	m.mu.Unlock()
	return health, nil
}

func (m *Manager) setDPIError(msg string) {
	m.mu.Lock()
	m.dpiErr = msg
	m.dpiRuntime = nil
	m.mu.Unlock()
	m.emitState()
}

func (m *Manager) firstCompatibleProfile() string {
	for _, p := range m.catalog.Profiles {
		if p.Supports(runtime.GOOS) {
			return p.ID
		}
	}
	return ""
}

func readinessMessage(checks []core.Check) string {
	if len(checks) == 0 {
		return "DPI-движок запущен. Фактическая эффективность не проверена."
	}
	failed := 0
	for _, c := range checks {
		if !c.OK {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Sprintf("DPI-движок запущен, но %d проверок готовности не прошли", failed)
	}
	return "DPI-движок запущен; локальные проверки готовности пройдены. Эффективность обхода не доказана."
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func nonEmpty(v []string) []string {
	out := v[:0]
	for _, s := range v {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (m *Manager) GetProfiles() []ProfileView {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	m.refreshProbe(ctx)
	m.mu.RLock()
	probe := m.probe
	m.mu.RUnlock()
	history, _ := m.diagStore.History(m.store.Snapshot().Diagnostics.HistoryLimit)
	views := make([]ProfileView, 0, len(m.catalog.Profiles))
	for _, p := range m.catalog.Profiles {
		v := ProfileView{Profile: p, EnginePresent: probe.EnginePresent, Privileged: probe.Privileged, RuntimeVerified: false}
		engine, _ := p.EngineFor(runtime.GOOS)
		switch {
		case !p.Supports(runtime.GOOS):
			v.Compatibility = "unsupported"
			v.CompatibilityNote = "Профиль не заявлен для этой ОС"
		case !probe.EnginePresent:
			v.Compatibility = "missing-engine"
			v.CompatibilityNote = "Движок отсутствует в портативной сборке"
		case !probe.EngineIntact:
			v.Compatibility = "corrupt-engine"
			v.CompatibilityNote = "Движок не прошёл проверку целостности"
		case probe.EngineVersion != "" && engine.Version != probe.EngineVersion:
			v.Compatibility = "version-mismatch"
			v.CompatibilityNote = fmt.Sprintf("Профиль: %s, движок: %s", engine.Version, probe.EngineVersion)
		case !probe.Privileged:
			v.Compatibility = "requires-privileges"
			v.CompatibilityNote = "Компоненты готовы; для запуска нужны повышенные права"
		default:
			v.Compatibility = "ready"
			v.CompatibilityNote = "Компоненты готовы; эффективность ещё не проверена"
		}
		v.Compatible = v.Compatibility == "ready" || v.Compatibility == "requires-privileges"
		for i := len(history) - 1; i >= 0 && len(v.RecentResults) < 3; i-- {
			if history[i].ProfileID == p.ID {
				v.RecentResults = append(v.RecentResults, history[i])
			}
		}
		views = append(views, v)
	}
	active := m.store.Snapshot().DPI.ActiveProfile
	if active != "" {
		sort.SliceStable(views, func(i, j int) bool {
			return views[i].ID == active && views[j].ID != active
		})
	}
	return views
}
