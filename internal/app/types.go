package app

import (
	"time"

	"ampersand/internal/config"
	"ampersand/internal/diagnostics"
	"ampersand/internal/platform/core"
	"ampersand/internal/profiles"
)

type ModuleState struct {
	Kind          string    `json:"kind"`
	Label         string    `json:"label"`
	Detail        string    `json:"detail"`
	PID           int       `json:"pid"`
	Since         time.Time `json:"since"`
	EngineVersion string    `json:"engineVersion,omitempty"`
	LatencyMS     int       `json:"latencyMs,omitempty"`
	Healthy       bool      `json:"healthy,omitempty"`
}

type OverallState struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

type PrivilegeState struct {
	Elevated bool   `json:"elevated"`
	Required bool   `json:"required"`
	Platform string `json:"platform"`
	Guidance string `json:"guidance"`
}

type VPNState struct {
	Active    bool   `json:"active"`
	Interface string `json:"interface,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Frozen    bool   `json:"frozen"`
}

type TelegramWebState struct {
	Supported   bool      `json:"supported"`
	Applied     bool      `json:"applied"`
	IP          string    `json:"ip,omitempty"`
	DNS         []string  `json:"dns,omitempty"`
	PageOK      bool      `json:"pageOk"`
	WebSocketOK bool      `json:"webSocketOk"`
	HostsPath   string    `json:"hostsPath,omitempty"`
	Detail      string    `json:"detail"`
	Problems    []string  `json:"problems,omitempty"`
	CheckedAt   time.Time `json:"checkedAt,omitempty"`
}

type State struct {
	Version           string            `json:"version"`
	OS                string            `json:"os"`
	Arch              string            `json:"arch"`
	Root              string            `json:"root"`
	DataDir           string            `json:"dataDir"`
	Overall           OverallState      `json:"overall"`
	Proxy             ModuleState       `json:"proxy"`
	DPI               ModuleState       `json:"dpi"`
	ActiveProfile     string            `json:"activeProfile"`
	ActiveProfileName string            `json:"activeProfileName"`
	PreviousProfile   string            `json:"previousProfile"`
	LastCheck         *config.LastCheck `json:"lastCheck,omitempty"`
	SetupComplete     bool              `json:"setupComplete"`
	Privilege         PrivilegeState    `json:"privilege"`
	VPN               VPNState          `json:"vpn"`
	TelegramWeb       TelegramWebState  `json:"telegramWeb"`
	Busy              bool              `json:"busy"`
	Settings          Settings          `json:"settings"`
}

type Settings struct {
	Appearance       string   `json:"appearance"`
	ProxyHost        string   `json:"proxyHost"`
	ProxyPort        int      `json:"proxyPort"`
	CFProxy          bool     `json:"cfProxy"`
	PoolSize         int      `json:"poolSize"`
	BufferKB         int      `json:"bufferKb"`
	AllowLAN         bool     `json:"allowLan"`
	StartOnLaunch    bool     `json:"startOnLaunch"`
	DPIInterface     string   `json:"dpiInterface"`
	CustomAddresses  []string `json:"customAddresses"`
	DPIStartOnLaunch bool     `json:"dpiStartOnLaunch"`
	TelegramWebAuto  bool     `json:"telegramWebAuto"`
	Autostart        bool     `json:"autostart"`
	TimeoutSeconds   int      `json:"timeoutSeconds"`
	HistoryLimit     int      `json:"historyLimit"`
}

type ActionResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	State   State  `json:"state"`
}

type SwitchResult struct {
	OK              bool         `json:"ok"`
	Message         string       `json:"message"`
	ProfileID       string       `json:"profileId"`
	PreviousProfile string       `json:"previousProfile"`
	RolledBack      bool         `json:"rolledBack"`
	RollbackError   string       `json:"rollbackError,omitempty"`
	Checks          []core.Check `json:"checks"`
	State           State        `json:"state"`
}

type ProfileView struct {
	profiles.Profile
	Compatible        bool                 `json:"compatible"`
	Compatibility     string               `json:"compatibility"`
	CompatibilityNote string               `json:"compatibilityNote"`
	EnginePresent     bool                 `json:"enginePresent"`
	Privileged        bool                 `json:"privileged"`
	RuntimeVerified   bool                 `json:"runtimeVerified"`
	RecentResults     []diagnostics.Record `json:"recentResults,omitempty"`
}

type DiagnosticRun struct {
	OK      bool                 `json:"ok"`
	Message string               `json:"message"`
	Records []diagnostics.Record `json:"records"`
	Summary diagnostics.Summary  `json:"summary"`
	State   State                `json:"state"`
}

type ProfileTestRun struct {
	OK         bool                 `json:"ok"`
	Message    string               `json:"message"`
	ProfileID  string               `json:"profileId"`
	Baseline   []diagnostics.Record `json:"baseline"`
	After      []diagnostics.Record `json:"after"`
	Assessment string               `json:"assessment"`
	State      State                `json:"state"`
}

type ExportResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Path    string `json:"path"`
}
