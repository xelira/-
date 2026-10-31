// Пакет core — общие типы для платформенных DPI-адаптеров.
package core

import (
	"context"
	"log/slog"
	"time"

	"ampersand/internal/profiles"
)

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type Probe struct {
	Platform       string   `json:"platform"`
	Supported      bool     `json:"supported"`
	Privileged     bool     `json:"privileged"`
	EnginePath     string   `json:"enginePath"`
	EngineVersion  string   `json:"engineVersion"`
	EnginePresent  bool     `json:"enginePresent"`
	EngineIntact   bool     `json:"engineIntact"`
	Interface      string   `json:"interface"`
	InterfaceValid bool     `json:"interfaceValid"`
	Problems       []string `json:"problems"`
}

type Options struct {
	Resources string
	DataDir   string
	Interface string
	ListsDir  string
	Logger    *slog.Logger
}

type Runtime interface {
	ProfileID() string
	PID() int
	StartedAt() time.Time
	Stop(context.Context) error
	Health(context.Context) []Check
}

type Controller interface {
	Platform() string
	Probe(context.Context) Probe
	Start(context.Context, profiles.Profile, Options) (Runtime, error)
	Stop(context.Context) error
	Running() bool
	Cleanup(context.Context) error
	Output(int) []string
}
