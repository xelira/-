//go:build android

package platform

import (
	"log/slog"

	"ampersand/internal/platform/android"
)

func NewController(resources, dataDir string, logger *slog.Logger) Controller {
	return android.New(resources, dataDir, logger)
}
