// Пакет update только смотрит метаданные релиза. Никаких скачиваний и запусков:
// обновление пользователь ставит сам и проверяет хеш.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const ReleaseAPI = "https://api.github.com/repos/xelira/-/releases/latest"

type Info struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Tag       string `json:"tag"`
	Available bool   `json:"available"`
	URL       string `json:"url"`
	APKURL    string `json:"apkUrl,omitempty"`
	SHAURL    string `json:"shaUrl,omitempty"`
}

type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check сравнивает текущую версию с тегом релиза. Сетевые ошибки не глотаем,
// пусть вызывающий сам решает, что показать.
func Check(ctx context.Context, current, apiURL string) (Info, error) {
	if apiURL == "" {
		apiURL = ReleaseAPI
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return Info{}, err
	}
	req.Header.Set("User-Agent", "ampersand-update-check")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("update endpoint returned HTTP %d", resp.StatusCode)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Info{}, err
	}
	latest := strings.TrimPrefix(strings.TrimSpace(r.TagName), "v")
	cur := strings.TrimPrefix(strings.TrimSpace(current), "v")
	info := Info{Current: cur, Latest: latest, Tag: r.TagName, Available: newer(latest, cur), URL: r.HTMLURL}
	for _, asset := range r.Assets {
		switch asset.Name {
		case "and-android.apk":
			info.APKURL = asset.BrowserDownloadURL
		case "SHA256SUMS.txt":
			info.SHAURL = asset.BrowserDownloadURL
		}
	}
	return info, nil
}

func newer(a, b string) bool {
	pa, pb := parse(a), parse(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parse(v string) [3]int {
	var out [3]int
	base := strings.SplitN(v, "-", 2)[0]
	parts := strings.Split(base, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}
