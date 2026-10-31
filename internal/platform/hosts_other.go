//go:build !linux && !windows

package platform

import "os"

func SystemHostsPath() string {
	if p := os.Getenv("AND_HOSTS_PATH"); p != "" {
		return p
	}
	return "/etc/hosts"
}

func FlushDNSCache() error { return nil }
