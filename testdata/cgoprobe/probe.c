#include <dlfcn.h>
#include <errno.h>
#include <pthread.h>
#include <stdio.h>

#include "probe.h"
#include "_cgo_export.h"

int probe_add(int a, int b) {
	return a + b;
}

// probe_printf needs libcosmo's stdio. It flushes, because Go exits without C's atexit handlers.
int probe_printf(const char *word) {
	int count = printf("c printf: %s %d\n", word, 42);
	fflush(stdout);
	return count;
}

// probe_errno needs libcosmo's per-thread errno, which cgo returns as the Go error.
int probe_errno(void) {
	errno = ENOENT;
	return -1;
}

// probe_callback calls back into Go on the calling thread.
int probe_callback(int value) {
	return goTriple(value) + 1;
}

static void *probe_thread_main(void *arg) {
	int *value = arg;
	*value = goTriple(*value);
	return NULL;
}

// probe_thread calls into Go from a thread that C created and Go never saw.
int probe_thread(int value) {
	pthread_t thread;
	int err = pthread_create(&thread, NULL, probe_thread_main, &value);
	if (err != 0) {
		return -err;
	}
	err = pthread_join(thread, NULL);
	if (err != 0) {
		return -err;
	}
	return value;
}

static void probe_dlerror(char *msg, int len) {
	const char *text = dlerror();
	snprintf(msg, len, "%s", text ? text : "");
}

// probe_dlopen_missing opens a library that does not exist, with plain dlopen
// as any cgo package spells it, and copies what dlerror says.
int probe_dlopen_missing(char *msg, int len) {
	void *handle = dlopen("libcgoprobe-missing.so", RTLD_NOW);
	probe_dlerror(msg, len);
	return handle == NULL ? 0 : 1;
}

// probe_dlopen opens a host library and finds a symbol in it, with plain
// dlopen, dlsym and dlclose. A negative return names the call that failed.
int probe_dlopen(const char *lib, const char *sym, char *msg, int len) {
	void *handle = dlopen(lib, RTLD_NOW);
	if (handle == NULL) {
		probe_dlerror(msg, len);
		return -1;
	}
	if (dlsym(handle, sym) == NULL) {
		probe_dlerror(msg, len);
		dlclose(handle);
		return -2;
	}
	if (dlclose(handle) != 0) {
		probe_dlerror(msg, len);
		return -3;
	}
	return 0;
}
