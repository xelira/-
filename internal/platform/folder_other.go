//go:build !linux && !windows

package platform

import "errors"

func OpenFolder(string) error { return errors.New("open folder unsupported") }
