package app

import (
	"os"
	"path/filepath"
)

// ResolveRoot — каталог рядом с бинарником. Данные и runtime-движки всегда
// лежат в <root>/data; рабочая директория нужна только для запуска из исходников.
func ResolveRoot() string {
	if v := os.Getenv("AND_ROOT"); v != "" {
		if abs, err := filepath.Abs(v); err == nil {
			return abs
		}
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		return filepath.Dir(exe)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
