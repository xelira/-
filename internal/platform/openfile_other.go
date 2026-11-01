//go:build !linux && !windows && !android

package platform

import "errors"

func OpenFile(string) error { return errors.New("open file unsupported") }
