package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type EngineManifest struct {
	ID              string `json:"id"`
	Version         string `json:"version"`
	Platform        string `json:"platform"`
	Source          string `json:"source"`
	Revision        string `json:"revision"`
	Upstream        string `json:"upstream"`
	UpstreamVersion string `json:"upstream_version"`
	License         string `json:"license"`
	SHA256          string `json:"sha256"`
}

func ReadManifest(path string) (EngineManifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return EngineManifest{}, err
	}
	var m EngineManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return EngineManifest{}, err
	}
	if m.ID == "" || m.Version == "" || m.SHA256 == "" {
		return EngineManifest{}, fmt.Errorf("engine manifest is incomplete: %s", path)
	}
	return m, nil
}

func VerifyFile(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != expected {
		return fmt.Errorf("integrity check failed for %s: got %s, want %s", filepath.Base(path), got, expected)
	}
	return nil
}

func EngineBinary(resources, platform, name string) string {
	if platform == "windows" {
		name += ".exe"
	}
	return filepath.Join(resources, "engines", platform, name)
}
