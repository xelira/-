//go:build linux

package platform

import (
	"log/slog"

	linuxdpi "ampersand/internal/platform/linux"
)

func NewController(resources, dataDir string, logger *slog.Logger) Controller {
	return linuxdpi.New(resources, dataDir, logger)
}
