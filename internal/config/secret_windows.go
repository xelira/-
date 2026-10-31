//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func secretPath(dir string) string { return filepath.Join(dir, "secret.dat") }

func loadSecret(dir string) (string, error) {
	b, err := os.ReadFile(secretPath(dir))
	if err != nil {
		return "", err
	}
	plain, err := dpapiUnprotect(b)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	defer wipe(plain)
	return strings.TrimSpace(string(plain)), nil
}

func saveSecret(dir, secret string) error {
	plain := []byte(secret)
	protected, err := dpapiProtect(plain)
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}
	return atomicWrite(secretPath(dir), protected, 0o600)
}

func dpapiProtect(plain []byte) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(plain))}
	if len(plain) > 0 {
		in.Data = &plain[0]
	}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func dpapiUnprotect(data []byte) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(data))}
	if len(data) > 0 {
		in.Data = &data[0]
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
