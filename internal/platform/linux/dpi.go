//go:build linux

// Пакет linuxdpi — nftables + NFQUEUE/nfqws для Linux.
package linuxdpi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ampersand/internal/platform/core"
	"ampersand/internal/process"
	"ampersand/internal/profiles"
)

const (
	nftTable = "and_app"
	queueNum = "220"
	fwMark   = "0x40000000"
)

type Controller struct {
	resources string
	dataDir   string
	logger    *slog.Logger
	sup       *process.Supervisor
	current   *dpiRuntime
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
		sup:       process.New("dpi-nfqws", logger, 200),
	}
}

func (c *Controller) Platform() string { return "linux" }

func tailLines(lines []string, n int) string {
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			clean = append(clean, line)
		}
	}
	if len(clean) > n {
		clean = clean[len(clean)-n:]
	}
	for i, line := range clean {
		if len(line) > 180 {
			clean[i] = line[:180] + "…"
		}
	}
	return strings.Join(clean, " | ")
}

func (c *Controller) Probe(ctx context.Context) core.Probe {
	p := core.Probe{Platform: runtime.GOOS, Privileged: os.Geteuid() == 0}
	p.EnginePath = core.EngineBinary(c.resources, "linux", "nfqws")
	m, err := core.ReadManifest(filepath.Join(c.resources, "engines", "linux", "engine.json"))
	if err != nil {
		p.Problems = append(p.Problems, "Не найден манифест движка nfqws.")
	} else {
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
		p.Problems = append(p.Problems, "Не найден nfqws в resources/engines/linux.")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		p.Problems = append(p.Problems, "Утилита nft не найдена. Установите nftables.")
	}
	if !p.Privileged {
		p.Problems = append(p.Problems, "Для nftables/NFQUEUE приложению нужны права root.")
	}
	if opts, ok := ctx.Value(interfaceKey{}).(string); ok {
		p.Interface = opts
		p.InterfaceValid = validInterface(opts)
	} else {
		p.InterfaceValid = true
	}
	p.Supported = p.EnginePresent && p.EngineIntact
	return p
}

type interfaceKey struct{}

func WithInterface(ctx context.Context, iface string) context.Context {
	return context.WithValue(ctx, interfaceKey{}, iface)
}

func validInterface(name string) bool {
	if name == "" || name == "any" {
		return true
	}
	if strings.ContainsAny(name, " \t\r\n\"'\\;{}") {
		return false
	}
	_, err := os.Stat(filepath.Join("/sys/class/net", name))
	return err == nil
}

func (c *Controller) Start(ctx context.Context, profile profiles.Profile, opts core.Options) (core.Runtime, error) {
	if c.Running() {
		return nil, errors.New("DPI уже запущен")
	}
	if os.Geteuid() != 0 {
		return nil, errors.New("Linux DPI требует запуска с правами root (nftables и NFQUEUE)")
	}
	probe := c.Probe(ctx)
	if !probe.EnginePresent || !probe.EngineIntact {
		return nil, fmt.Errorf("компоненты nfqws не прошли проверку: %s", strings.Join(probe.Problems, "; "))
	}
	if !validInterface(opts.Interface) {
		return nil, fmt.Errorf("сетевой интерфейс %q не найден", opts.Interface)
	}
	ref, ok := profile.EngineFor("linux")
	if !ok || ref.ID != "nfqws" {
		return nil, fmt.Errorf("профиль %s не предназначен для nfqws", profile.ID)
	}
	if ref.Version != probe.EngineVersion {
		return nil, fmt.Errorf("профиль рассчитан на nfqws %s, установлен %s", ref.Version, probe.EngineVersion)
	}
	others, err := otherNFQWS()
	if err != nil {
		return nil, err
	}
	if len(others) > 0 {
		return nil, fmt.Errorf("обнаружен другой процесс nfqws (pid %s); остановите его перед переключением", strings.Join(others, ", "))
	}
	if err := c.Cleanup(ctx); err != nil {
		return nil, err
	}
	if err := applyNFT(ctx, c.dataDir, profile.Ports, opts.Interface); err != nil {
		return nil, err
	}
	root, bin, lists := profiles.EnginePaths(c.resources, "linux")
	if opts.ListsDir != "" {
		lists = opts.ListsDir
	}
	args := []string{
		"--dpi-desync-fwmark=" + fwMark,
		"--qnum=" + queueNum,
	}
	args = append(args, profiles.Expand(profile.Linux.Args, root, bin, lists)...)
	if missing := core.MissingArgFiles(args); len(missing) > 0 {
		_ = c.Cleanup(ctx)
		return nil, fmt.Errorf("в профиле отсутствуют файлы: %s", strings.Join(missing, ", "))
	}
	if err := c.sup.Start(process.Spec{Name: "nfqws", Path: probe.EnginePath, Args: args, Dir: root}); err != nil {
		_ = c.Cleanup(ctx)
		return nil, err
	}
	select {
	case <-time.After(900 * time.Millisecond):
	case <-ctx.Done():
		_ = c.Stop(context.Background())
		return nil, ctx.Err()
	}
	if !c.sup.Running() {
		_ = c.Cleanup(context.Background())
		return nil, fmt.Errorf("nfqws завершился при запуске: %s", tailLines(c.sup.Output(12), 4))
	}
	rt := &dpiRuntime{profileID: profile.ID, startedAt: time.Now(), sup: c.sup, controller: c}
	c.current = rt
	c.logger.Info("DPI engine started", "engine", "nfqws", "profile", profile.ID, "pid", c.sup.Status().PID, "interface", opts.Interface)
	return rt, nil
}

