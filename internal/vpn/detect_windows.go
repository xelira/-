//go:build windows

package vpn

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func Detect() Status {
	var size uint32
	flags := uint32(windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST | windows.GAA_FLAG_SKIP_DNS_SERVER)
	err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, nil, &size)
	if err != nil && err != windows.ERROR_BUFFER_OVERFLOW {
		return Status{}
	}
	if size == 0 {
		return Status{}
	}
	buf := make([]byte, size)
	first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
	if err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, first, &size); err != nil {
		return Status{}
	}
	for a := first; a != nil; a = a.Next {
		if a.OperStatus != windows.IfOperStatusUp {
			continue
		}
		friendly := windows.UTF16PtrToString(a.FriendlyName)
		description := windows.UTF16PtrToString(a.Description)
		tunnel := a.IfType == windows.IF_TYPE_TUNNEL || a.IfType == windows.IF_TYPE_PPP
		if !tunnel && !nameLooksVPN(friendly) && !nameLooksVPN(description) {
			continue
		}
		name := friendly
		if name == "" {
			name = description
		}
		kind := vpnKind(name)
		if tunnel && kind == "vpn" {
			kind = "windows-vpn"
		}
		return Status{Active: true, Interface: strings.TrimSpace(name), Kind: kind}
	}
	return Status{}
}
