//go:build android

package platform

import "ampersand/internal/platform/android"

func InstallAPK(path string) error { return android.InstallAPK(path) }
