package diagnostics

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"ampersand/internal/config"
	"ampersand/internal/modules"
	"ampersand/internal/platform/core"
	"ampersand/internal/process"
	"ampersand/internal/profiles"
)

type Runner struct {
	Resources string
	DataDir   string
	Version   string
	Store     *config.Store
	Profiles  *profiles.Catalog
	Proxy     *modules.Proxy
	DPI       core.Controller
	Logger    *slog.Logger
}

type Target struct {
	Name      string
	URL       string
	Service   bool
	WebSocket bool
}

func DefaultTargets() []Target {
	return []Target{
		{Name: "Google connectivity check", URL: "https://www.google.com/generate_204"},
		{Name: "YouTube", URL: "https://www.youtube.com/", Service: true},
		{Name: "Discord API", URL: "https://discord.com/api/v9/gateway", Service: true},
		{Name: "Telegram Web", URL: "https://web.telegram.org/a/", Service: true},
		{Name: "Telegram WebSocket", URL: "wss://kws2-1.web.telegram.org/apiws", Service: true, WebSocket: true},
	}
}

func (r *Runner) RunComponents(ctx context.Context) []Record {
	var out []Record
	add := func(level Level, typ, target string, ok bool, detail, reason string) {
		res := ResultPass
		if !ok {
			res = ResultFail
		}
		out = append(out, Record{Time: time.Now(), Level: level, Type: typ, Target: target, Result: res, Detail: detail, Reason: reason})
	}
	start := time.Now()
	cfg := r.Store.Snapshot()
	if err := config.Validate(cfg); err != nil {
		add(LevelComponents, "config", "Конфигурация", false, "", err.Error())
	} else {
		add(LevelComponents, "config", "Конфигурация", true, string(cfg.Appearance)+", portable JSON", "")
	}
	out[len(out)-1].DurationMS = time.Since(start).Milliseconds()

	if r.Profiles != nil {
		add(LevelComponents, "profiles", "Каталог профилей", true, fmt.Sprintf("%d профилей", len(r.Profiles.Profiles)), "")
	}
	for _, check := range r.Proxy.Probe() {
		add(LevelComponents, "proxy", check.Name, check.OK, check.Detail, "")
	}

	if r.DPI != nil {
		probe := r.DPI.Probe(ctx)
		add(LevelComponents, "dpi", probe.Platform+" engine", probe.EnginePresent && probe.EngineIntact, probe.EngineVersion+" "+probe.EnginePath, strings.Join(probe.Problems, "; "))
		if probe.Platform == "android" {
			add(LevelComponents, "dpi", "Android VpnService", true, "ByeDPI, root не требуется", "")
		} else if probe.Privileged {
			add(LevelComponents, "dpi", "Привилегии DPI", true, privilegeDetail(true), "")
		} else {
			res := ResultSkipped
			if r.DPI.Running() {
				res = ResultFail
			}
			out = append(out, Record{Time: time.Now(), Level: LevelComponents, Type: "dpi", Target: "Привилегии DPI", Result: res, Detail: privilegeDetail(false), Reason: "DPI не запущен; для его запуска нужны повышенные права"})
		}
		if !probe.InterfaceValid {
			add(LevelComponents, "dpi", "Сетевой интерфейс", false, cfg.DPI.Interface, "интерфейс не найден")
		}
		if cfg.DPI.ActiveProfile != "" {
			if _, ok := r.Profiles.Find(cfg.DPI.ActiveProfile); !ok {
				add(LevelComponents, "profiles", "Активный профиль", false, cfg.DPI.ActiveProfile, "профиль отсутствует в каталоге")
			}
		}
	}

	portFree, err := process.PortAvailable(cfg.Proxy.Host, cfg.Proxy.Port)
	switch {
	case err != nil:
		add(LevelComponents, "port", fmt.Sprintf("%s:%d", cfg.Proxy.Host, cfg.Proxy.Port), false, "", err.Error())
	case r.Proxy.Running():
		add(LevelComponents, "port", fmt.Sprintf("%s:%d", cfg.Proxy.Host, cfg.Proxy.Port), true, "порт занят собственным прокси", "")
	case portFree:
		add(LevelComponents, "port", fmt.Sprintf("%s:%d", cfg.Proxy.Host, cfg.Proxy.Port), true, "порт свободен", "")
	default:
		add(LevelComponents, "port", fmt.Sprintf("%s:%d", cfg.Proxy.Host, cfg.Proxy.Port), false, "", "порт занят другим процессом")
	}

	if err := writable(r.DataDir); err != nil {
		add(LevelComponents, "permissions", "Каталог данных", false, r.DataDir, err.Error())
	} else {
		add(LevelComponents, "permissions", "Каталог данных", true, r.DataDir, "")
	}
	return out
}

