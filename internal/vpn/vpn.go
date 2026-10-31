// Пакет vpn замечает активные туннели. Задача простая: не мешать человеку
// работать и предупредить, что VPN и DPI-обход часто тянут одеяло на себя.
package vpn

import (
	"strings"
)

type Status struct {
	Active    bool   `json:"active"`
	Interface string `json:"interface,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

func nameLooksVPN(name string) bool {
	n := strings.ToLower(name)
	prefixes := []string{"tun", "tap", "wg", "ppp", "utun", "tailscale", "zt", "nebula", "vpn", "proton", "nordlynx", "mullvad", "wintun"}
	for _, p := range prefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	keywords := []string{"wireguard", "openvpn", "vpn", "tunnel", "tap-windows", "wintun", "proton", "nord", "mullvad", "tailscale", "zerotier"}
	for _, k := range keywords {
		if strings.Contains(n, k) {
			return true
		}
	}
	return false
}

func vpnKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasPrefix(n, "wg") || strings.Contains(n, "wireguard"):
		return "wireguard"
	case strings.HasPrefix(n, "tun") || strings.HasPrefix(n, "tap") || strings.Contains(n, "openvpn"):
		return "tun/tap"
	case strings.HasPrefix(n, "ppp"):
		return "ppp"
	case strings.HasPrefix(n, "tailscale") || strings.Contains(n, "tailscale"):
		return "tailscale"
	default:
		return "vpn"
	}
}
