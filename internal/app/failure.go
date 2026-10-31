package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ampersand/internal/platform"
)

// failureReport пишет человеческий txt рядом с логами и открывает его.
// Секретов тут нет: аргументы прокси идут через env, а вывод уже почищен.
func (m *Manager) failureReport(module, profile string, cause error, open bool) string {
	if m == nil {
		return ""
	}
	timestamp := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("failure-%s-%s.txt", strings.ToLower(strings.ReplaceAll(module, " ", "-")), timestamp)
	path := filepath.Join(m.dataDir, "logs", name)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	var b strings.Builder
	fmt.Fprintf(&b, "& — отчёт о сбое\n")
	fmt.Fprintf(&b, "Время: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "Модуль: %s\n", module)
	if profile != "" {
		fmt.Fprintf(&b, "Профиль: %s\n", profile)
	}
	fmt.Fprintf(&b, "ОС: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "Каталог данных: %s\n", m.dataDir)
	fmt.Fprintf(&b, "Ошибка: %v\n", cause)
	if m.dpi != nil {
		fmt.Fprintf(&b, "\n--- ЛОГ DPI-ДВИЖКА ---\n")
		for _, line := range m.dpi.Output(120) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "\n--- ЛОГ TELEGRAM PROXY ---\n")
	for _, line := range m.proxy.Output(120) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("\nОтправь этот файл, если непонятно, почему движок не запускается.\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		m.logger.Error("write failure report", "error", err)
		return ""
	}
	if open {
		if err := platform.OpenFile(path); err != nil {
			m.logger.Warn("open failure report", "error", err)
		}
	}
	return path
}

func withReport(message, path string) string {
	if path == "" {
		return message
	}
	return message + "\nОтчёт: " + path
}
