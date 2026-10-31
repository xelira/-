package app

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// prepareRuntimeLists делает копию списков и подмешивает пользовательские
// адреса. Так мы не трогаем ресурсы приложения, но профили видят свои IP.
func prepareRuntimeLists(resources, dataDir string, custom []string) (string, error) {
	source := filepath.Join(resources, "engines", "lists")
	if len(custom) == 0 {
		return source, nil
	}
	dest := filepath.Join(dataDir, "lists")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(source, entry.Name()), filepath.Join(dest, entry.Name())); err != nil {
			return "", err
		}
	}
	var ips, domains []string
	for _, raw := range custom {
		value := strings.TrimSpace(raw)
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		if ip := net.ParseIP(value); ip != nil {
			if ip.To4() != nil {
				ips = append(ips, value)
			}
			continue
		}
		if _, network, err := net.ParseCIDR(value); err == nil {
			if network.IP.To4() != nil {
				ips = append(ips, value)
			}
			continue
		}
		domains = append(domains, value)
	}
	if err := appendLines(filepath.Join(dest, "ipset-all.txt"), ips); err != nil {
		return "", err
	}
	if err := appendLines(filepath.Join(dest, "list-general-user.txt"), domains); err != nil {
		return "", err
	}
	return dest, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func appendLines(path string, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		seen[strings.TrimSpace(line)] = true
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		if _, err := fmt.Fprintln(f, line); err != nil {
			return err
		}
		seen[line] = true
	}
	return nil
}
