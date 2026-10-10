// Copyright The Go Authors. All rights reserved.

//go:build cosmo

// Go's thread state on a cosmo thread. libcosmo owns the C thread pointer:
// %fs (Linux), gs:0x30 (XNU) or a TEB TLS slot (NT) on amd64.

#include <errno.h>
#include <pthread.h>
#include <signal.h>
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

// libcosmo's import address table slots, which the NT loader fills.
extern void *__imp_GetProcAddress;
extern void *__imp_LoadLibraryA;

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

	// On NT gs:0x28 is the TEB's ArbitraryUserPointer, which is already per thread.
	if (__hostos & COSMO_HOST_WINDOWS) {
		return;
	}
	__asm__ volatile("mov %%gs:0x30,%0" : "=r"(tib));
	cosmo_go_tls[1] = tib;
	cosmo_set_gs(&cosmo_go_tls[0]);
}

// cosmo_foreign_enter returns the GS base that Go reads g through. libcosmo's
// foreign dlopen resets the thread pointer, and cosmo_foreign_leave puts the
// base back.
static uintptr_t
cosmo_foreign_enter(void)
{
	uintptr_t base = 0;
	long ret;

	if (__hostos & (COSMO_HOST_WINDOWS | COSMO_HOST_XNU)) {
		return 0;
	}
	// arch_prctl(ARCH_GET_GS, &base)
	__asm__ volatile("syscall" : "=a"(ret) : "0"(158L), "D"(0x1004L), "S"(&base) : "rcx", "r11", "memory", "cc");
	return ret == 0 ? base : 0;
}

static void
cosmo_foreign_leave(uintptr_t base)
{
	if (base != 0) {
		cosmo_set_gs((uintptr_t *)(base + 0x28));
	}
}

// cosmo_host_slots fills the runtime's ntiat. The Go runtime finds every NT
// function through GetProcAddress and LoadLibraryA.
static void
cosmo_host_slots(void **slots)
{
	if (__hostos & COSMO_HOST_WINDOWS) {
		slots[0] = __imp_GetProcAddress;
		slots[1] = __imp_LoadLibraryA;
	}
}

#elif defined(__aarch64__)

// The head of the APE loader's Syslib (ape-m1.c), through dlsym.
struct cosmo_syslib {
	int32_t magic;
	int32_t version;
	void *before_stack_min[22];
	int32_t stack_min;
	int32_t attr_size;
	void *before_dlopen[23];
	void *dlopen;
	void *(*dlsym)(void *, const char *);
};

// cosmo_host is the runtime's cosmoHostSlots: the Syslib.
static uintptr_t *cosmo_host;

// XNU writes TPIDR_EL0 at every context switch. TPIDRRO_EL0 is the thread's
// Apple TSD array, which the runtime reads instead.
static uintptr_t *
cosmo_tsd(void)
{
	uintptr_t tsd;

	__asm__ volatile("mrs %0, tpidrro_el0" : "=r"(tsd));
	return (uintptr_t *)(tsd & ~(uintptr_t)7);
}

// cosmo_tsd_slot allocates an Apple TSD key and returns its offset from
// TPIDRRO_EL0, as runtime.tlsinit does on darwin.
static uintptr_t
cosmo_tsd_slot(void)
{
	struct cosmo_syslib *lib = (struct cosmo_syslib *)__syslib;
	void *rtld_default = (void *)-2;
	int (*key_create)(unsigned long *, void (*)(void *));
	int (*set_specific)(unsigned long, const void *);
	unsigned long key;
	uintptr_t *tsd;
	const uintptr_t magic = 0xc476c475c47957;
	int idx;

	if (lib == NULL || lib->version < 6 || lib->dlsym == NULL) {
		fprintf(stderr, "runtime/cgo: the APE loader's Syslib has no dlsym\n");
		abort();
	}
	key_create = lib->dlsym(rtld_default, "pthread_key_create");
	set_specific = lib->dlsym(rtld_default, "pthread_setspecific");
	if (key_create == NULL || set_specific == NULL || key_create(&key, NULL) != 0) {
		fprintf(stderr, "runtime/cgo: cannot create an Apple pthread key\n");
		abort();
	}
	set_specific(key, (const void *)magic);
	tsd = cosmo_tsd();
	for (idx = 0; idx < 768; idx++) {
		if (tsd[idx] == magic) {
			set_specific(key, NULL);
			return idx * sizeof(uintptr_t);
		}
	}
	fprintf(stderr, "runtime/cgo: the Apple pthread key is not in the thread's TSD\n");
	abort();
}

static void
cosmo_bind_go_tls(void)
{
	register uintptr_t tib __asm__("x28");

	if (__hostos & COSMO_HOST_XNU) {
		*(uintptr_t *)((char *)cosmo_tsd() + cosmo_host[2]) = tib;
		return;
	}
	__asm__ volatile("msr tpidr_el0, %0" : : "r"(tib));
}

// The helper that libcosmo's foreign dlopen loads moves TPIDR_EL0 to glibc's
// block.
static uintptr_t
cosmo_foreign_enter(void)
{
	return 0;
}

