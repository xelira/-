// Пакет config отвечает за переносимую конфигурацию приложения.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	CurrentVersion = 2
	DefaultPort    = 1443
)

var (
	ErrInvalidConfig = errors.New("invalid configuration")
)

// Appearance — сохранённый выбор темы.
type Appearance string

const (
	AppearanceAuto  Appearance = "auto"
	AppearanceLight Appearance = "light"
	AppearanceDark  Appearance = "dark"
)

// Config можно отдавать в интерфейс. Секрет прокси лежит отдельно.
type Config struct {
	Version       int         `json:"version"`
	Appearance    Appearance  `json:"appearance"`
	Proxy         Proxy       `json:"proxy"`
	DPI           DPI         `json:"dpi"`
	Diagnostics   Diagnostics `json:"diagnostics"`
	TelegramWeb   TelegramWeb `json:"telegramWeb"`
	Autostart     bool        `json:"autostart"`
	SetupComplete bool        `json:"setupComplete"`
}

type TelegramWeb struct {
	// AutoFix проверяет hosts при запуске приложения и возвращает его
	// при выходе. Это делает исправление зависимым от работы приложения.
	AutoFix bool `json:"autoFix"`
}

type Proxy struct {
	StartOnLaunch bool     `json:"startOnLaunch"`
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	DCIP          []string `json:"dcIp"`
	CFProxy       bool     `json:"cfProxy"`
	PoolSize      int      `json:"poolSize"`
	BufferKB      int      `json:"bufferKb"`
	AllowLAN      bool     `json:"allowLan"`
}

type DPI struct {
	ActiveProfile        string   `json:"activeProfile"`
	LastKnownGoodProfile string   `json:"lastKnownGoodProfile"`
	PreviousProfile      string   `json:"previousProfile"`
	Interface            string   `json:"interface"`
	CustomAddresses      []string `json:"customAddresses,omitempty"`
	StartOnLaunch        bool     `json:"startOnLaunch"`
}

type Diagnostics struct {
	TimeoutSeconds int        `json:"timeoutSeconds"`
	HistoryLimit   int        `json:"historyLimit"`
	LastCheck      *LastCheck `json:"lastCheck,omitempty"`
}

type LastCheck struct {
	Time         time.Time `json:"time"`
	Level        string    `json:"level"`
	Pass         int       `json:"pass"`
	Fail         int       `json:"fail"`
	Skipped      int       `json:"skipped"`
	Insufficient int       `json:"insufficient"`
	ProfileID    string    `json:"profileId,omitempty"`
}

// Defaults собирает дефолтный конфиг. Секрет создаётся при первом запуске.
func Defaults() Config {
	return Config{
		Version:    CurrentVersion,
		Appearance: AppearanceAuto,
		Proxy: Proxy{
			Host:    "127.0.0.1",
			Port:    DefaultPort,
			DCIP:    []string{"2:149.154.167.220", "4:149.154.167.220"},
			CFProxy: true,
			// Без лишнего жира: двух готовых сокетов на DC хватает обычному
			// Telegram Desktop, а буфер 128 КБ заметно скромнее дефолта upstream.
			PoolSize: 2,
			BufferKB: 128,
		},
		DPI: DPI{
			Interface: "any",
		},
		Diagnostics: Diagnostics{
			TimeoutSeconds: 6,
			HistoryLimit:   200,
		},
		TelegramWeb: TelegramWeb{
			AutoFix: true,
		},
	}
}

// Store хранит конфиг и секрет рядом с приложением, без реестра и домашних каталогов.
type Store struct {
	mu      sync.RWMutex
	dataDir string
	root    string
	cfg     Config
	secret  string
}

