//go:build windows

// Пакет windowsdpi — адаптер WinDivert/WinWS для Windows.
package windowsdpi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"ampersand/internal/platform/core"
	"ampersand/internal/process"
	"ampersand/internal/profiles"
)

type Controller struct {
	resources string
	dataDir   string
	logger    *slog.Logger
	sup       *process.Supervisor
	current   *dpiRuntime
	manifest  core.EngineManifest
}

type dpiRuntime struct {
	profileID  string
	startedAt  time.Time
	sup        *process.Supervisor
	controller *Controller
}

func New(resources, dataDir string, logger *slog.Logger) *Controller {
	if logger == nil {
		logger = slog.Default()
	}
	return &Controller{
		resources: resources,
		dataDir:   dataDir,
		logger:    logger,
		sup:       process.New("dpi-winws", logger, 200),
	}
}

func (c *Controller) Platform() string { return "windows" }

func (c *Controller) isElevated() bool {
	token := windows.GetCurrentProcessToken()
	return token.IsElevated()
}

func (c *Controller) Probe(context.Context) core.Probe {
	p := core.Probe{Platform: runtime.GOOS, Privileged: c.isElevated()}
	p.EnginePath = core.EngineBinary(c.resources, "windows", "winws")
	m, err := core.ReadManifest(filepath.Join(c.resources, "engines", "windows", "engine.json"))
	if err != nil {
		p.Problems = append(p.Problems, "Не найден манифест движка WinWS.")
	} else {
		c.manifest = m
		p.EngineVersion = m.Version
		if _, err := os.Stat(p.EnginePath); err == nil {
			p.EnginePresent = true
			if err := core.VerifyFile(p.EnginePath, m.SHA256); err != nil {
				p.Problems = append(p.Problems, err.Error())
			} else {
				p.EngineIntact = true
			}
		}
	}
	if !p.EnginePresent {
		p.Problems = append(p.Problems, "Не найден winws.exe в портативном каталоге resources/engines/windows.")
	}
	for _, f := range []string{"WinDivert.dll", "WinDivert64.sys"} {
		if _, err := os.Stat(filepath.Join(c.resources, "engines", "windows", f)); err != nil {
			p.Problems = append(p.Problems, "Отсутствует "+f+".")
		}
	}
	if !p.Privileged {
		p.Problems = append(p.Problems, "Для WinDivert требуются права администратора.")
	}
	p.InterfaceValid = true
	p.Supported = p.EnginePresent && p.EngineIntact
	return p
}

func (c *Controller) Start(ctx context.Context, profile profiles.Profile, opts core.Options) (core.Runtime, error) {
	if c.Running() {
		return nil, errors.New("DPI уже запущен")
	}
	probe := c.Probe(ctx)
	if !probe.Privileged {
		return nil, errors.New("Windows DPI требует запуска приложения от имени администратора (WinDivert)")
	}
	ref, ok := profile.EngineFor("windows")
	if !ok || ref.ID != "winws" {
		return nil, fmt.Errorf("профиль %s не предназначен для WinWS", profile.ID)
	}
	if probe.EngineVersion != "" && ref.Version != probe.EngineVersion {
		return nil, fmt.Errorf("профиль рассчитан на WinWS %s, установлен %s", ref.Version, probe.EngineVersion)
	}
	if !probe.EnginePresent || !probe.EngineIntact {
		return nil, fmt.Errorf("компоненты WinWS не прошли проверку: %s", strings.Join(probe.Problems, "; "))
	}
	if killed := process.KillStale(filepath.Dir(probe.EnginePath)); len(killed) > 0 {
		c.logger.Info("removed stale winws process", "pids", killed)
	}
	root, _, _ := profiles.EnginePaths(c.resources, "windows")
	args := buildWinWSArgs(profile, c.resources, opts.ListsDir)
	if missing := core.MissingArgFiles(args); len(missing) > 0 {
		return nil, fmt.Errorf("в профиле отсутствуют файлы: %s", strings.Join(missing, ", "))
	}
	if err := c.sup.Start(process.Spec{
		Name: "winws",
		Path: probe.EnginePath,
		Args: args,
		Dir:  root,
	}); err != nil {
		return nil, err
	}
	select {
	case <-time.After(1200 * time.Millisecond):
	case <-ctx.Done():
		_ = c.sup.Stop(2 * time.Second)
		return nil, ctx.Err()
	}
	if !c.sup.Running() {
		logs := strings.Join(c.sup.Output(8), "\n")
		return nil, fmt.Errorf("WinWS завершился при запуске. %s", logs)
	}
	rt := &dpiRuntime{profileID: profile.ID, startedAt: time.Now(), sup: c.sup, controller: c}
	c.current = rt
	c.logger.Info("DPI engine started", "engine", "winws", "profile", profile.ID, "pid", c.sup.Status().PID)
	return rt, nil
}

func (c *Controller) Stop(context.Context) error {
	if !c.sup.Running() {
		c.current = nil
		return nil
	}
	err := c.sup.Stop(4 * time.Second)
	c.current = nil
	return err
}

func (c *Controller) Cleanup(context.Context) error { return nil }

func (c *Controller) Running() bool         { return c.sup.Running() }
func (c *Controller) Output(n int) []string { return c.sup.Output(n) }

func buildWinWSArgs(profile profiles.Profile, resources, runtimeLists string) []string {
	_, bin, lists := profiles.EnginePaths(resources, "windows")
	if runtimeLists != "" {
		lists = runtimeLists
	}
	args := []string{"--wf-tcp=" + profile.Windows.WFTCP, "--wf-udp=" + profile.Windows.WFUDP}
	expanded := profiles.Expand(profile.Windows.Args, filepath.Join(resources, "engines", "windows"), bin, lists)
	for i := range expanded {
		expanded[i] = filepath.FromSlash(expanded[i])
	}
	return append(args, expanded...)
}

func (r *dpiRuntime) ProfileID() string              { return r.profileID }
func (r *dpiRuntime) PID() int                       { return r.sup.Status().PID }
func (r *dpiRuntime) StartedAt() time.Time           { return r.startedAt }
func (r *dpiRuntime) Stop(ctx context.Context) error { return r.controller.Stop(ctx) }

func (r *dpiRuntime) Health(ctx context.Context) []core.Check {
	checks := []core.Check{{Name: "Процесс WinWS", OK: r.sup.Running(), Detail: fmt.Sprintf("pid=%d", r.PID())}}
	probe := r.controller.Probe(ctx)
	checks = append(checks,
		core.Check{Name: "Права администратора", OK: probe.Privileged, Detail: "WinDivert требует повышенных прав"},
		core.Check{Name: "Целостность winws.exe", OK: probe.EngineIntact, Detail: probe.EngineVersion},
	)
	return checks
}
