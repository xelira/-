//go:build !linux && !windows && !android

package platform

import "errors"

func OpenFolder(string) error { return errors.New("open folder unsupported") }