func (r *Runner) RunConnections(ctx context.Context) []Record {
	var out []Record
	cfg := r.Store.Snapshot()
	if r.Proxy.Running() {
		start := time.Now()
		err := probeTCP(ctx, cfg.Proxy.Host, cfg.Proxy.Port, time.Duration(cfg.Diagnostics.TimeoutSeconds)*time.Second)
		rec := Record{Time: time.Now(), Level: LevelConnection, Type: "local-proxy", Target: fmt.Sprintf("%s:%d", cfg.Proxy.Host, cfg.Proxy.Port), Result: ResultPass, DurationMS: time.Since(start).Milliseconds()}
		if err != nil {
			rec.Result, rec.Reason = ResultFail, err.Error()
		} else {
			rec.Detail = "Локальный TCP-порт принимает соединения"
		}
		out = append(out, rec)
	} else {
		out = append(out, Record{Time: time.Now(), Level: LevelConnection, Type: "local-proxy", Target: fmt.Sprintf("%s:%d", cfg.Proxy.Host, cfg.Proxy.Port), Result: ResultSkipped, Reason: "Telegram Proxy не запущен"})
	}
	out = append(out, r.CheckDNS(ctx, "web.telegram.org", "kws2-1.web.telegram.org")...)
	out = append(out, r.CheckTargets(ctx, DefaultTargets(), LevelConnection, "", "")...)
	dcStart := time.Now()
	err := probeTCP(ctx, "149.154.167.220", 443, time.Duration(cfg.Diagnostics.TimeoutSeconds)*time.Second)
	rec := Record{Time: time.Now(), Level: LevelConnection, Type: "telegram-dc", Target: "149.154.167.220:443", Result: ResultPass, DurationMS: time.Since(dcStart).Milliseconds(), Detail: "TCP-соединение с публичным DC Telegram"}
	if err != nil {
		rec.Result, rec.Reason = ResultFail, err.Error()
	}
	out = append(out, rec)
	return out
}

// CheckDNS показывает, какие адреса реально вернул системный резолвер.
// Для Telegram Web это принципиально: «правильный» DNS может указывать на
// заблокированный IP, и тогда DPI бессилен.
func (r *Runner) CheckDNS(ctx context.Context, hosts ...string) []Record {
	timeout := time.Duration(r.Store.Snapshot().Diagnostics.TimeoutSeconds) * time.Second
	out := make([]Record, 0, len(hosts))
	for _, host := range hosts {
		start := time.Now()
		lookupCtx, cancel := context.WithTimeout(ctx, timeout)
		addrs, err := net.DefaultResolver.LookupHost(lookupCtx, host)
		cancel()
		rec := Record{
			Time: time.Now(), Level: LevelConnection, Type: "dns", Target: host,
			DurationMS: time.Since(start).Milliseconds(),
		}
		if err != nil {
			rec.Result, rec.Reason = ResultFail, err.Error()
		} else {
			rec.Result = ResultPass
			rec.Detail = strings.Join(addrs, ", ")
		}
		out = append(out, rec)
	}
	return out
}