static void
cosmo_foreign_leave(uintptr_t base)
{
	(void)base;
	if (!(__hostos & (COSMO_HOST_WINDOWS | COSMO_HOST_XNU))) {
		cosmo_bind_go_tls();
	}
}

// cosmo_host_slots fills the runtime's cosmoHostSlots. macOS calls go
// through the Syslib.
static void
cosmo_host_slots(void **slots)
{
	cosmo_host = (uintptr_t *)slots;
	cosmo_host[0] = (uintptr_t)__syslib;
	if (__hostos & COSMO_HOST_XNU) {
		cosmo_host[1] = cosmo_tsd_slot();
		cosmo_host[2] = cosmo_tsd_slot();
	}
}

#else
#error "unsupported cosmo architecture"
#endif

// cosmo_inittls is x_cgo_inittls. rt0_go passes the Go runtime's __hostos and
// its host slots, because the APE boot handed the host to libcosmo, not to Go.
static void
cosmo_inittls(void **hostos, void **slots)
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
	cosmo_host_slots(slots);
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

// libcosmo's dlopen, dlsym, dlclose and dlerror are stubs that always fail.
void *cosmo_dlopen(const char *, int);
void *cosmo_dlsym(void *, const char *);
int cosmo_dlclose(void *);
char *cosmo_dlerror(void);

extern char *__program_executable_name;
extern char **__argv;
char *GetProgramExecutableName(void);
char *realpath(const char *, char *);
char *getenv(const char *);
int access(const char *, int);

static pthread_once_t cosmo_progname_once = PTHREAD_ONCE_INIT;
static char cosmo_progname[4096];

// cosmo_path_lookup finds a bare command name on PATH, as the shell did.
static int
cosmo_path_lookup(const char *cmd, char *out, size_t size)
{
	char cand[4096];
	const char *dir = getenv("PATH");
	while (dir != NULL && *dir != 0) {
		const char *end = strchr(dir, ':');
		size_t len = end != NULL ? (size_t)(end - dir) : strlen(dir);
		size_t need = len + 1 + strlen(cmd) + 1;
		if (len > 0 && need <= sizeof cand) {
			memcpy(cand, dir, len);
			cand[len] = '/';
			strcpy(cand + len + 1, cmd);
			if (access(cand, 1) == 0 && realpath(cand, out) != NULL)
				return 1;
		}
		dir = end != NULL ? end + 1 : NULL;
	}
	return 0;
}

// cosmo_find_progname gives libcosmo the program's real path. An APE loader
// execs a memfd, so /proc/self/exe names "/memfd:<name>". cosmo_dlopen stats
// the program path, and a path that opens nothing stops it.
static void
cosmo_find_progname(void)
{
	const char *name = GetProgramExecutableName();
	if (name != NULL && strncmp(name, "/memfd:", 7) != 0)
		return;
	if (__argv == NULL || __argv[0] == NULL)
		return;
	if (strchr(__argv[0], '/') != NULL) {
		if (realpath(__argv[0], cosmo_progname) != NULL)
			__program_executable_name = cosmo_progname;
		return;
	}
	if (cosmo_path_lookup(__argv[0], cosmo_progname, sizeof cosmo_progname))
		__program_executable_name = cosmo_progname;
}

// cosmo_foreign holds the state of one call into host code.
struct cosmo_foreign {
	sigset_t mask;
	uintptr_t base;
};

// cosmo_foreign_begin blocks every signal on this thread, then gives the
// thread the host's TLS. A Go signal handler that ran on the host's TLS would
// crash.
static void
cosmo_foreign_begin(struct cosmo_foreign *call)
{
	sigset_t all;

	sigfillset(&all);
	pthread_sigmask(SIG_BLOCK, &all, &call->mask);
	call->base = cosmo_foreign_enter();
}

static void
cosmo_foreign_end(struct cosmo_foreign *call)
{
	cosmo_foreign_leave(call->base);
	pthread_sigmask(SIG_SETMASK, &call->mask, NULL);
}

void *
__wrap_dlopen(const char *path, int mode)
{
	struct cosmo_foreign call;
	void *handle;

	cosmo_foreign_begin(&call);
	pthread_once(&cosmo_progname_once, cosmo_find_progname);
	handle = cosmo_dlopen(path, mode);
	cosmo_foreign_end(&call);
	return handle;
}

void *
__wrap_dlsym(void *handle, const char *name)
{
	struct cosmo_foreign call;
	void *sym;

	cosmo_foreign_begin(&call);
	sym = cosmo_dlsym(handle, name);
	cosmo_foreign_end(&call);
	return sym;
}

int
__wrap_dlclose(void *handle)
{
	struct cosmo_foreign call;
	int err;

	cosmo_foreign_begin(&call);
	err = cosmo_dlclose(handle);
	cosmo_foreign_end(&call);
	return err;
}

char *
__wrap_dlerror(void)
{
	struct cosmo_foreign call;
	char *msg;

	cosmo_foreign_begin(&call);
	msg = cosmo_dlerror();
	cosmo_foreign_end(&call);
	return msg;
}
