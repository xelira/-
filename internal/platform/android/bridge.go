//go:build android

package android

/*
#include <jni.h>
#include <stdlib.h>
#include <string.h>

static jint and_call_int_str(JNIEnv *env, jobject ctx, const char *name, const char *sig, const char *value) {
	jclass cls = (*env)->GetObjectClass(env, ctx);
	if (!cls) return -99;
	jmethodID mid = (*env)->GetMethodID(env, cls, name, sig);
	if (!mid) {
		(*env)->DeleteLocalRef(env, cls);
		return -99;
	}
	jstring arg = (*env)->NewStringUTF(env, value);
	jint result = (*env)->CallIntMethod(env, ctx, mid, arg);
	if (arg) (*env)->DeleteLocalRef(env, arg);
	(*env)->DeleteLocalRef(env, cls);
	return result;
}

static jboolean and_call_bool(JNIEnv *env, jobject ctx, const char *name, const char *sig) {
	jclass cls = (*env)->GetObjectClass(env, ctx);
	if (!cls) return JNI_FALSE;
	jmethodID mid = (*env)->GetMethodID(env, cls, name, sig);
	if (!mid) {
		(*env)->DeleteLocalRef(env, cls);
		return JNI_FALSE;
	}
	jboolean result = (*env)->CallBooleanMethod(env, ctx, mid);
	(*env)->DeleteLocalRef(env, cls);
	return result;
}

static void and_call_void(JNIEnv *env, jobject ctx, const char *name, const char *sig) {
	jclass cls = (*env)->GetObjectClass(env, ctx);
	if (!cls) return;
	jmethodID mid = (*env)->GetMethodID(env, cls, name, sig);
	if (!mid) {
		(*env)->DeleteLocalRef(env, cls);
		return;
	}
	(*env)->CallVoidMethod(env, ctx, mid);
	(*env)->DeleteLocalRef(env, cls);
}

static void and_call_void_str(JNIEnv *env, jobject ctx, const char *name, const char *sig, const char *value) {
	jclass cls = (*env)->GetObjectClass(env, ctx);
	if (!cls) return;
	jmethodID mid = (*env)->GetMethodID(env, cls, name, sig);
	if (!mid) {
		(*env)->DeleteLocalRef(env, cls);
		return;
	}
	jstring arg = (*env)->NewStringUTF(env, value);
	(*env)->CallVoidMethod(env, ctx, mid, arg);
	if (arg) (*env)->DeleteLocalRef(env, arg);
	(*env)->DeleteLocalRef(env, cls);
}

static void and_call_void_bool(JNIEnv *env, jobject ctx, const char *name, const char *sig, jboolean value) {
	jclass cls = (*env)->GetObjectClass(env, ctx);
	if (!cls) return;
	jmethodID mid = (*env)->GetMethodID(env, cls, name, sig);
	if (!mid) {
		(*env)->DeleteLocalRef(env, cls);
		return;
	}
	(*env)->CallVoidMethod(env, ctx, mid, value);
	(*env)->DeleteLocalRef(env, cls);
}

static char *and_call_string(JNIEnv *env, jobject ctx, const char *name, const char *sig) {
	jclass cls = (*env)->GetObjectClass(env, ctx);
	if (!cls) return NULL;
	jmethodID mid = (*env)->GetMethodID(env, cls, name, sig);
	if (!mid) {
		(*env)->DeleteLocalRef(env, cls);
		return NULL;
	}
	jstring result = (jstring)(*env)->CallObjectMethod(env, ctx, mid);
	(*env)->DeleteLocalRef(env, cls);
	if (!result) return NULL;
	const char *chars = (*env)->GetStringUTFChars(env, result, NULL);
	char *copy = chars ? strdup(chars) : NULL;
	if (chars) (*env)->ReleaseStringUTFChars(env, result, chars);
	(*env)->DeleteLocalRef(env, result);
	return copy;
}

static char *and_exception(JNIEnv *env) {
	if (!(*env)->ExceptionCheck(env)) return NULL;
	jthrowable exc = (*env)->ExceptionOccurred(env);
	(*env)->ExceptionClear(env);
	if (!exc) return strdup("JNI exception");
	jclass cls = (*env)->GetObjectClass(env, exc);
	jmethodID mid = (*env)->GetMethodID(env, cls, "toString", "()Ljava/lang/String;");
	char *copy = NULL;
	if (mid) {
		jstring text = (jstring)(*env)->CallObjectMethod(env, exc, mid);
		if (text) {
			const char *chars = (*env)->GetStringUTFChars(env, text, NULL);
			if (chars) {
				copy = strdup(chars);
				(*env)->ReleaseStringUTFChars(env, text, chars);
			}
			(*env)->DeleteLocalRef(env, text);
		}
	}
	(*env)->DeleteLocalRef(env, cls);
	(*env)->DeleteLocalRef(env, exc);
	if (!copy) copy = strdup("JNI exception");
	return copy;
}
*/
import "C"

