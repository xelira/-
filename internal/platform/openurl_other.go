//go:build !linux && !windows && !android

package platform

import "errors"

func OpenURL(string) error { return errors.New("open URL is not supported on this platform") }
