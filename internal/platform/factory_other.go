//go:build !windows && !linux

package platform

import (
	"context"
	"errors"
	"log/slog"

	"ampersand/internal/profiles"
)

func NewController(resources, dataDir string, logger *slog.Logger) Controller {
	return unsupportedController{}
}

type unsupportedController struct{}

func (unsupportedController) Platform() string { return "unsupported" }
func (unsupportedController) Probe(ctx context.Context) Probe {
	return Probe{Problems: []string{"Платформа не поддерживается"}}
}
func (unsupportedController) Start(ctx context.Context, p profiles.Profile, o Options) (Runtime, error) {
	return nil, errors.New("platform unsupported")
}
func (unsupportedController) Stop(context.Context) error    { return nil }
func (unsupportedController) Running() bool                 { return false }
func (unsupportedController) Cleanup(context.Context) error { return nil }
