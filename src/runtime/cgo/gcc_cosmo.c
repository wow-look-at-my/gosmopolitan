// Copyright The Go Authors. All rights reserved.

//go:build cosmo

// Go's thread state on a cosmo thread. libcosmo owns the C thread pointer:
// %fs (Linux) or gs:0x30 (XNU) on amd64, and x28 on arm64.

#include <errno.h>
#include <pthread.h>
#include <string.h>
#include "libcgo.h"

extern const int __hostos;
struct Syslib;
extern struct Syslib *__syslib;

// libcosmo's __hostos bits (libc/dce.h).
#define COSMO_HOST_LINUX 1
#define COSMO_HOST_WINDOWS 4
#define COSMO_HOST_XNU 8

// The Go runtime's __hostos values (os_cosmo_amd64.go, os_cosmo_arm64.go).
#define GO_HOST_LINUX 0
#define GO_HOST_WINDOWS 2
#define GO_HOST_XNU 8

#if defined(__x86_64__)

static __thread uintptr_t cosmo_go_tls[2];

// cosmo_set_gs points the GS base 0x28 below slot. runtime.settls does the
// same for a thread that Go starts itself.
static void
cosmo_set_gs(uintptr_t *slot)
{
	uintptr_t base = (uintptr_t)slot - 0x28;
	long ret;

	if (__hostos & COSMO_HOST_XNU) {
		// thread_fast_set_cthread_self
		__asm__ volatile("syscall" : "=a"(ret) : "0"(0x3000003L), "D"(base) : "rcx", "rdx", "r11", "memory", "cc");
		return;
	}
	// arch_prctl(ARCH_SET_GS, base)
	__asm__ volatile("syscall" : "=a"(ret) : "0"(158L), "D"(0x1001L), "S"(base) : "rcx", "r11", "memory", "cc");
	if (ret != 0) {
		fprintf(stderr, "runtime/cgo: arch_prctl(ARCH_SET_GS) failed: %ld\n", ret);
		abort();
	}
}

static void
cosmo_bind_go_tls(void)
{
	uintptr_t tib;

	__asm__ volatile("mov %%gs:0x30,%0" : "=r"(tib));
	cosmo_go_tls[1] = tib;
	cosmo_set_gs(&cosmo_go_tls[0]);
}

#elif defined(__aarch64__)

static void
cosmo_bind_go_tls(void)
{
	register uintptr_t tib __asm__("x28");

	__asm__ volatile("msr tpidr_el0, %0" : : "r"(tib));
}

#else
#error "unsupported cosmo architecture"
#endif

// cosmo_inittls is x_cgo_inittls. rt0_go passes the Go runtime's __hostos and
// __syslib, because the APE boot handed them to libcosmo's _start, not to Go.
static void
cosmo_inittls(void **hostos, void **syslib)
{
	int32_t *goHostos = (int32_t *)hostos;

	if (__hostos & COSMO_HOST_XNU) {
		*goHostos = GO_HOST_XNU;
	} else if (__hostos & COSMO_HOST_WINDOWS) {
		*goHostos = GO_HOST_WINDOWS;
	} else if ((__hostos & COSMO_HOST_LINUX) || __hostos == 0) {
		*goHostos = GO_HOST_LINUX;
	} else {
		fprintf(stderr, "runtime/cgo: libcosmo reports host %#x, which the Go runtime does not support\n", __hostos);
		abort();
	}
	if (syslib != NULL) {
		*syslib = __syslib;
	}
	cosmo_bind_go_tls();
}

void (*x_cgo_inittls)(void **tlsg, void **tlsbase) = cosmo_inittls;

struct cosmo_start {
	void *(*fn)(void *);
	void *arg;
};

int __real_pthread_create(pthread_t *, const pthread_attr_t *, void *(*)(void *), void *);

static void *
cosmo_thread_start(void *ptr)
{
	struct cosmo_start start;

	memcpy(&start, ptr, sizeof start);
	free(ptr);
	cosmo_bind_go_tls();
	return start.fn(start.arg);
}

// __wrap_pthread_create replaces pthread_create in a cosmo link (-Wl,--wrap). Every new thread binds Go's slot first.
int
__wrap_pthread_create(pthread_t *thread, const pthread_attr_t *attr, void *(*fn)(void *), void *arg)
{
	struct cosmo_start *start;
	int err;

	start = malloc(sizeof *start);
	if (start == NULL) {
		return EAGAIN;
	}
	start->fn = fn;
	start->arg = arg;
	err = __real_pthread_create(thread, attr, cosmo_thread_start, start);
	if (err != 0) {
		free(start);
	}
	return err;
}
