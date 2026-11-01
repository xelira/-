//go:build !linux && !windows && !android

package platform

import "errors"

func SetAutostart(bool) error { return errors.New("autostart unsupported") }

func AutostartSupported() bool { return false }
