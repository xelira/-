package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"ampersand/internal/assets"
	"ampersand/internal/config"
	"ampersand/internal/diagnostics"
	"ampersand/internal/modules"
	"ampersand/internal/platform"
	"ampersand/internal/platform/core"
	"ampersand/internal/profiles"
	"ampersand/internal/telegramweb"
	"ampersand/internal/vpn"
)

type Manager struct {
	version     string
	root        string
	resources   string
	dataDir     string
	store       *config.Store
	logger      *slog.Logger
	logCloser   interface{ Close() error }
	catalog     *profiles.Catalog
	proxy       *modules.Proxy
	dpi         core.Controller
	diag        *diagnostics.Runner
	diagStore   *diagnostics.Store
	telegramWeb *telegramweb.Fix

	opMu sync.Mutex

	mu            sync.RWMutex
	busy          bool
	proxyErr      string
	dpiErr        string
	dpiRuntime    core.Runtime
	probe         core.Probe
	shuttingDown  bool
	proxyRestarts []time.Time
	autoHostsFix  bool

	vpnDetect    func() vpn.Status
	vpnWasActive bool
	vpnDismissed bool

	emit func(string, any)
}

func NewManager(version string, autoStart bool) (*Manager, error) {
	root := ResolveRoot()
	store, err := config.Open(root)
	if err != nil {
		return nil, err
	}
	resources, err := assets.Materialize(store.DataDir())
	if err != nil {
		return nil, err
	}
	logger, closer, err := newLogger(store.DataDir(), store.Secret(), false)
	if err != nil {
		return nil, err
	}
	catalog, err := profiles.Load(filepath.Join(resources, "profiles.json"))
	if err != nil {
		_ = closer.Close()
		return nil, err
	}
	m := &Manager{
		version:     version,
		root:        root,
		resources:   resources,
		dataDir:     store.DataDir(),
		store:       store,
		logger:      logger,
		logCloser:   closer,
		catalog:     catalog,
		proxy:       modules.NewProxy(resources, store.DataDir(), logger),
		dpi:         platform.NewController(resources, store.DataDir(), logger),
		telegramWeb: telegramweb.New(platform.NewHostsFile(platform.SystemHostsPath(), filepath.Join(store.DataDir(), "hosts"))),
		vpnDetect:   vpn.Detect,
	}
	m.diagStore, err = diagnostics.NewStore(store.DataDir(), store.Snapshot().Diagnostics.HistoryLimit)
	if err != nil {
		_ = closer.Close()
		return nil, err
	}
	m.diag = &diagnostics.Runner{
		Resources: resources,
		DataDir:   store.DataDir(),
		Version:   version,
		Store:     store,
		Profiles:  catalog,
		Proxy:     m.proxy,
		DPI:       m.dpi,
		Logger:    logger,
	}
	m.proxy.SetOnExit(func(failed bool) {
		if failed {
			go m.onProxyExitCallback()
		}
	})
	m.proxy.SetOnDegraded(func() { go m.onProxyDegraded() })
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	m.refreshProbe(ctx)
	cancel()
	// Инициализируем VPN-статус до автостарта. Если туннель уже поднят,
	// ничего само не запускаем: человек сначала должен нажать «Ну и че».
	m.vpnStatus()
	cfg := store.Snapshot()
	if autoStart && cfg.TelegramWeb.AutoFix && platform.HostsSupported() {
		m.startTelegramWebAutoFix()
	}
	if autoStart && !m.vpnStatus().Active {
		if cfg.Proxy.StartOnLaunch {
			go func() {
				time.Sleep(300 * time.Millisecond)
				res := m.StartProxy()
				if !res.OK {
					logger.Warn("automatic proxy start failed", "message", res.Message)
				}
			}()
		}
		if cfg.DPI.StartOnLaunch {
			go func() {
				time.Sleep(500 * time.Millisecond)
				res := m.StartDPI("")
				if !res.OK {
					logger.Warn("automatic DPI start failed", "message", res.Message)
				}
			}()
		}
	}
	logger.Info("application started", "version", version, "os", runtime.GOOS, "arch", runtime.GOARCH, "root", root)
	return m, nil
}

