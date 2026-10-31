//go:build windows

package platform

import (
	"log/slog"

	windowsdpi "ampersand/internal/platform/windows"
)

func NewController(resources, dataDir string, logger *slog.Logger) Controller {
	return windowsdpi.New(resources, dataDir, logger)
}