import (
	"encoding/json"
	"errors"
	"unsafe"

	"fyne.io/fyne/v2/driver/mobile/jni"
)

// Config is the payload sent to the Kotlin VpnService.
type Config struct {
	Args []string `json:"args"`
	Port int      `json:"port"`
	DNS  string   `json:"dns"`
	IPv6 bool     `json:"ipv6"`
}

func withEnv(fn func(env *C.JNIEnv, ctx C.jobject) error) error {
	return jni.Run(func(_, jniEnv, ctx uintptr) error {
		env := (*C.JNIEnv)(unsafe.Pointer(jniEnv))
		if err := fn(env, C.jobject(unsafe.Pointer(ctx))); err != nil {
			return err
		}
		if msg := C.and_exception(env); msg != nil {
			defer C.free(unsafe.Pointer(msg))
			return errors.New(C.GoString(msg))
		}
		return nil
	})
}

// StartBridge launches the Android VpnService. It returns 1 when the service
// is starting, 0 when the system VPN permission dialog was requested and a
// negative value on error.
func StartBridge(cfg Config) (int, error) {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return -1, err
	}
	arg := C.CString(string(payload))
	defer C.free(unsafe.Pointer(arg))
	name := C.CString("startDpi")
	defer C.free(unsafe.Pointer(name))
	sig := C.CString("(Ljava/lang/String;)I")
	defer C.free(unsafe.Pointer(sig))
	var result C.jint
	err = withEnv(func(env *C.JNIEnv, ctx C.jobject) error {
		result = C.and_call_int_str(env, ctx, name, sig, arg)
		return nil
	})
	return int(result), err
}

func StopBridge() error {
	name := C.CString("stopDpi")
	defer C.free(unsafe.Pointer(name))
	sig := C.CString("()V")
	defer C.free(unsafe.Pointer(sig))
	return withEnv(func(env *C.JNIEnv, ctx C.jobject) error {
		C.and_call_void(env, ctx, name, sig)
		return nil
	})
}

func RunningBridge() (bool, error) {
	name := C.CString("isDpiRunning")
	defer C.free(unsafe.Pointer(name))
	sig := C.CString("()Z")
	defer C.free(unsafe.Pointer(sig))
	var result C.jboolean
	err := withEnv(func(env *C.JNIEnv, ctx C.jobject) error {
		result = C.and_call_bool(env, ctx, name, sig)
		return nil
	})
	return result != 0, err
}

func StatusBridge() (string, error) {
	name := C.CString("dpiStatus")
	defer C.free(unsafe.Pointer(name))
	sig := C.CString("()Ljava/lang/String;")
	defer C.free(unsafe.Pointer(sig))
	var result *C.char
	err := withEnv(func(env *C.JNIEnv, ctx C.jobject) error {
		result = C.and_call_string(env, ctx, name, sig)
		return nil
	})
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	defer C.free(unsafe.Pointer(result))
	return C.GoString(result), nil
}

func InstallAPKBridge(path string) error {
	name := C.CString("installApk")
	defer C.free(unsafe.Pointer(name))
	sig := C.CString("(Ljava/lang/String;)V")
	defer C.free(unsafe.Pointer(sig))
	arg := C.CString(path)
	defer C.free(unsafe.Pointer(arg))
	return withEnv(func(env *C.JNIEnv, ctx C.jobject) error {
		C.and_call_void_str(env, ctx, name, sig, arg)
		return nil
	})
}

func OpenURLBridge(url string) error {
	name := C.CString("openUrl")
	defer C.free(unsafe.Pointer(name))
	sig := C.CString("(Ljava/lang/String;)V")
	defer C.free(unsafe.Pointer(sig))
	arg := C.CString(url)
	defer C.free(unsafe.Pointer(arg))
	return withEnv(func(env *C.JNIEnv, ctx C.jobject) error {
		C.and_call_void_str(env, ctx, name, sig, arg)
		return nil
	})
}

func KeepAliveBridge(enabled bool) error {
	name := C.CString("keepAlive")
	defer C.free(unsafe.Pointer(name))
	sig := C.CString("(Z)V")
	defer C.free(unsafe.Pointer(sig))
	value := C.jboolean(0)
	if enabled {
		value = C.jboolean(1)
	}
	return withEnv(func(env *C.JNIEnv, ctx C.jobject) error {
		C.and_call_void_bool(env, ctx, name, sig, value)
		return nil
	})
}

func RequestPermissionsBridge() error {
	name := C.CString("requestPermissions")
	defer C.free(unsafe.Pointer(name))
	sig := C.CString("()V")
	defer C.free(unsafe.Pointer(sig))
	return withEnv(func(env *C.JNIEnv, ctx C.jobject) error {
		C.and_call_void(env, ctx, name, sig)
		return nil
	})
}
