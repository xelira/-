//go:build android

package platform

import "ampersand/internal/platform/android"

func OpenURL(url string) error { return android.OpenURL(url) }
