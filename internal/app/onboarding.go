package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"time"

	"github.com/coder/websocket"

	"ampersand/internal/config"
	"ampersand/internal/diagnostics"
)

type OnboardingResult struct {
	OK          bool
	Message     string
	ProfileID   string
	ProfileName string
	Records     []diagnostics.Record
	State       State
}

func (m *Manager) SkipOnboarding() ActionResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.store.Update(func(c *config.Config) error {
		c.SetupComplete = true
		return nil
	}); err != nil {
		return ActionResult{OK: false, Message: err.Error(), State: m.state()}
	}
	m.emitState()
	return ActionResult{OK: true, Message: "Настройка пропущена", State: m.state()}
}

// AutoConfigure запускает первичную диагностику. Если есть права на DPI,
// быстро перебирает профили и оставляет тот, который хоть как-то открывает
// YouTube/Telegram. Если сеть лежит целиком — не врёт про успех.
func (m *Manager) AutoConfigure() OnboardingResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setBusy(true)
	defer m.setBusy(false)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	probe := m.refreshProbe(ctx)
	var records []diagnostics.Record
	records = append(records, m.diag.RunComponents(ctx)...)
	records = append(records, m.diag.CheckDNS(ctx, "web.telegram.org", "kws2-1.web.telegram.org")...)
	telegramReachable := quickTelegramReachable(ctx)
	if !telegramReachable {
		records = append(records, diagnostics.Record{
			Time: time.Now(), Level: diagnostics.LevelConnection, Type: "telegram-web",
			Target: "web.telegram.org", Result: diagnostics.ResultFail,
			Detail: "текущий DNS/IP Telegram не принимает TCP 443",
			Reason: "нужен hosts-фикс или VPN; DPI не помогает, если IP заблокирован",
		})
	}

	cfg := m.store.Snapshot()
	first := m.firstCompatibleProfile()
	chosen := cfg.DPI.ActiveProfile
	if chosen == "" {
		chosen = cfg.DPI.LastKnownGoodProfile
	}
	if chosen == "" {
		chosen = first
	}
	message := "Первичные настройки выставлены"

	if !probe.Privileged {
		records = append(records, m.diag.RunConnections(ctx)...)
		message += ". DPI не проверялся: нет прав"
	} else if m.dpi != nil {
		dnsCtx, stopDNS := context.WithTimeout(ctx, 3*time.Second)
		_, dnsErr := net.DefaultResolver.LookupHost(dnsCtx, "www.youtube.com")
		stopDNS()
		if dnsErr != nil {
			records = append(records, diagnostics.Record{
				Time: time.Now(), Level: diagnostics.LevelConnection, Type: "dns",
				Target: "www.youtube.com", Result: diagnostics.ResultFail,
				Reason: dnsErr.Error(),
			})
			message = "DNS не разрешает YouTube. Профили DPI не помогут, пока DNS сломан — включи Secure DNS/DoH или смени DNS."
			_ = m.store.Update(func(c *config.Config) error {
				c.SetupComplete = true
				if chosen != "" {
					c.DPI.ActiveProfile = chosen
					c.DPI.LastKnownGoodProfile = chosen
				}
				return nil
			})
			if len(records) > 0 {
				_ = m.diagStore.Append(records)
			}
			m.emitState()
			name := ""
			if p, ok := m.catalog.Find(chosen); ok {
				name = p.Name
			}
			return OnboardingResult{OK: true, Message: message, ProfileID: chosen, ProfileName: name, Records: records, State: m.state()}
		}
		if m.dpi.Running() {
			_ = m.dpi.Stop(ctx)
		}
		type candidate struct {
			id      string
			name    string
			score   int
			latency time.Duration
		}
		var best candidate
		timeout := 2 * time.Second
		for _, profile := range m.catalog.Profiles {
			if !profile.Supports(runtime.GOOS) {
				continue
			}
			if ctx.Err() != nil {
				break
			}
			started := time.Now()
			if _, err := m.startDPILocked(ctx, profile.ID); err != nil {
				continue
			}
			score := 0
			var latency time.Duration
			// Проверяем не только HTML, но и WebSocket: Telegram Web без
			// kws*.web.telegram.org/apiws не работает, даже если страница
			// открылась. YouTube даёт ещё один независимый сигнал.
			if ok, took := quickHTTPS(ctx, "https://www.youtube.com/", timeout); ok {
				score++
				latency += took
			}
			if telegramReachable {
				pageOK, pageTook := quickHTTPS(ctx, "https://web.telegram.org/a/", timeout)
				if pageOK {
					score++
					latency += pageTook
				}
				wsOK, wsTook := quickWebSocket(ctx, "kws2-1.web.telegram.org", timeout)
				if wsOK {
					score += 2
					latency += wsTook
				}
			}
			_ = m.dpi.Stop(context.Background())
			m.mu.Lock()
			m.dpiRuntime = nil
			m.mu.Unlock()
			records = append(records, diagnostics.Record{
				Time: time.Now(), Level: diagnostics.LevelEfficacy, Type: "profile-tune",
				Target: profile.ID, Result: tuneResult(score), ProfileID: profile.ID,
				EngineVersion: profile.Engines[runtime.GOOS].Version,
				Detail:        fmt.Sprintf("YouTube/Telegram page/WS: %d/4", score),
				DurationMS:    time.Since(started).Milliseconds(),
			})
			if score == 0 {
				continue
			}
			if best.id == "" || score > best.score || (score == best.score && latency < best.latency) {
				best = candidate{id: profile.ID, name: profile.Name, score: score, latency: latency}
			}
			if score >= 4 {
				break
			}
		}
		if best.id != "" {
			chosen = best.id
			if _, err := m.startDPILocked(ctx, best.id); err == nil {
				message = "Выбран профиль " + best.name + ". DPI уже запущен."
			} else {
				message = "Лучший профиль " + best.name + " найден, но запустить его сейчас не удалось: " + err.Error()
			}
		} else if first != "" {
			chosen = first
			message += ". Рабочий профиль не найден — оставлен " + first + ". Если не открывается web.telegram.org, проверь hosts в Настройках → Telegram Web."
		}
	}

	_ = m.store.Update(func(c *config.Config) error {
		c.SetupComplete = true
		if chosen != "" {
			c.DPI.ActiveProfile = chosen
			c.DPI.LastKnownGoodProfile = chosen
		}
		return nil
	})
	if len(records) > 0 {
		_ = m.diagStore.Append(records)
		summary := diagnostics.Summarize(records)
		_ = m.store.Update(func(c *config.Config) error {
			c.Diagnostics.LastCheck = &config.LastCheck{
				Time: time.Now(), Level: "onboarding", Pass: summary.Pass, Fail: summary.Fail,
				Skipped: summary.Skipped, Insufficient: summary.Insufficient,
			}
			return nil
		})
	}
	m.emitState()
	name := ""
	if p, ok := m.catalog.Find(chosen); ok {
		name = p.Name
	}
	return OnboardingResult{OK: true, Message: message, ProfileID: chosen, ProfileName: name, Records: records, State: m.state()}
}