func (m *Manager) SetEmitter(fn func(string, any)) {
	m.mu.Lock()
	m.emit = fn
	m.mu.Unlock()
}

func (m *Manager) refreshProbe(ctx context.Context) core.Probe {
	if m.dpi == nil {
		return core.Probe{}
	}
	p := m.dpi.Probe(ctx)
	cfg := m.store.Snapshot()
	p.Interface = cfg.DPI.Interface
	p.InterfaceValid = core.ValidInterface(runtime.GOOS, cfg.DPI.Interface)
	if !p.InterfaceValid {
		p.Problems = append(p.Problems, "сетевой интерфейс не найден: "+cfg.DPI.Interface)
	}
	m.mu.Lock()
	m.probe = p
	m.mu.Unlock()
	return p
}

func (m *Manager) onProxyDegraded() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	shuttingDown := m.shuttingDown
	m.mu.Unlock()
	if shuttingDown || !m.proxy.Running() {
		return
	}
	now := time.Now()
	recent := m.proxyRestarts[:0]
	for _, t := range m.proxyRestarts {
		if now.Sub(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	m.proxyRestarts = recent
	if len(m.proxyRestarts) >= 3 {
		m.setProxyError("Telegram Proxy деградировал; лимит автоперезапусков исчерпан")
		return
	}
	m.proxyRestarts = append(m.proxyRestarts, now)
	_ = m.proxy.Stop()
	cfg := m.store.Snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := m.proxy.Start(ctx, cfg.Proxy, m.store.Secret()); err != nil {
		m.setProxyError("Не удалось перезапустить Telegram Proxy: " + err.Error())
		return
	}
	m.setProxyError("")
	m.logger.Warn("Telegram proxy restarted after health degradation")
	m.emitState()
}

func (m *Manager) onProxyExitCallback() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	if m.shuttingDown {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	// Упавший прокси поднимаем не больше трёх раз за десять минут. Хватит,
	// чтобы пережить случайный сбой, и не хватит, чтобы устроить вечный цикл.
	now := time.Now()
	recent := m.proxyRestarts[:0]
	for _, t := range m.proxyRestarts {
		if now.Sub(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	m.proxyRestarts = recent
	if len(m.proxyRestarts) >= 3 {
		m.setProxyError("Telegram Proxy остановился; лимит автоперезапусков исчерпан")
		return
	}
	m.proxyRestarts = append(m.proxyRestarts, now)
	cfg := m.store.Snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := m.proxy.Start(ctx, cfg.Proxy, m.store.Secret()); err != nil {
		m.setProxyError("Не удалось перезапустить Telegram Proxy: " + err.Error())
		_ = m.failureReport("Telegram Proxy (автоперезапуск)", "", err, false)
		return
	}
	m.setProxyError("")
	m.logger.Warn("Telegram proxy restarted after unexpected exit")
	m.emitState()
}

func (m *Manager) setProxyError(msg string) {
	m.mu.Lock()
	m.proxyErr = msg
	m.mu.Unlock()
	m.emitState()
}

func (m *Manager) Close() {
	m.opMu.Lock()
	m.mu.Lock()
	m.shuttingDown = true
	m.mu.Unlock()
	m.proxy.SetOnExit(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if m.proxy.Running() {
		if err := m.proxy.Stop(); err != nil {
			m.logger.Warn("proxy shutdown failed", "error", err)
		}
	}
	if m.dpi != nil && m.dpi.Running() {
		if err := m.dpi.Stop(ctx); err != nil {
			m.logger.Warn("DPI shutdown failed", "error", err)
		}
	}
	if m.dpi != nil {
		_ = m.dpi.Cleanup(ctx)
	}
	m.mu.RLock()
	autoHosts := m.autoHostsFix
	m.mu.RUnlock()
	if autoHosts && m.telegramWeb != nil {
		if err := m.telegramWeb.Revert(); err != nil {
			m.logger.Warn("telegram web auto revert failed", "error", err)
		} else {
			_ = os.Remove(m.telegramWebAutoMarker())
			m.mu.Lock()
			m.autoHostsFix = false
			m.mu.Unlock()
			m.logger.Info("telegram web hosts reverted on shutdown")
		}
	}
	m.opMu.Unlock()
	if m.logCloser != nil {
		_ = m.logCloser.Close()
	}
}

func (m *Manager) state() State {
	cfg := m.store.Snapshot()
	m.mu.RLock()
	busy := m.busy
	proxyErr := m.proxyErr
	dpiErr := m.dpiErr
	probe := m.probe
	dpiRuntime := m.dpiRuntime
	m.mu.RUnlock()

	proxyRunning := m.proxy.Running()
	proxyState := ModuleState{Kind: "stopped", Label: "Выключен", Detail: "Прокси не запущен"}
	if proxyRunning {
		proxyState = ModuleState{Kind: "running", Label: "Запущен", Detail: fmt.Sprintf("%s:%d принимает TCP", cfg.Proxy.Host, cfg.Proxy.Port), PID: m.proxy.PID(), Since: m.proxy.StartedAt(), LatencyMS: m.proxy.LatencyMS(), Healthy: m.proxy.Healthy()}
		if proxyState.Healthy && proxyState.LatencyMS > 0 {
			proxyState.Detail = fmt.Sprintf("работает · %d мс", proxyState.LatencyMS)
		} else if !proxyState.Healthy {
			proxyState.Detail = "работает, маршрут пока не подтверждён"
		}
		if proxyErr != "" {
			proxyState.Detail += " · " + proxyErr
		}
	} else if proxyErr != "" {
		proxyState = ModuleState{Kind: "error", Label: "Ошибка", Detail: proxyErr}
	}

	dpiRunning := m.dpi != nil && m.dpi.Running()
	if dpiRuntime != nil && !dpiRunning && dpiErr == "" {
		dpiErr = "DPI-движок неожиданно завершился"
	}
	dpiState := ModuleState{Kind: "stopped", Label: "Выключен", Detail: "DPI-движок не запущен"}
	if dpiRunning {
		pid := 0
		since := time.Now()
		version := probe.EngineVersion
		if dpiRuntime != nil {
			pid = dpiRuntime.PID()
			since = dpiRuntime.StartedAt()
		}
		dpiState = ModuleState{Kind: "running", Label: "Движок запущен", Detail: "Проверка эффективности — в разделе «Диагностика»", PID: pid, Since: since, EngineVersion: version}
		if dpiErr != "" {
			dpiState.Detail += " · " + dpiErr
		}
	} else if dpiErr != "" {
		dpiState = ModuleState{Kind: "error", Label: "Ошибка", Detail: dpiErr, EngineVersion: probe.EngineVersion}
	}

	activeName := ""
	if p, ok := m.catalog.Find(cfg.DPI.ActiveProfile); ok {
		activeName = p.Name
	}
	overall := overall(proxyRunning, dpiRunning, proxyState.Kind == "error" || dpiState.Kind == "error", busy)
	guidance := privilegeGuidance()
	vpnState := m.vpnStatus()
	state := State{
		Version:           m.version,
		OS:                runtime.GOOS,
		Arch:              runtime.GOARCH,
		Root:              m.root,
		DataDir:           m.dataDir,
		Overall:           overall,
		Proxy:             proxyState,
		DPI:               dpiState,
		ActiveProfile:     cfg.DPI.ActiveProfile,
		ActiveProfileName: activeName,
		PreviousProfile:   cfg.DPI.PreviousProfile,
		LastCheck:         cfg.Diagnostics.LastCheck,
		Privilege: PrivilegeState{
			Elevated: probe.Privileged || runtime.GOOS == "android",
			Required: runtime.GOOS != "android",
			Platform: probe.Platform,
			Guidance: guidance,
		},
		Busy:          busy,
		SetupComplete: cfg.SetupComplete,
		VPN:           vpnState,
		TelegramWeb:   m.TelegramWebStatus(),
		Settings:      settingsFromConfig(cfg),
	}
	return state
}

func telegramWebState(st telegramweb.Status) TelegramWebState {
	return TelegramWebState{
		Supported:   st.Supported,
		Applied:     st.Applied,
		IP:          st.IP,
		DNS:         append([]string(nil), st.DNS...),
		PageOK:      st.PageOK,
		WebSocketOK: st.WebSocketOK,
		HostsPath:   st.HostsPath,
		Detail:      st.Detail,
		Problems:    append([]string(nil), st.Problems...),
		CheckedAt:   st.CheckedAt,
	}
}

func overall(proxy, dpi, failed, busy bool) OverallState {
	if busy {
		return OverallState{Kind: "busy", Label: "Выполняется…", Detail: "Дождитесь завершения операции"}
	}
	if failed {
		return OverallState{Kind: "attention", Label: "Требует внимания", Detail: "Один из модулей сообщил об ошибке"}
	}
	if proxy && dpi {
		return OverallState{Kind: "running", Label: "Модули запущены", Detail: "Откройте диагностику для проверки соединений"}
	}
	if proxy || dpi {
		detail := "Работает только Telegram Proxy"
		if dpi {
			detail = "Работает только DPI"
		}
		return OverallState{Kind: "partial", Label: "Частично запущено", Detail: detail}
	}
	return OverallState{Kind: "ready", Label: "Готово", Detail: "Модули выключены"}
}

func privilegeGuidance() string {
	if runtime.GOOS == "windows" {
		return "Для DPI запустите приложение от имени администратора."
	}
	if runtime.GOOS == "linux" {
		return "Для DPI нужны root/nftables. Запустите приложение через sudo или настройте разрешение pkexec; прокси работает без root."
	}
	if runtime.GOOS == "android" {
		return "DPI работает через Android VpnService и ByeDPI. Root не нужен; при первом запуске разрешите VPN."
	}
	return "DPI не поддерживается на этой платформе."
}

func settingsFromConfig(c config.Config) Settings {
	return Settings{
		Appearance:       string(c.Appearance),
		ProxyHost:        c.Proxy.Host,
		ProxyPort:        c.Proxy.Port,
		CFProxy:          c.Proxy.CFProxy,
		PoolSize:         c.Proxy.PoolSize,
		BufferKB:         c.Proxy.BufferKB,
		AllowLAN:         c.Proxy.AllowLAN,
		StartOnLaunch:    c.Proxy.StartOnLaunch,
		DPIInterface:     c.DPI.Interface,
		CustomAddresses:  append([]string(nil), c.DPI.CustomAddresses...),
		DPIStartOnLaunch: c.DPI.StartOnLaunch,
		TelegramWebAuto:  c.TelegramWeb.AutoFix,
		Autostart:        c.Autostart,
		TimeoutSeconds:   c.Diagnostics.TimeoutSeconds,
		HistoryLimit:     c.Diagnostics.HistoryLimit,
	}
}

func (m *Manager) emitState() {
	m.mu.RLock()
	fn := m.emit
	m.mu.RUnlock()
	if fn != nil {
		fn("state:changed", m.state())
	}
}

func (m *Manager) setBusy(v bool) {
	m.mu.Lock()
	m.busy = v
	m.mu.Unlock()
	m.emitState()
}

// GetState можно дёргать из UI хоть каждую секунду, он ничего тяжёлого не делает.
func (m *Manager) GetState() State { return m.state() }

func (m *Manager) StartProxy() ActionResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setBusy(true)
	defer m.setBusy(false)
	if m.proxy.Running() {
		return ActionResult{OK: true, Message: "Telegram Proxy уже запущен", State: m.state()}
	}
	cfg := m.store.Snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := m.proxy.Start(ctx, cfg.Proxy, m.store.Secret()); err != nil {
		m.setProxyError(err.Error())
		report := m.failureReport("Telegram Proxy", "", err, false)
		return ActionResult{OK: false, Message: withReport("Telegram Proxy: "+err.Error(), report), State: m.state()}
	}
	m.setProxyError("")
	_ = platform.KeepAlive(true)
	m.emitState()
	return ActionResult{OK: true, Message: "Telegram Proxy запущен и слушает локальный порт", State: m.state()}
}

func (m *Manager) StopProxy() ActionResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setBusy(true)
	defer m.setBusy(false)
	if !m.proxy.Running() {
		return ActionResult{OK: true, Message: "Telegram Proxy уже выключен", State: m.state()}
	}
	if err := m.proxy.Stop(); err != nil {
		m.setProxyError(err.Error())
		return ActionResult{OK: false, Message: err.Error(), State: m.state()}
	}
	m.setProxyError("")
	_ = platform.KeepAlive(false)
	m.emitState()
	return ActionResult{OK: true, Message: "Telegram Proxy остановлен", State: m.state()}
}

func (m *Manager) Toggle() ActionResult {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setBusy(true)
	defer m.setBusy(false)
	proxyRunning := m.proxy.Running()
	dpiRunning := m.dpi != nil && m.dpi.Running()
	if proxyRunning || dpiRunning {
		var msgs []string
		if proxyRunning {
			if err := m.proxy.Stop(); err != nil {
				msgs = append(msgs, "Telegram Proxy: "+err.Error())
				m.setProxyError(err.Error())
			} else {
				m.setProxyError("")
				_ = platform.KeepAlive(false)
				msgs = append(msgs, "Telegram Proxy остановлен")
			}
		}
		if dpiRunning {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := m.dpi.Stop(ctx)
			cancel()
			if err != nil {
				msgs = append(msgs, "DPI: "+err.Error())
				m.setDPIError(err.Error())
			} else {
				m.mu.Lock()
				m.dpiRuntime = nil
				m.dpiErr = ""
				m.mu.Unlock()
				msgs = append(msgs, "DPI остановлен")
			}
		}
		m.emitState()
		return ActionResult{OK: true, Message: strings.Join(msgs, "; "), State: m.state()}
	}

	cfg := m.store.Snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	err := m.proxy.Start(ctx, cfg.Proxy, m.store.Secret())
	cancel()
	if err != nil {
		m.setProxyError(err.Error())
	} else {
		m.setProxyError("")
	}
	pid := cfg.DPI.ActiveProfile
	if pid == "" {
		pid = m.firstCompatibleProfile()
	}
	var dpiMsg string
	if pid == "" {
		dpiMsg = "нет доступного DPI-профиля"
	} else if _, err := m.startDPILocked(context.Background(), pid); err != nil {
		m.setDPIError(err.Error())
		dpiMsg = err.Error()
	} else {
		m.setDPIError("")
		_ = m.store.Update(func(c *config.Config) error {
			c.DPI.ActiveProfile = pid
			c.DPI.LastKnownGoodProfile = pid
			return nil
		})
		dpiMsg = "DPI запущен"
	}
	m.emitState()
	msg := "Модули включены"
	if err != nil || dpiMsg != "DPI запущен" {
		msg = "Частично включено: " + strings.Join(nonEmpty([]string{errText(err), dpiMsg}), "; ")
	}
	return ActionResult{OK: err == nil || m.proxy.Running(), Message: msg, State: m.state()}
}

// vpnStatus смотрит интерфейсы при каждом вызове: VPN может включиться
// посреди работы, и притворяться, что мы этого не видим, глупо.
func (m *Manager) vpnStatus() VPNState {
	detect := m.vpnDetect
	if detect == nil {
		detect = vpn.Detect
	}
	st := detect()
	m.mu.Lock()
	if st.Active {
		if !m.vpnWasActive {
			m.vpnDismissed = false
		}
	} else {
		m.vpnDismissed = false
	}
	m.vpnWasActive = st.Active
	frozen := st.Active && !m.vpnDismissed
	m.mu.Unlock()
	return VPNState{Active: st.Active, Interface: st.Interface, Kind: st.Kind, Frozen: frozen}
}

// DismissVPNNotice снимает заморозку до следующего включения VPN.
func (m *Manager) DismissVPNNotice() ActionResult {
	if !m.vpnStatus().Active {
		return ActionResult{OK: true, Message: "VPN не активен", State: m.state()}
	}
	m.mu.Lock()
	m.vpnDismissed = true
	m.mu.Unlock()
	return ActionResult{OK: true, Message: "Продолжаем. Если сеть начнёт чудить — вспомни про VPN.", State: m.state()}
}
