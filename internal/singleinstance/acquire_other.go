//go:build !android

package singleinstance

import "net"

var held net.Listener

// Acquire takes a loopback port as a lightweight cross-platform single
// instance lock. A second copy exits instead of opening a duplicate window.
func Acquire() bool {
	if held != nil {
		return true
	}
	ln, err := net.Listen("tcp", "127.0.0.1:47823")
	if err != nil {
		return false
	}
	held = ln
	return true
}
