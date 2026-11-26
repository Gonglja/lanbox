//go:build android && cgo

// Command lanboxjni 用 -buildmode=c-shared 编成 liblanbox.so，供 Kotlin 通过 JNI 调用。
//
// 走 JNI 而不是 gomobile bind 的原因：gomobile（2026 版）要求把 golang.org/x/mobile
// 作为 tool 依赖写进 go.mod，并把 go 指令抬到 1.26；这个仓库要保持「服务端零第三方
// 依赖 + Go 1.25」。手写这几个 JNI 包装只多几十行，构建只依赖 NDK。
package main

/*
#include <jni.h>
#include <stdlib.h>
#include <string.h>

// lanbox_copy_jstring 把 Java String 复制成 C 字符串（调用方负责 free）。
static char *lanbox_copy_jstring(JNIEnv *env, jstring s) {
	if (s == NULL) {
		return NULL;
	}
	const char *c = (*env)->GetStringUTFChars(env, s, NULL);
	if (c == NULL) {
		return NULL;
	}
	char *out = strdup(c);
	(*env)->ReleaseStringUTFChars(env, s, c);
	return out;
}

static jstring lanbox_new_jstring(JNIEnv *env, const char *s) {
	if (s == NULL) {
		s = "";
	}
	return (*env)->NewStringUTF(env, s);
}
*/
import "C"

import (
	"unsafe"

	"lanbox/mobile"
)

func main() {}

// goString 把 jstring 转成 Go string。
func goString(env *C.JNIEnv, s C.jstring) string {
	cs := C.lanbox_copy_jstring(env, s)
	if cs == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cs))
	return C.GoString(cs)
}

// newJString 把 Go string 转成 jstring。
func newJString(env *C.JNIEnv, s string) C.jstring {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	return C.lanbox_new_jstring(env, cs)
}

//export Java_top_glj0_lanbox_Native_configure
func Java_top_glj0_lanbox_Native_configure(env *C.JNIEnv, obj C.jobject, dataDir, listen, hostname, version C.jstring) {
	mobile.Configure(
		goString(env, dataDir),
		goString(env, listen),
		goString(env, hostname),
		goString(env, version),
	)
}

//export Java_top_glj0_lanbox_Native_start
func Java_top_glj0_lanbox_Native_start(env *C.JNIEnv, obj C.jobject) C.jstring {
	if err := mobile.Start(); err != nil {
		return newJString(env, err.Error())
	}
	return newJString(env, "")
}

//export Java_top_glj0_lanbox_Native_stop
func Java_top_glj0_lanbox_Native_stop(env *C.JNIEnv, obj C.jobject) {
	mobile.Stop()
}

//export Java_top_glj0_lanbox_Native_running
func Java_top_glj0_lanbox_Native_running(env *C.JNIEnv, obj C.jobject) C.jboolean {
	if mobile.Running() {
		return C.JNI_TRUE
	}
	return C.JNI_FALSE
}

//export Java_top_glj0_lanbox_Native_addrs
func Java_top_glj0_lanbox_Native_addrs(env *C.JNIEnv, obj C.jobject) C.jstring {
	return newJString(env, mobile.Addrs())
}

//export Java_top_glj0_lanbox_Native_certPin
func Java_top_glj0_lanbox_Native_certPin(env *C.JNIEnv, obj C.jobject) C.jstring {
	return newJString(env, mobile.CertPin())
}

//export Java_top_glj0_lanbox_Native_loopbackToken
func Java_top_glj0_lanbox_Native_loopbackToken(env *C.JNIEnv, obj C.jobject) C.jstring {
	return newJString(env, mobile.LoopbackToken())
}

//export Java_top_glj0_lanbox_Native_logs
func Java_top_glj0_lanbox_Native_logs(env *C.JNIEnv, obj C.jobject) C.jstring {
	return newJString(env, mobile.Logs())
}

//export Java_top_glj0_lanbox_Native_adminPassword
func Java_top_glj0_lanbox_Native_adminPassword(env *C.JNIEnv, obj C.jobject) C.jstring {
	return newJString(env, mobile.AdminPassword())
}
