// Package modules содержит встроенный Go-прокси Telegram (MTProto over WebSocket).
package modules

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"ampersand/internal/config"
	"ampersand/internal/platform/core"
	"ampersand/internal/process"
	tgwscfg "ampersand/internal/tgws/config"
	"ampersand/internal/tgws/mtpserver"
	"ampersand/internal/tgws/telegram"
	"ampersand/internal/tgws/wsbridge"
)

type Proxy struct {
	mu        sync.Mutex
	dataDir   string
	logger    *slog.Logger
	cfg       config.Proxy
	startedAt time.Time
	running   bool
	cancel    context.CancelFunc
	done      chan struct{}

	onExit       func(bool)
	onDegraded   func()
	healthCfg    tgwscfg.Config
	healthCancel context.CancelFunc
	healthy      atomic.Bool
	latencyMS    atomic.Int64
	degraded     atomic.Bool
}

func NewProxy(_ string, dataDir string, logger *slog.Logger) *Proxy {
	if logger == nil {
		logger = slog.Default()
	}
	return &Proxy{dataDir: dataDir, logger: logger}
}

func (p *Proxy) Probe() []core.Check {
	p.mu.Lock()
	running := p.running
	cfg := p.cfg
	p.mu.Unlock()
	checks := []core.Check{
		{Name: "Встроенный Telegram Proxy", OK: true, Detail: "Go MTProto-over-WebSocket"},
		{Name: "Процесс прокси", OK: running, Detail: p.statusDetail()},
	}
	if running {
		if err := process.Listening(cfg.Host, cfg.Port, 500*time.Millisecond); err == nil {
			checks = append(checks, core.Check{Name: "Локальный порт", OK: true, Detail: fmt.Sprintf("%s:%d принимает TCP", cfg.Host, cfg.Port)})
		} else {
			checks = append(checks, core.Check{Name: "Локальный порт", OK: false, Detail: fmt.Sprintf("порт %d не отвечает", cfg.Port)})
		}
	}
	return checks
}

func directWSReachable(ctx context.Context, cfg tgwscfg.Config) bool {
	if len(cfg.DCIPs) == 0 {
		return false
	}
	for dc, ip := range cfg.DCIPs {
		if ip == "" {
			continue
		}
		domains := telegram.WSDomains(dc, false)
		if len(domains) == 0 {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ws, err := wsbridge.Dial(probeCtx, cfg, ip, domains[0])
		cancel()
		if err == nil {
			_ = ws.Close()
			return true
		}
	}
	return false
}

func (p *Proxy) Start(ctx context.Context, cfg config.Proxy, secret string) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return errors.New("Telegram Proxy уже запущен")
	}
	p.mu.Unlock()
	if len(secret) != 32 {
		return errors.New("некорректный секрет прокси")
	}
	key, err := hex.DecodeString(secret)
	if err != nil || len(key) != 16 {
		return errors.New("некорректный секрет прокси")
	}
	available, portErr := process.PortAvailable(cfg.Host, cfg.Port)
	if portErr != nil {
		return portErr
	}
	if !available {
		return fmt.Errorf("порт %s:%d уже занят другим процессом", cfg.Host, cfg.Port)
	}

	tcfg := tgwscfg.Default()
	tcfg.Host = cfg.Host
	tcfg.Port = cfg.Port
	tcfg.PoolSize = cfg.PoolSize
	tcfg.BufferKB = cfg.BufferKB
	if len(cfg.DCIP) > 0 {
		dcips, err := tgwscfg.ParseDCIPList(cfg.DCIP)
		if err != nil {
			return err
		}
		tcfg.DCIPs = dcips
	}
	if cfg.CFProxy {
		tcfg.UseCFProxy = true
		tcfg.CFDomains = tgwscfg.BuiltinCFDomains()
		tcfg.CFDomainSource = tgwscfg.CFDomainSourceBuiltin
		if len(tcfg.CFDomains) > 0 {
			tcfg.CFDomain = tcfg.CFDomains[0]
		}
		// Direct Telegram DCs are blocked on many networks. Trying them first
		// costs a full DialTimeout on every new relay; use CF first when a
		// quick direct WebSocket probe fails.
		if !directWSReachable(ctx, tcfg) {
			tcfg.UseCFProxyFirst = true
			p.logger.Info("telegram direct WS unavailable, using Cloudflare first")
		}
	}
	// Логи движка не нужны в UI и не должны разрастаться.
	logger := log.New(io.Discard, "", 0)
	server := mtpserver.NewMTServer(tcfg, key, logger)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		defer close(done)
		err := server.Run(runCtx)
		p.mu.Lock()
		p.running = false
		onExit := p.onExit
		p.mu.Unlock()
		p.healthy.Store(false)
		if err != nil && runCtx.Err() == nil {
			select {
			case errCh <- err:
			default:
			}
			if onExit != nil {
				go onExit(true)
			}
		}
	}()

	waitCtx, waitCancel := context.WithTimeout(ctx, 8*time.Second)
	defer waitCancel()
	host := cfg.Host
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	if err := process.WaitListening(waitCtx, host, cfg.Port, 100*time.Millisecond); err != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
		return fmt.Errorf("прокси не открыл порт: %w", err)
	}
	select {
	case err := <-errCh:
		cancel()
		return fmt.Errorf("прокси завершился при запуске: %w", err)
	case <-done:
		cancel()
		return errors.New("прокси завершился сразу после запуска")
	default:
	}
	p.mu.Lock()
	p.cfg = cfg
	p.startedAt = time.Now()
	p.running = true
	p.cancel = cancel
	p.done = done
	p.healthCfg = tcfg
	p.healthy.Store(true)
	p.degraded.Store(false)
	healthCtx, healthCancel := context.WithCancel(runCtx)
	p.healthCancel = healthCancel
	p.mu.Unlock()
	go p.healthLoop(healthCtx)
	p.logger.Info("embedded Telegram proxy started", "host", cfg.Host, "port", cfg.Port)
	return nil
}

