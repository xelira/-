package core

import (
	"os"
	"path/filepath"
	"strings"
)

func ValidInterface(goos, name string) bool {
	if goos != "linux" {
		return true
	}
	if name == "" || name == "any" {
		return true
	}
	if strings.ContainsAny(name, " \t\r\n\"'\\;{}") {
		return false
	}
	_, err := os.Stat(filepath.Join("/sys/class/net", name))
	return err == nil
}
