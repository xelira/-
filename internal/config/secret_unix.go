//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"strings"
)

func secretPath(dir string) string { return filepath.Join(dir, "secret") }

func loadSecret(dir string) (string, error) {
	b, err := os.ReadFile(secretPath(dir))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func saveSecret(dir, secret string) error {
	return atomicWrite(secretPath(dir), []byte(secret+"\n"), 0o600)
}
