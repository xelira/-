//go:build android

// Package jni exposes the JNI helpers that Fyne keeps in an internal package.
// It is used only by the Android platform backend.
package jni

import "fyne.io/fyne/v2/internal/driver/mobile/mobileinit"

// Run executes fn on a goroutine attached to the Android JVM. The ctx argument
// is the current android.content.Context (the NativeActivity).
func Run(fn func(vm, jniEnv, ctx uintptr) error) error {
	return mobileinit.RunOnJVM(fn)
}
