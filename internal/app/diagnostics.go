package app

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"ampersand/internal/config"
	"ampersand/internal/diagnostics"
)

// RunDiagnostics гоняет проверки компонентов и сети, пишет историю
// и обновляет сводку «последняя проверка» на главной.
func (m *Manager) RunDiagnostics(scope string) DiagnosticRun {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setBusy(true)
	defer m.setBusy(false)
	if scope == "" {
		scope = "quick"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var records []diagnostics.Record
	switch scope {
	case "components":
		records = m.diag.RunComponents(ctx)
	case "connections":
		records = m.diag.RunConnections(ctx)
	case "quick", "full":
		records = append(records, m.diag.RunComponents(ctx)...)
		records = append(records, m.diag.RunConnections(ctx)...)
	default:
		return DiagnosticRun{OK: false, Message: "Неизвестный набор проверок: " + scope, State: m.state()}
	}
	appendErr := m.diagStore.Append(records)
	summary := diagnostics.Summarize(records)
	level := "A+B"
	if scope == "components" {
		level = "A"
	} else if scope == "connections" {
		level = "B"
	}
	_ = m.store.Update(func(c *config.Config) error {
		c.Diagnostics.LastCheck = &config.LastCheck{
			Time:         time.Now(),
			Level:        level,
			Pass:         summary.Pass,
			Fail:         summary.Fail,
			Skipped:      summary.Skipped,
			Insufficient: summary.Insufficient,
		}
		return nil
	})
	m.emitState()
	msg := fmt.Sprintf("Проверки: %d успешно, %d с ошибкой, %d пропущено", summary.Pass, summary.Fail, summary.Skipped)
	if appendErr != nil {
		msg += ". История не сохранена: " + appendErr.Error()
	}
	return DiagnosticRun{OK: appendErr == nil, Message: msg, Records: records, Summary: summary, State: m.state()}
}

// TestProfile временно включает профиль и сравнивает HTTPS-доступность до/после.
// Потом возвращает прежнее состояние DPI. И не делает вид, что пара HTTP-кодов
// доказывает обход DPI — это всего лишь то, что мы реально увидели.
func (m *Manager) TestProfile(profileID string) ProfileTestRun {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setBusy(true)
	defer m.setBusy(false)
	profile, ok := m.catalog.Find(profileID)
	if !ok {
		return ProfileTestRun{OK: false, Message: "Профиль не найден: " + profileID, ProfileID: profileID, State: m.state()}
	}
	if !profile.Supports(runtime.GOOS) {
		return ProfileTestRun{OK: false, Message: "Профиль не поддерживает эту ОС", ProfileID: profileID, State: m.state()}
	}
	probe := m.refreshProbe(context.Background())
	if !probe.Privileged {
		return ProfileTestRun{OK: false, Message: "Для проверки профиля нужны повышенные права: " + privilegeGuidance(), ProfileID: profileID, State: m.state()}
	}
	wasRunning := m.dpi.Running()
	previous := ""
	if wasRunning {
		m.mu.RLock()
		if m.dpiRuntime != nil {
			previous = m.dpiRuntime.ProfileID()
		}
		m.mu.RUnlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if m.dpi.Running() {
		if err := m.dpi.Stop(ctx); err != nil {
			m.setDPIError(err.Error())
			return ProfileTestRun{OK: false, Message: "Не удалось остановить текущий DPI: " + err.Error(), ProfileID: profileID, State: m.state()}
		}
		m.mu.Lock()
		m.dpiRuntime = nil
		m.mu.Unlock()
	}

	targets := diagnostics.DefaultTargets()
	baseline := m.diag.CheckTargets(ctx, targets, diagnostics.LevelEfficacy, "", "")
	engineVersion := ""
	if ref, ok := profile.EngineFor(runtime.GOOS); ok {
		engineVersion = ref.Version
	}
	var after []diagnostics.Record
	var applyErr error
	if _, err := m.startDPILocked(ctx, profileID); err != nil {
		applyErr = err
	} else {
		after = m.diag.CheckTargets(ctx, targets, diagnostics.LevelEfficacy, profileID, engineVersion)
	}
	var restoreErr error
	if m.dpi.Running() {
		if err := m.dpi.Stop(ctx); err != nil {
			restoreErr = err
		}
		m.mu.Lock()
		m.dpiRuntime = nil
		m.mu.Unlock()
	}
	if wasRunning && previous != "" {
		if _, err := m.startDPILocked(ctx, previous); err != nil {
			restoreErr = errors.Join(restoreErr, err)
			m.setDPIError("Не удалось восстановить предыдущий DPI-профиль: " + err.Error())
		} else {
			m.setDPIError("")
		}
	}
	all := append(append([]diagnostics.Record{}, baseline...), after...)
	appendErr := m.diagStore.Append(all)
	summary := diagnostics.Summarize(all)
	_ = m.store.Update(func(c *config.Config) error {
		c.Diagnostics.LastCheck = &config.LastCheck{
			Time:         time.Now(),
			Level:        "C",
			Pass:         summary.Pass,
			Fail:         summary.Fail,
			Skipped:      summary.Skipped,
			Insufficient: summary.Insufficient,
			ProfileID:    profileID,
		}
		return nil
	})
	m.emitState()
	assessment := assessEfficacy(baseline, after)
	msg := assessment
	if applyErr != nil {
		msg = "Профиль не удалось запустить для проверки: " + applyErr.Error()
	} else if restoreErr != nil {
		msg += " Восстановление предыдущего состояния DPI завершилось ошибкой: " + restoreErr.Error()
	}
	if appendErr != nil {
		msg += " История не сохранена: " + appendErr.Error()
	}
	return ProfileTestRun{
		OK:         applyErr == nil && restoreErr == nil && appendErr == nil,
		Message:    msg,
		ProfileID:  profileID,
		Baseline:   baseline,
		After:      after,
		Assessment: assessment,
		State:      m.state(),
	}
}

func assessEfficacy(before, after []diagnostics.Record) string {
	beforeMap := map[string]diagnostics.Result{}
	for _, r := range before {
		if r.Type == "https" || r.Type == "websocket" {
			beforeMap[r.Type+"|"+r.Target] = r.Result
		}
	}
	var improved, worsened, sameUp, sameDown, unknown int
	for _, r := range after {
		if r.Type != "https" && r.Type != "websocket" {
			continue
		}
		old, ok := beforeMap[r.Type+"|"+r.Target]
		if !ok {
			unknown++
			continue
		}
		switch {
		case old != diagnostics.ResultPass && r.Result == diagnostics.ResultPass:
			improved++
		case old == diagnostics.ResultPass && r.Result != diagnostics.ResultPass:
			worsened++
		case old == diagnostics.ResultPass && r.Result == diagnostics.ResultPass:
			sameUp++
		default:
			sameDown++
		}
	}
	parts := []string{}
	if improved > 0 {
		parts = append(parts, fmt.Sprintf("после применения стало доступно целей: %d", improved))
	}
	if worsened > 0 {
		parts = append(parts, fmt.Sprintf("перестало быть доступным: %d", worsened))
	}
	if sameUp > 0 {
		parts = append(parts, fmt.Sprintf("доступно до и после: %d", sameUp))
	}
	if sameDown > 0 {
		parts = append(parts, fmt.Sprintf("недоступно до и после: %d", sameDown))
	}
	if unknown > 0 {
		parts = append(parts, fmt.Sprintf("недостаточно данных: %d", unknown))
	}
	if len(parts) == 0 {
		return "Недостаточно данных. HTTPS-проверки не дали сопоставимого результата."
	}
	return strings.Join(parts, "; ") + ". Это не доказывает обход DPI: результат зависит от сети, DNS и конкретного сервиса."
}

func (m *Manager) DiagnosticsHistory(limit int) []diagnostics.Record {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	records, err := m.diagStore.History(limit)
	if err != nil {
		m.logger.Error("read diagnostics history", "error", err)
		return nil
	}
	return records
}

func (m *Manager) ExportDiagnostics() ExportResult {
	records := m.DiagnosticsHistory(m.store.Snapshot().Diagnostics.HistoryLimit)
	if len(records) == 0 {
		return ExportResult{OK: false, Message: "История проверок пуста; сначала выполните диагностику"}
	}
	path, err := m.diag.Export(records)
	if err != nil {
		return ExportResult{OK: false, Message: "Не удалось экспортировать отчёт: " + err.Error()}
	}
	return ExportResult{OK: true, Message: "Отчёт сохранён без секретов: " + path, Path: path}
}
