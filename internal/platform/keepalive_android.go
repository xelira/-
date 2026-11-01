//go:build android

package platform

import "ampersand/internal/platform/android"

// KeepAlive starts/stops the foreground service that keeps the Fyne process
// alive while the embedded Telegram proxy is running.
func KeepAlive(enabled bool) error { return android.KeepAlive(enabled) }