func (p *Proxy) Stop() error {
	p.mu.Lock()
	if !p.running || p.cancel == nil {
		p.mu.Unlock()
		return nil
	}
	cancel := p.cancel
	done := p.done
	healthCancel := p.healthCancel
	p.cancel = nil
	p.healthCancel = nil
	p.healthy.Store(false)
	p.mu.Unlock()
	if healthCancel != nil {
		healthCancel()
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		return errors.New("прокси не остановился за 5 секунд")
	}
	p.mu.Lock()
	p.running = false
	p.mu.Unlock()
	p.logger.Info("embedded Telegram proxy stopped")
	return nil
}

func (p *Proxy) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *Proxy) PID() int { return os.Getpid() }

func (p *Proxy) StartedAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startedAt
}

func (p *Proxy) Output(int) []string { return nil }

func (p *Proxy) SetOnExit(fn func(bool)) {
	p.mu.Lock()
	p.onExit = fn
	p.mu.Unlock()
}

func (p *Proxy) SetOnDegraded(fn func()) {
	p.mu.Lock()
	p.onDegraded = fn
	p.mu.Unlock()
}

func (p *Proxy) LatencyMS() int { return int(p.latencyMS.Load()) }
func (p *Proxy) Healthy() bool  { return p.healthy.Load() }
func (p *Proxy) Degraded() bool { return p.degraded.Load() }

func (p *Proxy) Link(host string, port int, secret string) string {
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	return "tg://proxy?server=" + host + "&port=" + strconv.Itoa(port) + "&secret=dd" + secret
}

func (p *Proxy) healthLoop(ctx context.Context) {
	failures := 0
	ticker := time.NewTicker(12 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		p.mu.Lock()
		cfg := p.healthCfg
		host := cfg.Host
		port := cfg.Port
		onDegraded := p.onDegraded
		p.mu.Unlock()
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		latency, err := probeUpstream(probeCtx, cfg, host, port)
		cancel()
		if err == nil {
			failures = 0
			p.degraded.Store(false)
			p.healthy.Store(true)
			p.latencyMS.Store(latency.Milliseconds())
			continue
		}
		failures++
		p.healthy.Store(false)
		p.latencyMS.Store(0)
		p.logger.Warn("telegram proxy health check failed", "error", err, "failures", failures)
		if failures >= 2 && p.degraded.CompareAndSwap(false, true) && onDegraded != nil {
			go onDegraded()
		}
	}
}

func probeUpstream(ctx context.Context, cfg tgwscfg.Config, host string, port int) (time.Duration, error) {
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	if err := process.Listening(host, port, 400*time.Millisecond); err != nil {
		return 0, fmt.Errorf("локальный порт: %w", err)
	}
	if cfg.UseCFProxyFirst {
		if d, err := probeCloudflare(ctx, cfg); err == nil {
			return d, nil
		}
	}
	if d, err := probeDirect(ctx, cfg); err == nil {
		return d, nil
	}
	if !cfg.UseCFProxyFirst {
		if d, err := probeCloudflare(ctx, cfg); err == nil {
			return d, nil
		}
	}
	return 0, errors.New("нет доступного WebSocket-маршрута")
}

func probeDirect(ctx context.Context, cfg tgwscfg.Config) (time.Duration, error) {
	for dc, ip := range cfg.DCIPs {
		if ip == "" {
			continue
		}
		domains := telegram.WSDomains(dc, false)
		if len(domains) == 0 {
			continue
		}
		start := time.Now()
		ws, err := wsbridge.Dial(ctx, cfg, ip, domains[0])
		if err == nil {
			_ = ws.Close()
			return time.Since(start), nil
		}
	}
	return 0, errors.New("direct WebSocket недоступен")
}

func probeCloudflare(ctx context.Context, cfg tgwscfg.Config) (time.Duration, error) {
	for _, base := range cfg.CFDomains {
		for dc := range cfg.DCIPs {
			cfHost := telegram.CFWSDomain(base, dc)
			start := time.Now()
			ws, err := wsbridge.Dial(ctx, cfg, cfHost, cfHost)
			if err == nil {
				_ = ws.Close()
				return time.Since(start), nil
			}
		}
	}
	return 0, errors.New("Cloudflare WebSocket недоступен")
}

func (p *Proxy) statusDetail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return fmt.Sprintf("работает на %s:%d", p.cfg.Host, p.cfg.Port)
	}
	return "выключен"
}