func (c *Controller) Stop(ctx context.Context) error {
	var errs []error
	if c.sup.Running() {
		if err := c.sup.Stop(4 * time.Second); err != nil {
			errs = append(errs, err)
		}
	}
	c.current = nil
	if err := c.Cleanup(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (c *Controller) Cleanup(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "nft", "delete", "table", "inet", nftTable)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "No such file or directory") || strings.Contains(msg, "does not exist") {
			return nil
		}
		return fmt.Errorf("очистка nftables: %s: %w", msg, err)
	}
	return nil
}

func (c *Controller) Running() bool         { return c.sup.Running() }
func (c *Controller) Output(n int) []string { return c.sup.Output(n) }

func (r *dpiRuntime) ProfileID() string              { return r.profileID }
func (r *dpiRuntime) PID() int                       { return r.sup.Status().PID }
func (r *dpiRuntime) StartedAt() time.Time           { return r.startedAt }
func (r *dpiRuntime) Stop(ctx context.Context) error { return r.controller.Stop(ctx) }

func (r *dpiRuntime) Health(ctx context.Context) []core.Check {
	ok, detail := nftTableExists(ctx)
	return []core.Check{
		{Name: "Процесс nfqws", OK: r.sup.Running(), Detail: fmt.Sprintf("pid=%d", r.PID())},
		{Name: "Правила nftables", OK: ok, Detail: detail},
		{Name: "Права root", OK: os.Geteuid() == 0, Detail: "Требуются для nftables/NFQUEUE"},
	}
}

func nftTableExists(ctx context.Context) (bool, string) {
	cmd := exec.CommandContext(ctx, "nft", "list", "table", "inet", nftTable)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, strings.TrimSpace(string(out))
	}
	return true, "Таблица inet " + nftTable + " загружена"
}

func otherNFQWS() ([]string, error) {
	matches, err := filepath.Glob("/proc/[0-9]*/comm")
	if err != nil {
		return nil, err
	}
	var pids []string
	self := os.Getpid()
	for _, path := range matches {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(b)) != "nfqws" {
			continue
		}
		pidText := strings.Split(path, "/")[2]
		pid, err := strconv.Atoi(pidText)
		if err != nil || pid == self {
			continue
		}
		pids = append(pids, pidText)
	}
	return pids, nil
}

func applyNFT(ctx context.Context, dataDir string, ports profiles.Ports, iface string) error {
	script := buildNFTScript(ports, iface)
	if err := os.MkdirAll(filepath.Join(dataDir, "run"), 0o700); err != nil {
		return err
	}
	path := filepath.Join(dataDir, "run", "dpi-rules.nft")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "nft", "-f", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("применение nftables: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func buildNFTScript(ports profiles.Ports, iface string) string {
	oif, iif := "", ""
	if iface != "" && iface != "any" {
		oif = fmt.Sprintf("oifname %q ", iface)
		iif = fmt.Sprintf("iifname %q ", iface)
	}
	var b strings.Builder
	b.WriteString("table inet " + nftTable + " {\n")
	b.WriteString("  chain post {\n")
	b.WriteString("    type filter hook postrouting priority mangle; policy accept;\n")
	if len(ports.TCP) > 0 {
		fmt.Fprintf(&b, "    %smeta mark and %s == 0 tcp dport { %s } ct original packets 1-6 queue num %s bypass comment \"and-app\"\n", oif, fwMark, strings.Join(ports.TCP, ", "), queueNum)
	}
	if len(ports.UDP) > 0 {
		fmt.Fprintf(&b, "    %smeta mark and %s == 0 udp dport { %s } ct original packets 1-6 queue num %s bypass comment \"and-app\"\n", oif, fwMark, strings.Join(ports.UDP, ", "), queueNum)
	}
	b.WriteString("  }\n")
	b.WriteString("  chain pre {\n")
	b.WriteString("    type filter hook prerouting priority filter; policy accept;\n")
	if len(ports.TCP) > 0 {
		fmt.Fprintf(&b, "    %stcp sport { %s } ct reply packets 1-3 queue num %s bypass comment \"and-app\"\n", iif, strings.Join(ports.TCP, ", "), queueNum)
	}
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}
