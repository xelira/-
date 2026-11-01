package app

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"ampersand/internal/platform"
)

// TelegramWebStatus отдаёт дешёвый снимок: применён ли hosts и что было
// найдено при последней проверке. Сетевые запросы здесь не выполняются.
func (m *Manager) TelegramWebStatus() TelegramWebState {
	if !platform.HostsSupported() {
		return TelegramWebState{Supported: false, Detail: "На Android системный hosts недоступен. Используйте Telegram-прокси."}
	}
	if m.telegramWeb == nil {
		return TelegramWebState{Detail: "Telegram Web hosts недоступен"}
	}
	return telegramWebState(m.telegramWeb.Status())
}

// CheckTelegramWeb проверяет DNS, страницу /a/ и WebSocket kws2-1.
func (m *Manager) CheckTelegramWeb() TelegramWebState {
	if !platform.HostsSupported() {
		return TelegramWebState{Supported: false, Detail: "На Android системный hosts недоступен."}
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.telegramWeb == nil {
		return TelegramWebState{Detail: "Telegram Web hosts недоступен"}
	}
	m.setBusy(true)
	defer m.setBusy(false)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	return telegramWebState(m.telegramWeb.Check(ctx))
}

// ApplyTelegramWebFix подбирает рабочий IP и добавляет управляемый блок в hosts.
func (m *Manager) ApplyTelegramWebFix() ActionResult {
	if !platform.HostsSupported() {
		return ActionResult{OK: false, Message: "На Android системный hosts недоступен. Используйте Telegram-прокси.", State: m.state()}
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.telegramWeb == nil {
		return ActionResult{OK: false, Message: "Telegram Web hosts недоступен", State: m.state()}
	}
	// Ручное исправление означает «оставить hosts после выхода приложения».
	m.clearTelegramWebAuto()
	m.setBusy(true)
	defer m.setBusy(false)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	st, err := m.telegramWeb.Apply(ctx)
	if err != nil {
		return ActionResult{
			OK:      false,
			Message: "Telegram Web: " + err.Error(),
			State:   m.state(),
		}
	}
	return ActionResult{
		OK:      true,
		Message: "Telegram Web: hosts обновлён, web.telegram.org → " + st.IP + ". Перезапустите браузер. Для стабильности включите DPI-профиль «Telegram Web».",
		State:   m.state(),
	}
}

// RevertTelegramWebFix удаляет только блок приложения и восстанавливает
// строки hosts, которые были убраны при исправлении.
func (m *Manager) RevertTelegramWebFix() ActionResult {
	if !platform.HostsSupported() {
		return ActionResult{OK: false, Message: "На Android системный hosts недоступен.", State: m.state()}
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setBusy(true)
	defer m.setBusy(false)
	if m.telegramWeb == nil {
		return ActionResult{OK: false, Message: "Telegram Web hosts недоступен", State: m.state()}
	}
	m.clearTelegramWebAuto()
	if err := m.telegramWeb.Revert(); err != nil {
		return ActionResult{OK: false, Message: "Telegram Web: " + err.Error(), State: m.state()}
	}
	return ActionResult{OK: true, Message: "Telegram Web: записи удалены из hosts", State: m.state()}
}

func (m *Manager) telegramWebAutoMarker() string {
	return filepath.Join(m.dataDir, "telegram-web-auto")
}

// startTelegramWebAutoFix делает фикс зависимым от приложения: при запуске
// hosts проверяется и при необходимости исправляется, а при штатном выходе
// возвращается обратно. Если приложение запущено без root, фикс просто не
// применяется — никаких запросов пароля из этой функции нет.
func (m *Manager) startTelegramWebAutoFix() {
	if !platform.HostsSupported() || m.telegramWeb == nil {
		return
	}
	go func() {
		time.Sleep(700 * time.Millisecond)
		st := m.telegramWeb.Status()
		if st.Applied {
			if _, err := os.Stat(m.telegramWebAutoMarker()); err == nil {
				m.mu.Lock()
				m.autoHostsFix = true
				m.mu.Unlock()
			}
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		st, err := m.telegramWeb.Apply(ctx)
		if err != nil {
			m.logger.Warn("telegram web auto fix failed", "error", err)
			return
		}
		if err := os.WriteFile(m.telegramWebAutoMarker(), []byte(time.Now().Format(time.RFC3339)), 0o600); err != nil {
			m.logger.Warn("telegram web auto marker failed", "error", err)
		}
		m.mu.Lock()
		m.autoHostsFix = true
		m.mu.Unlock()
		m.logger.Info("telegram web hosts fixed automatically", "ip", st.IP)
		m.emitState()
	}()
}

func (m *Manager) clearTelegramWebAuto() {
	m.mu.Lock()
	m.autoHostsFix = false
	m.mu.Unlock()
	_ = os.Remove(m.telegramWebAutoMarker())
}

func (m *Manager) OpenTelegramWeb() ActionResult {
	if err := platform.OpenURL("https://web.telegram.org/a/"); err != nil {
		return ActionResult{OK: false, Message: "Не удалось открыть браузер: " + err.Error(), State: m.state()}
	}
	return ActionResult{OK: true, Message: "Открываю web.telegram.org/a/", State: m.state()}
}
