//go:build android

// Package android implements the no-root DPI backend through Android VpnService.
// The packet engine (ByeDPI + hev-socks5-tunnel) runs in a dedicated :dpi
// process; Go only sends the selected strategy and reports readiness.
package android

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"ampersand/internal/platform/core"
	"ampersand/internal/process"
	"ampersand/internal/profiles"
)

const (
	dpiPort = 1080
	dpiDNS  = ""
)

type Controller struct {
	resources string
	dataDir   string
	logger    *slog.Logger
	running   bool
	startedAt time.Time
	profileID string
}

type dpiRuntime struct {
	controller *Controller
	profileID  string
	startedAt  time.Time
}

func New(resources, dataDir string, logger *slog.Logger) *Controller {
	if logger == nil {
		logger = slog.Default()
	}
	return &Controller{resources: resources, dataDir: dataDir, logger: logger}
}

func (c *Controller) Platform() string { return "android" }

func (c *Controller) Probe(context.Context) core.Probe {
	return core.Probe{
		Platform:       "android",
		Supported:      true,
		Privileged:     true,
		EnginePath:     "ByeDPI + hev-socks5-tunnel (embedded)",
		EngineVersion:  "0.17.3",
		EnginePresent:  true,
		EngineIntact:   true,
		InterfaceValid: true,
	}
}

func (c *Controller) Start(ctx context.Context, profile profiles.Profile, opts core.Options) (core.Runtime, error) {
	if len(profile.Android.Args) == 0 {
		return nil, fmt.Errorf("профиль %s не содержит Android-стратегии", profile.ID)
	}
	root, bin, lists := profiles.EnginePaths(c.resources, "android")
	if opts.ListsDir != "" {
		lists = opts.ListsDir
	}
	args := profiles.Expand(profile.Android.Args, root, bin, lists)
	// Bind the SOCKS proxy to loopback only; ByeDPI defaults to 0.0.0.0.
	args = append([]string{"-i127.0.0.1", "-p1080"}, args...)
	// Desync only TCP/TLS. UDP (voice chat, DNS and other real-time traffic)
	// stays in a separate pass-through group so it is not dropped.
	args = append(args, "-Kt,h", "-An", "-Ku")
	cfg := Config{Args: args, Port: dpiPort, DNS: dpiDNS, IPv6: false}

	code, err := StartBridge(cfg)
	if err != nil {
		return nil, fmt.Errorf("Android VPN: %w", err)
	}
	if code == 0 {
		c.logger.Info("android vpn permission requested")
	} else if code < 0 {
		return nil, errors.New("Android VPN: не удалось запустить сервис")
	}

	// The first launch shows the system VPN consent dialog. Wait for the user
	// while keeping the operation cancellable from the UI.
	deadline := time.Now().Add(90 * time.Second)
	for {
		if running, _ := RunningBridge(); running {
			break
		}
		if st, _ := StatusBridge(); strings.Contains(strings.ToLower(st), "denied") {
			return nil, errors.New("разрешение VPN не выдано")
		}
		if time.Now().After(deadline) {
			return nil, errors.New("Android VPN не запустился: нет ответа от сервиса")
		}
		select {
		case <-ctx.Done():
			_ = StopBridge()
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}

	if err := process.WaitListening(ctx, "127.0.0.1", dpiPort, 100*time.Millisecond); err != nil {
		_ = StopBridge()
		return nil, fmt.Errorf("ByeDPI не открыл порт %d: %w", dpiPort, err)
	}

	c.running = true
	c.startedAt = time.Now()
	c.profileID = profile.ID
	rt := &dpiRuntime{controller: c, profileID: profile.ID, startedAt: c.startedAt}
	c.logger.Info("android dpi started", "profile", profile.ID, "port", dpiPort)
	return rt, nil
}

func (c *Controller) Stop(context.Context) error {
	if err := StopBridge(); err != nil {
		return err
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if running, _ := RunningBridge(); !running {
			c.running = false
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.running = false
	return nil
}

func (c *Controller) Running() bool {
	if running, err := RunningBridge(); err == nil {
		c.running = running
		return running
	}
	if c.running && process.Listening("127.0.0.1", dpiPort, 250*time.Millisecond) == nil {
		return true
	}
	return false
}

func (c *Controller) Cleanup(ctx context.Context) error { return c.Stop(ctx) }
func (c *Controller) Output(int) []string               { return nil }

func (r *dpiRuntime) ProfileID() string              { return r.profileID }
func (r *dpiRuntime) PID() int                       { return 0 }
func (r *dpiRuntime) StartedAt() time.Time           { return r.startedAt }
func (r *dpiRuntime) Stop(ctx context.Context) error { return r.controller.Stop(ctx) }
func (r *dpiRuntime) Health(context.Context) []core.Check {
	checks := []core.Check{
		{Name: "Android VpnService", OK: true, Detail: "ByeDPI + hev-socks5-tunnel"},
	}
	if err := process.Listening("127.0.0.1", dpiPort, 400*time.Millisecond); err == nil {
		checks = append(checks, core.Check{Name: "ByeDPI SOCKS", OK: true, Detail: fmt.Sprintf("127.0.0.1:%d принимает TCP", dpiPort)})
	} else {
		checks = append(checks, core.Check{Name: "ByeDPI SOCKS", OK: false, Detail: "локальный порт не отвечает"})
	}
	return checks
}

// Status returns the raw JSON status from the Android service, if any.
func Status() string {
	st, _ := StatusBridge()
	return st
}

// OpenURL opens a browser through the Android intent bridge.
func OpenURL(url string) error { return OpenURLBridge(url) }

// InstallAPK opens the system package installer for a downloaded APK.
func InstallAPK(path string) error { return InstallAPKBridge(path) }

// KeepAlive keeps the Fyne process alive while the Telegram proxy is running.
func KeepAlive(enabled bool) error { return KeepAliveBridge(enabled) }

// RequestPermissions asks for notification permission (Android 13+).
func RequestPermissions() { _ = RequestPermissionsBridge() }

// Port returns the local ByeDPI SOCKS port.
func Port() int { return dpiPort }

// ListenAddress is used by diagnostics and tests.
func ListenAddress() string { return net.JoinHostPort("127.0.0.1", fmt.Sprint(dpiPort)) }