// CheckTargets делает DNS/TCP/TLS/HTTP/WebSocket-проверки и честно пишет
// только доступность. Это не доказательство обхода DPI и не повод рисовать
// зелёный успех.
func (r *Runner) CheckTargets(ctx context.Context, targets []Target, level Level, profileID, engineVersion string) []Record {
	timeout := time.Duration(r.Store.Snapshot().Diagnostics.TimeoutSeconds) * time.Second
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			DisableKeepAlives:     true,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	results := make([][]Record, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target Target) {
			defer wg.Done()
			u, err := url.Parse(target.URL)
			if err != nil {
				return
			}
			host := u.Hostname()
			port := u.Port()
			if port == "" {
				port = "443"
			}
			if target.WebSocket {
				start := time.Now()
				wsErr := probeWebSocket(ctx, host, timeout)
				results[i] = []Record{{
					Time: time.Now(), Level: level, Type: "websocket", Target: host + ":" + port,
					Result: passFail(wsErr), Reason: errText(wsErr), ProfileID: profileID,
					EngineVersion: engineVersion, Detail: target.Name,
					DurationMS: time.Since(start).Milliseconds(),
				}}
				return
			}
			var rows []Record
			start := time.Now()
			tcpErr := probeTCP(ctx, host, mustPort(port), timeout)
			rows = append(rows, Record{
				Time: time.Now(), Level: level, Type: "tcp", Target: host + ":" + port,
				Result: passFail(tcpErr), Reason: errText(tcpErr), ProfileID: profileID,
				EngineVersion: engineVersion, DurationMS: time.Since(start).Milliseconds(),
				Detail: target.Name,
			})
			start = time.Now()
			tlsErr := probeTLS(ctx, host, port, timeout)
			rows = append(rows, Record{
				Time: time.Now(), Level: level, Type: "tls", Target: host + ":" + port,
				Result: passFail(tlsErr), Reason: errText(tlsErr), ProfileID: profileID,
				EngineVersion: engineVersion, DurationMS: time.Since(start).Milliseconds(),
				Detail: target.Name,
			})
			start = time.Now()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
			req.Header.Set("User-Agent", "&ampersand-diagnostics/"+r.Version)
			resp, httpErr := client.Do(req)
			rec := Record{Time: time.Now(), Level: level, Type: "https", Target: target.URL, ProfileID: profileID, EngineVersion: engineVersion, Detail: target.Name, DurationMS: time.Since(start).Milliseconds()}
			if httpErr != nil {
				rec.Result, rec.Reason = ResultFail, httpErr.Error()
			} else {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
				_ = resp.Body.Close()
				rec.Detail = fmt.Sprintf("%s, HTTP %d", target.Name, resp.StatusCode)
				// 403/404 и страницы-заглушки не считаем успехом.
				if resp.StatusCode >= 200 && resp.StatusCode < 400 {
					rec.Result = ResultPass
				} else {
					rec.Result, rec.Reason = ResultFail, fmt.Sprintf("HTTP %d", resp.StatusCode)
				}
			}
			rows = append(rows, rec)
			results[i] = rows
		}(i, target)
	}
	wg.Wait()
	out := make([]Record, 0, len(targets)*3)
	for _, rows := range results {
		out = append(out, rows...)
	}
	return out
}

func passFail(err error) Result {
	if err != nil {
		return ResultFail
	}
	return ResultPass
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func probeTCP(ctx context.Context, host string, port int, timeout time.Duration) error {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	if err != nil {
		return err
	}
	return conn.Close()
}

func probeTLS(ctx context.Context, host, port string, timeout time.Duration) error {
	d := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: timeout},
		Config:    &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
	}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	return conn.Close()
}

func probeWebSocket(ctx context.Context, host string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
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
	conn, _, err := websocket.Dial(ctx, "wss://"+host+"/apiws", &websocket.DialOptions{
		HTTPClient:   client,
		HTTPHeader:   http.Header{"Origin": []string{"https://web.telegram.org"}},
		Subprotocols: []string{"binary"},
	})
	if err != nil {
		return err
	}
	_ = conn.CloseNow()
	return nil
}

func mustPort(s string) int {
	var p int
	_, _ = fmt.Sscanf(s, "%d", &p)
	return p
}

func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

func privilegeDetail(v bool) string {
	if v {
		return "повышенные права доступны"
	}
	return "DPI-движок не сможет перехватывать трафик без повышения прав"
}

func (r *Runner) BuildReport(records []Record) Report {
	return Report{
		GeneratedAt: time.Now(),
		AppVersion:  r.Version,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Summary:     Summarize(records),
		Records:     records,
		Notes: []string{
			"Отчёт не содержит секретов, токенов и содержимого трафика.",
			"HTTP-доступность контрольного адреса не доказывает, что DPI-обход работает.",
			"WebSocket-проверка подтверждает только handshake, но не вход в аккаунт Telegram.",
			"Для оценки профиля нужны согласованные проверки до и после применения.",
		},
	}
}

func (r *Runner) Export(records []Record) (string, error) {
	report := r.BuildReport(records)
	store, err := NewStore(r.DataDir, r.Store.Snapshot().Diagnostics.HistoryLimit)
	if err != nil {
		return "", err
	}
	path, err := store.Export(report, r.DataDir)
	if err != nil {
		return "", err
	}
	if r.Logger != nil {
		r.Logger.Info("diagnostics exported", "path", filepath.Base(path))
	}
	return path, nil
}
