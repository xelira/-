package app

import (
	"slices"
	"strings"

	"ampersand/internal/config"
	"ampersand/internal/platform"
)

func (m *Manager) SaveSettings(s Settings) ActionResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	before := m.store.Snapshot()
	if s.Autostart != before.Autostart {
		if err := platform.SetAutostart(s.Autostart); err != nil {
			return ActionResult{OK: false, Message: "Автозапуск: " + err.Error(), State: m.state()}
		}
	}
	err := m.store.Update(func(c *config.Config) error {
		c.Appearance = config.Appearance(s.Appearance)
		c.Proxy.Host = strings.TrimSpace(s.ProxyHost)
		c.Proxy.Port = s.ProxyPort
		c.Proxy.CFProxy = s.CFProxy
		c.Proxy.PoolSize = s.PoolSize
		c.Proxy.BufferKB = s.BufferKB
		c.Proxy.AllowLAN = s.AllowLAN
		c.Proxy.StartOnLaunch = s.StartOnLaunch
		c.DPI.Interface = strings.TrimSpace(s.DPIInterface)
		c.DPI.CustomAddresses = cleanAddresses(s.CustomAddresses)
		c.DPI.StartOnLaunch = s.DPIStartOnLaunch
		c.TelegramWeb.AutoFix = s.TelegramWebAuto
		c.Autostart = s.Autostart
		if s.Autostart && !before.Autostart {
			c.Proxy.StartOnLaunch = true
			c.DPI.StartOnLaunch = true
		}
		c.Diagnostics.TimeoutSeconds = s.TimeoutSeconds
		c.Diagnostics.HistoryLimit = s.HistoryLimit
		return nil
	})
	if err != nil {
		if s.Autostart != before.Autostart {
			_ = platform.SetAutostart(before.Autostart)
		}
		return ActionResult{OK: false, Message: err.Error(), State: m.state()}
	}
	m.diagStore.SetLimit(s.HistoryLimit)
	if before.TelegramWeb.AutoFix && !s.TelegramWebAuto {
		m.mu.RLock()
		autoHosts := m.autoHostsFix
		m.mu.RUnlock()
		if autoHosts && m.telegramWeb != nil {
			if err := m.telegramWeb.Revert(); err != nil {
				m.logger.Warn("telegram web revert after disabling auto fix failed", "error", err)
			} else {
				m.clearTelegramWebAuto()
			}
		}
	}
	msg := "Настройки сохранены"
	if m.proxy.Running() && (before.Proxy.Host != s.ProxyHost || before.Proxy.Port != s.ProxyPort || before.Proxy.CFProxy != s.CFProxy) {
		msg += ". Перезапустите Telegram Proxy, чтобы применить сетевые параметры"
	}
	if m.dpi.Running() && before.DPI.Interface != s.DPIInterface {
		msg += ". Перезапустите DPI, чтобы применить сетевой интерфейс"
	}
	if m.dpi.Running() && !slices.Equal(cleanAddresses(before.DPI.CustomAddresses), cleanAddresses(s.CustomAddresses)) {
		msg += ". Перезапустите DPI, чтобы применить новые адреса"
	}
	return ActionResult{OK: true, Message: msg, State: m.state()}
}

func (m *Manager) RotateSecret() ActionResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.proxy.Running() {
		return ActionResult{OK: false, Message: "Остановите Telegram Proxy перед сменой секрета", State: m.state()}
	}
	if _, err := m.store.RotateSecret(); err != nil {
		return ActionResult{OK: false, Message: err.Error(), State: m.state()}
	}
	return ActionResult{OK: true, Message: "Секрет обновлён. Ссылка для Telegram изменилась.", State: m.state()}
}

func (m *Manager) ProxyLink() string {
	cfg := m.store.Snapshot()
	return m.proxy.Link(cfg.Proxy.Host, cfg.Proxy.Port, m.store.Secret())
}

func (m *Manager) OpenDataFolder() ActionResult {
	path := m.dataDir
	if err := platform.OpenFolder(path); err != nil {
		return ActionResult{OK: false, Message: err.Error(), State: m.state()}
	}
	return ActionResult{OK: true, Message: "Открыт каталог данных: " + path, State: m.state()}
}

func cleanAddresses(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.HasPrefix(value, "#") || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
