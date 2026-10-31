// Package assets вшивает профили, списки и native-движки в бинарник и
// разворачивает их в data/runtime при запуске.
package assets

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:files
var content embed.FS

func Materialize(dataDir string) (string, error) {
	// nfqws сбрасывает привилегии и должен иметь возможность пройти к runtime.
	_ = os.Chmod(dataDir, 0o711)
	root := filepath.Join(dataDir, "runtime")
	_ = os.MkdirAll(root, 0o755)
	_ = os.Chmod(root, 0o755)
	err := fs.WalkDir(content, "files", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("files", path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			return os.Chmod(dst, 0o755)
		}
		data, err := content.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if filepath.Ext(dst) == "" || filepath.Ext(dst) == ".exe" {
			mode = 0o755
		}
		if old, err := os.ReadFile(dst); err == nil && string(old) == string(data) {
			return os.Chmod(dst, mode)
		}
		if err := os.WriteFile(dst, data, mode); err != nil {
			return err
		}
		return os.Chmod(dst, mode)
	})
	if err != nil {
		return "", err
	}
	return root, nil
}