func tuneResult(score int) diagnostics.Result {
	if score > 0 {
		return diagnostics.ResultPass
	}
	return diagnostics.ResultFail
}

func quickTelegramReachable(ctx context.Context) bool {
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(lookupCtx, "web.telegram.org")
	if err != nil {
		return false
	}
	for _, ip := range addrs {
		for attempt := 0; attempt < 2; attempt++ {
			d := net.Dialer{Timeout: 1500 * time.Millisecond}
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "443"))
			if err == nil {
				_ = conn.Close()
				return true
			}
		}
	}
	return false
}

func quickWebSocket(ctx context.Context, host string, timeout time.Duration) (bool, time.Duration) {
	start := time.Now()
	wsCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:             nil,
			ForceAttemptHTTP2: false,
			DisableKeepAlives: true,
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	conn, _, err := websocket.Dial(wsCtx, "wss://"+host+"/apiws", &websocket.DialOptions{
		HTTPClient:   client,
		HTTPHeader:   http.Header{"Origin": []string{"https://web.telegram.org"}},
		Subprotocols: []string{"binary"},
	})
	if err != nil {
		return false, time.Since(start)
	}
	_ = conn.CloseNow()
	return true, time.Since(start)
}

func quickHTTPS(ctx context.Context, url string, timeout time.Duration) (bool, time.Duration) {
	start := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, 0
	}
	request.Header.Set("User-Agent", "ampersand-onboarding/1")
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:             nil,
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
			DisableKeepAlives: true,
		},
	}
	resp, err := client.Do(request)
	if err != nil {
		return false, time.Since(start)
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 500, time.Since(start)
}
