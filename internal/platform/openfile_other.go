//go:build !linux && !windows

package platform

import "errors"

func OpenFile(string) error { return errors.New("open file unsupported") }
