//go:build android

package platform

import (
	"os"
	"path/filepath"
)

// Android has no writable system hosts file. The Telegram Web tab is hidden
// on this platform; DNS interception would require a separate resolver.
func SystemHostsPath() string {
	if root := os.Getenv("FILESDIR"); root != "" {
		return filepath.Join(root, "hosts")
	}
	return "/data/local/tmp/and-hosts"
}

func FlushDNSCache() error { return nil }

func HostsSupported() bool { return false }