// Open читает конфиг из root/data. Если файла нет — создаёт всё с нуля.
func Open(root string) (*Store, error) {
	dataDir, err := DataDir(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	s := &Store{dataDir: dataDir, root: root}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// DataDir возвращает каталог данных. AND_DATA_DIR нужен только для разработки
// и тестов; в релизе это всегда <каталог с бинарником>/data.
func DataDir(root string) (string, error) {
	if override := strings.TrimSpace(os.Getenv("AND_DATA_DIR")); override != "" {
		return filepath.Abs(override)
	}
	if root == "" {
		return "", errors.New("application root is empty")
	}
	return filepath.Join(root, "data"), nil
}

func (s *Store) load() error {
	path := filepath.Join(s.dataDir, "config.json")
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.cfg = Defaults()
		secret, err := newSecret()
		if err != nil {
			return err
		}
		if err := saveSecret(s.dataDir, secret); err != nil {
			return err
		}
		s.secret = secret
		return s.persistLocked()
	case err != nil:
		return fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	applyDefaults(&cfg)
	if err := Validate(cfg); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	secret, err := loadSecret(s.dataDir)
	if errors.Is(err, os.ErrNotExist) {
		secret, err = newSecret()
		if err == nil {
			err = saveSecret(s.dataDir, secret)
		}
	}
	if err != nil {
		return fmt.Errorf("load secret: %w", err)
	}
	if len(secret) != 32 {
		return fmt.Errorf("load secret: expected 32 hex characters")
	}
	if _, err := hex.DecodeString(secret); err != nil {
		return fmt.Errorf("load secret: %w", err)
	}
	s.cfg, s.secret = cfg, secret
	return nil
}

func applyDefaults(c *Config) {
	oldVersion := c.Version
	d := Defaults()
	// Конфиги версии 1 не знали про AutoFix. Для них включаем автоисправление,
	// чтобы обновление сразу давало рабочую веб-версию Telegram.
	if oldVersion < 2 {
		c.TelegramWeb.AutoFix = true
	}
	if c.Version < CurrentVersion {
		c.Version = CurrentVersion
	}
	if c.Appearance == "" {
		c.Appearance = d.Appearance
	}
	if c.Proxy.Host == "" {
		c.Proxy.Host = d.Proxy.Host
	}
	if c.Proxy.Port == 0 {
		c.Proxy.Port = d.Proxy.Port
	}
	if len(c.Proxy.DCIP) == 0 {
		c.Proxy.DCIP = append([]string(nil), d.Proxy.DCIP...)
	}
	if c.Proxy.PoolSize == 0 {
		c.Proxy.PoolSize = d.Proxy.PoolSize
	}
	if c.Proxy.BufferKB == 0 {
		c.Proxy.BufferKB = d.Proxy.BufferKB
	}
	if c.DPI.Interface == "" {
		c.DPI.Interface = "any"
	}
	if c.Diagnostics.TimeoutSeconds == 0 {
		c.Diagnostics.TimeoutSeconds = d.Diagnostics.TimeoutSeconds
	}
	if c.Diagnostics.HistoryLimit == 0 {
		c.Diagnostics.HistoryLimit = d.Diagnostics.HistoryLimit
	}
}

// Validate проверяет структуру и то, на чём можно случайно прострелить ногу.
func Validate(c Config) error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidConfig, c.Version)
	}
	switch c.Appearance {
	case AppearanceAuto, AppearanceLight, AppearanceDark:
	default:
		return fmt.Errorf("%w: unknown appearance %q", ErrInvalidConfig, c.Appearance)
	}
	if err := validateProxy(c.Proxy); err != nil {
		return err
	}
	if c.Proxy.PoolSize < 0 || c.Proxy.PoolSize > 64 {
		return fmt.Errorf("%w: pool size must be between 0 and 64", ErrInvalidConfig)
	}
	if c.Proxy.BufferKB < 4 || c.Proxy.BufferKB > 4096 {
		return fmt.Errorf("%w: buffer must be between 4 and 4096 KB", ErrInvalidConfig)
	}
	if c.DPI.Interface == "" {
		return fmt.Errorf("%w: network interface is empty", ErrInvalidConfig)
	}
	if len(c.DPI.CustomAddresses) > 500 {
		return fmt.Errorf("%w: too many custom addresses", ErrInvalidConfig)
	}
	for _, addr := range c.DPI.CustomAddresses {
		if !validCustomAddress(addr) {
			return fmt.Errorf("%w: invalid custom address %q", ErrInvalidConfig, addr)
		}
	}
	if strings.ContainsAny(c.DPI.Interface, " \t\r\n\"'\\;") {
		return fmt.Errorf("%w: invalid network interface", ErrInvalidConfig)
	}
	if c.Diagnostics.TimeoutSeconds < 1 || c.Diagnostics.TimeoutSeconds > 60 {
		return fmt.Errorf("%w: diagnostics timeout must be 1..60 seconds", ErrInvalidConfig)
	}
	if c.Diagnostics.HistoryLimit < 20 || c.Diagnostics.HistoryLimit > 10000 {
		return fmt.Errorf("%w: history limit must be 20..10000", ErrInvalidConfig)
	}
	return nil
}

var domainLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func validCustomAddress(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" || strings.HasPrefix(v, "#") || len(v) > 253 {
		return false
	}
	if strings.Contains(v, "/") {
		_, _, err := net.ParseCIDR(v)
		return err == nil
	}
	if net.ParseIP(v) != nil {
		return true
	}
	if strings.HasPrefix(v, "*.") {
		v = v[2:]
	}
	labels := strings.Split(v, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !domainLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func validateProxy(p Proxy) error {
	host := strings.TrimSpace(p.Host)
	if host == "" {
		return fmt.Errorf("%w: proxy host is empty", ErrInvalidConfig)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: proxy host must be an IP address", ErrInvalidConfig)
	}
	if !ip.IsLoopback() && !p.AllowLAN {
		return fmt.Errorf("%w: non-loopback proxy host requires explicit LAN permission", ErrInvalidConfig)
	}
	if p.Port < 1024 || p.Port > 65535 {
		return fmt.Errorf("%w: proxy port must be 1024..65535", ErrInvalidConfig)
	}
	if len(p.DCIP) > 16 {
		return fmt.Errorf("%w: too many DC mappings", ErrInvalidConfig)
	}
	for _, entry := range p.DCIP {
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("%w: DC mapping %q must be DC:IP", ErrInvalidConfig, entry)
		}
		if strings.TrimSpace(parts[0]) == "" || net.ParseIP(parts[1]) == nil {
			return fmt.Errorf("%w: invalid DC mapping %q", ErrInvalidConfig, entry)
		}
	}
	return nil
}

// Snapshot отдаёт копию конфига, чтобы снаружи никто не менял его втихую.
func (s *Store) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.Proxy.DCIP = append([]string(nil), s.cfg.Proxy.DCIP...)
	c.DPI.CustomAddresses = append([]string(nil), s.cfg.DPI.CustomAddresses...)
	return c
}

// Secret — MTProto-секрет. Его нельзя писать в логи и отчёты. Вообще.
func (s *Store) Secret() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.secret
}

// DataDir возвращает каталог данных.
func (s *Store) DataDir() string { return s.dataDir }

// Root возвращает корень портативной сборки.
func (s *Store) Root() string { return s.root }

// Save проверяет и атомарно записывает конфиг.
func (s *Store) Save(cfg Config) error {
	applyDefaults(&cfg)
	if err := Validate(cfg); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.cfg
	s.cfg = cfg
	if err := s.persistLocked(); err != nil {
		s.cfg = old
		return err
	}
	return nil
}

// Update даёт безопасно поменять конфиг под локом и сразу сохранить.
func (s *Store) Update(fn func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	next.Proxy.DCIP = append([]string(nil), s.cfg.Proxy.DCIP...)
	next.DPI.CustomAddresses = append([]string(nil), s.cfg.DPI.CustomAddresses...)
	if err := fn(&next); err != nil {
		return err
	}
	applyDefaults(&next)
	if err := Validate(next); err != nil {
		return err
	}
	old := s.cfg
	s.cfg = next
	if err := s.persistLocked(); err != nil {
		s.cfg = old
		return err
	}
	return nil
}

// RotateSecret генерирует новый секрет и сохраняет его до возврата.
func (s *Store) RotateSecret() (string, error) {
	secret, err := newSecret()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := saveSecret(s.dataDir, secret); err != nil {
		return "", err
	}
	s.secret = secret
	return secret, nil
}

func (s *Store) persistLocked() error {
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	b = append(b, '\n')
	path := filepath.Join(s.dataDir, "config.json")
	if old, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".bak", old, 0o600)
	}
	return atomicWrite(path, b, 0o600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func newSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}
