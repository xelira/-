package core

import (
	"os"
	"path/filepath"
	"strings"
)

// MissingArgFiles ищет абсолютные пути в аргументах DPI, которых нет на диске.
func MissingArgFiles(args []string) []string {
	var missing []string
	for _, arg := range args {
		_, value, ok := strings.Cut(arg, "=")
		if !ok || value == "" {
			continue
		}
		value = strings.Trim(value, "\"")
		if !filepath.IsAbs(value) {
			continue
		}
		if _, err := os.Stat(value); err != nil {
			missing = append(missing, value)
		}
	}
	return missing
}
