//go:build linux && !android

package vpn

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	arphrdNone = 65534
	arphrdPPP  = 512
)

func Detect() Status {
	ifaces, err := net.Interfaces()
	if err != nil {
		return Status{}
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		base := filepath.Join("/sys/class/net", iface.Name)
		state, _ := os.ReadFile(filepath.Join(base, "operstate"))
		if strings.TrimSpace(string(state)) == "down" {
			continue
		}
		rawType, _ := os.ReadFile(filepath.Join(base, "type"))
		ifType, _ := strconv.Atoi(strings.TrimSpace(string(rawType)))
		isVpn := nameLooksVPN(iface.Name) || ifType == arphrdNone || ifType == arphrdPPP
		if !isVpn || !hasIPv4Route(iface.Name) {
			continue
		}
		kind := vpnKind(iface.Name)
		if kind == "vpn" && ifType == arphrdPPP {
			kind = "ppp"
		}
		return Status{Active: true, Interface: iface.Name, Kind: kind}
	}
	return Status{}
}

// hasIPv4Route отсекает TUN-пустышки: интерфейс поднят, а маршрутов через
// него нет. Mihomo/Clash так умеют висеть, когда VPN фактически выключен.
func hasIPv4Route(name string) bool {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] != name {
			continue
		}
		flags, _ := strconv.ParseUint(fields[3], 16, 32)
		if flags&0x1 != 0 {
			return true
		}
	}
	return false
}
