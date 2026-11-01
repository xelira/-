#include <jni.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <errno.h>

extern int server_fd;
extern int main(int argc, char **argv);

static pthread_mutex_t proxy_lock = PTHREAD_MUTEX_INITIALIZER;
static int proxy_started;
static int proxy_running;

int __wrap_daemon(int nochdir, int noclose) {
    (void)nochdir;
    (void)noclose;
    errno = EPERM;
    return -1;
}

JNIEXPORT jint JNICALL
Java_com_and_app_NativeProxy_start(JNIEnv *env, jclass clazz, jobjectArray args) {
    (void)clazz;
    pthread_mutex_lock(&proxy_lock);
    if (proxy_started) {
        pthread_mutex_unlock(&proxy_lock);
        return -2;
    }
    proxy_started = 1;
    pthread_mutex_unlock(&proxy_lock);

    int argc = (*env)->GetArrayLength(env, args);
    char **argv = calloc((size_t)argc + 1, sizeof(char *));
    int result = -1;
    if (!argv) goto done;

    for (int i = 0; i < argc; i++) {
        jstring arg = (jstring)(*env)->GetObjectArrayElement(env, args, i);
        if (!arg) goto cleanup;
        const char *text = (*env)->GetStringUTFChars(env, arg, NULL);
        if (text) {
            argv[i] = strdup(text);
            (*env)->ReleaseStringUTFChars(env, arg, text);
        }
        (*env)->DeleteLocalRef(env, arg);
        if (!argv[i]) goto cleanup;
    }

    pthread_mutex_lock(&proxy_lock);
    server_fd = -1;
    proxy_running = 1;
    pthread_mutex_unlock(&proxy_lock);

    result = main(argc, argv);

    pthread_mutex_lock(&proxy_lock);
    proxy_running = 0;
    proxy_started = 0;
    pthread_mutex_unlock(&proxy_lock);

cleanup:
    for (int i = 0; i < argc; i++) free(argv[i]);
    free(argv);
done:
    return result;
}

JNIEXPORT jint JNICALL
Java_com_and_app_NativeProxy_stop(JNIEnv *env, jclass clazz) {
    (void)env;
    (void)clazz;
    pthread_mutex_lock(&proxy_lock);
    int result = (proxy_running && server_fd >= 0) ? shutdown(server_fd, SHUT_RDWR) : -1;
    pthread_mutex_unlock(&proxy_lock);
    return result;
}
