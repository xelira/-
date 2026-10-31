//go:build !linux && !windows

package platform

import "errors"

func SetAutostart(bool) error { return errors.New("autostart unsupported") }
