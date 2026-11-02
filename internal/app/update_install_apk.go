package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ampersand/internal/platform"
	"ampersand/internal/update"
)

// DownloadAndInstallUpdate downloads the signed Android APK, verifies its
// SHA-256 against SHA256SUMS.txt from the same release and opens the system
// package installer. It never installs silently.
func (m *Manager) DownloadAndInstallUpdate() UpdateResult {
	if runtime.GOOS != "android" {
		return UpdateResult{OK: false, Message: "Установка APK доступна только на Android"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	info, err := update.Check(ctx, m.version, update.ReleaseAPI)
	if err != nil {
		return UpdateResult{OK: false, Message: "Проверка обновлений: " + err.Error()}
	}
	if !info.Available {
		return UpdateResult{OK: true, Current: info.Current, Message: "Установлена актуальная версия."}
	}
	if info.APKURL == "" || info.SHAURL == "" {
		return UpdateResult{OK: false, Message: "В релизе нет APK или SHA256SUMS.txt", Current: info.Current, Latest: info.Latest, Available: true, URL: info.URL}
	}

	sums, err := httpText(ctx, info.SHAURL)
	if err != nil {
		return UpdateResult{OK: false, Message: "Не удалось получить SHA256SUMS.txt: " + err.Error()}
	}
	expected := checksumFor(sums, "and-android.apk")
	if expected == "" {
		return UpdateResult{OK: false, Message: "В SHA256SUMS.txt нет and-android.apk"}
	}

	dir := filepath.Join(m.dataDir, "updates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return UpdateResult{OK: false, Message: err.Error()}
	}
	tmp, err := os.CreateTemp(dir, "and-update-*.apk")
	if err != nil {
		return UpdateResult{OK: false, Message: err.Error()}
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	defer tmp.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.APKURL, nil)
	if err != nil {
		return UpdateResult{OK: false, Message: err.Error()}
	}
	req.Header.Set("User-Agent", "ampersand-update-installer")
	resp, err := (&http.Client{Timeout: 4 * time.Minute}).Do(req)
	if err != nil {
		return UpdateResult{OK: false, Message: "Скачивание APK: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UpdateResult{OK: false, Message: fmt.Sprintf("Скачивание APK: HTTP %d", resp.StatusCode)}
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body); err != nil {
		return UpdateResult{OK: false, Message: "Скачивание APK: " + err.Error()}
	}
	if err := tmp.Close(); err != nil {
		return UpdateResult{OK: false, Message: err.Error()}
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return UpdateResult{OK: false, Message: "SHA-256 APK не совпадает"}
	}
	finalPath := filepath.Join(dir, "and-update.apk")
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return UpdateResult{OK: false, Message: err.Error()}
	}
	if err := platform.InstallAPK(finalPath); err != nil {
		return UpdateResult{OK: false, Message: "Не удалось открыть установщик: " + err.Error()}
	}
	return UpdateResult{
		OK: true, Current: info.Current, Latest: info.Latest, Available: true, CanAutoUpdate: true,
		URL: info.URL, Message: "APK скачан и проверен. Подтверди установку в системном окне.",
	}
}

func httpText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ampersand-update-installer")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b), err
}

func checksumFor(sums, name string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == name {
			return fields[0]
		}
	}
	return ""
}
