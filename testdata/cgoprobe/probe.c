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
