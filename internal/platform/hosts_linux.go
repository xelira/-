//go:build linux && !android

package platform

import (
	"os"
	"os/exec"
)

func SystemHostsPath() string {
	if p := os.Getenv("AND_HOSTS_PATH"); p != "" {
		return p
	}
	return "/etc/hosts"
}

func FlushDNSCache() error {
	// Best effort: different distros use different resolvers.
	for _, cmd := range [][]string{
		{"resolvectl", "flush-caches"},
		{"systemd-resolve", "--flush-caches"},
		{"nscd", "-i", "hosts"},
	} {
		if _, err := exec.LookPath(cmd[0]); err == nil {
			if err := exec.Command(cmd[0], cmd[1:]...).Run(); err == nil {
				return nil
			}
		}
	}
	return nil
}

func HostsSupported() bool { return true }
