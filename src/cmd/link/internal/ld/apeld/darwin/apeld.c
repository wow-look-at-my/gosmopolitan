// apeld: boot an APE's arm64 payload on macOS from a precompiled loader.
//
// macOS has no memfd and cannot exec an ELF, so the loader maps the
// payload's PT_LOAD segments itself, builds a SysV stack with an auxv,
// hands the payload a Syslib table of libSystem entry points, and jumps.
// This is the job gosmopolitan's embedded ape-m1.c does after the shell
// compiles it with cc. Here it is compiled once, ahead of time.
//
// Contract with the payload (rt0_cosmo_arm64.s): sp = argc block, x2 =
// program path, x3 = 8 (XNU), x15 = Syslib with magic "slib", x16 = entry.
// Wrapped Syslib entries return -errno on failure, as Linux syscalls do.

#include <dispatch/dispatch.h>
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <libkern/OSCacheControl.h>
#include <mach/mach.h>
#include <mach/vm_region.h>
#include <pthread.h>
#include <semaphore.h>
#include <signal.h>
#include <stdint.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/random.h>
#include <sys/resource.h>
#include <sys/select.h>
#include <sys/sysctl.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

// libSystem exports this MIG stub; the SDK header for it is not bundled.
extern kern_return_t mach_vm_region(vm_map_t, mach_vm_address_t *, mach_vm_size_t *,
                                    vm_region_flavor_t, vm_region_info_t,
                                    mach_msg_type_number_t *, mach_port_t *);

#define PAGESZ 16384
#define PAYLOAD_ALIGN 0x10000
#define EM_AARCH64 183
#define PT_LOAD 1
#define PT_DYNAMIC 2
#define PT_INTERP 3
#define PF_X 1
#define PF_W 2
#define PF_R 4
#define MAX_PHDRS 18

enum { AT_NULL = 0, AT_PHDR = 3, AT_PHENT = 4, AT_PHNUM = 5, AT_PAGESZ = 6,
	AT_ENTRY = 9, AT_UID = 11, AT_EUID = 12, AT_GID = 13, AT_EGID = 14,
	AT_SECURE = 23, AT_RANDOM = 25, AT_EXECFN = 31 };
// No AT_HWCAP on purpose: the runtime's fixAuxv then measures the CPU
// through sysctl instead of trusting a word this loader made up.
#define AUXV_WORDS 32

typedef struct {
	unsigned char ident[16];
	uint16_t type, machine;
	uint32_t version;
	uint64_t entry, phoff, shoff;
	uint32_t flags;
	uint16_t ehsize, phentsize, phnum, shentsize, shnum, shstrndx;
} Ehdr;

typedef struct {
	uint32_t type, flags;
	uint64_t offset, vaddr, paddr, filesz, memsz, align;
} Phdr;

struct Syslib {
	int magic;
	int version;
	long (*fork)(void);
	long (*pipe)(int[2]);
	long (*clock_gettime)(int, struct timespec *);
	long (*nanosleep)(const struct timespec *, struct timespec *);
	long (*mmap)(void *, size_t, int, int, int, off_t);
	int (*pthread_jit_write_protect_supported_np)(void);
	void (*pthread_jit_write_protect_np)(int);
	void (*sys_icache_invalidate)(void *, size_t);
	int (*pthread_create)(pthread_t *, const pthread_attr_t *, void *(*)(void *), void *);
	void (*pthread_exit)(void *);
	int (*pthread_kill)(pthread_t, int);
	int (*pthread_sigmask)(int, const sigset_t *, sigset_t *);
	int (*pthread_setname_np)(const char *);
	dispatch_semaphore_t (*dispatch_semaphore_create)(long);
	long (*dispatch_semaphore_signal)(dispatch_semaphore_t);
	long (*dispatch_semaphore_wait)(dispatch_semaphore_t, dispatch_time_t);
	dispatch_time_t (*dispatch_walltime)(const struct timespec *, int64_t);
	pthread_t (*pthread_self)(void);
	void (*dispatch_release)(dispatch_semaphore_t);
	long (*raise)(int);
	int (*pthread_join)(pthread_t, void **);
	void (*pthread_yield_np)(void);
	int pthread_stack_min;
	int sizeof_pthread_attr_t;
	int (*pthread_attr_init)(pthread_attr_t *);
	int (*pthread_attr_destroy)(pthread_attr_t *);
	int (*pthread_attr_setstacksize)(pthread_attr_t *, size_t);
	int (*pthread_attr_setguardsize)(pthread_attr_t *, size_t);
	void (*exit)(int);
	long (*close)(int);
	long (*munmap)(void *, size_t);
	long (*openat)(int, const char *, int, int);
	long (*write)(int, const void *, size_t);
	long (*read)(int, void *, size_t);
	long (*sigaction)(int, const struct sigaction *, struct sigaction *);
	long (*pselect)(int, fd_set *, fd_set *, fd_set *, const struct timespec *, const sigset_t *);
	long (*mprotect)(void *, size_t, int);
	long (*sigaltstack)(const stack_t *, stack_t *);
	long (*getentropy)(void *, size_t);
	long (*sem_open)(const char *, int, uint16_t, unsigned);
	long (*sem_unlink)(const char *);
	long (*sem_close)(int *);
	long (*sem_post)(int *);
	long (*sem_wait)(int *);
	long (*sem_trywait)(int *);
	long (*getrlimit)(int, struct rlimit *);
	long (*setrlimit)(int, const struct rlimit *);
	void *(*dlopen)(const char *, int);
	void *(*dlsym)(void *, const char *);
	int (*dlclose)(void *);
	char *(*dlerror)(void);
	int (*pthread_cpu_number_np)(size_t *);
	long (*sysctl)(int *, u_int, void *, size_t *, void *, size_t);
	long (*sysctlbyname)(const char *, void *, size_t *, void *, size_t);
	long (*sysctlnametomib)(const char *, int *, size_t *);
};

// These outlive main: AT_PHDR and x15 point into them.
static Phdr phdrs[MAX_PHDRS];
static struct Syslib lib;
static char rando[16];

__attribute__((noreturn)) static void die(const char *msg) {
	write(2, "apeld: ", 7);
	write(2, msg, strlen(msg));
	write(2, "\n", 1);
	_exit(127);
}

static long sysret(long rc) { return rc == -1 ? -errno : rc; }

static long w_fork(void) { return sysret(fork()); }
static long w_pipe(int p[2]) { return sysret(pipe(p)); }
static long w_clock_gettime(int c, struct timespec *t) { return sysret(clock_gettime(c, t)); }
static long w_nanosleep(const struct timespec *a, struct timespec *b) { return sysret(nanosleep(a, b)); }
static long w_mmap(void *a, size_t n, int p, int f, int fd, off_t o) {
	void *r = mmap(a, n, p, f, fd, o);
	return r == MAP_FAILED ? -errno : (long)r;
}
static long w_raise(int s) { return sysret(raise(s)); }
static long w_close(int fd) { return sysret(close(fd)); }
static long w_munmap(void *a, size_t n) { return sysret(munmap(a, n)); }
static long w_openat(int d, const char *p, int f, int m) { return sysret(openat(d, p, f, m)); }
static long w_write(int fd, const void *b, size_t n) { return sysret(write(fd, b, n)); }
static long w_read(int fd, void *b, size_t n) { return sysret(read(fd, b, n)); }
static long w_sigaction(int s, const struct sigaction *a, struct sigaction *o) { return sysret(sigaction(s, a, o)); }
static long w_pselect(int n, fd_set *r, fd_set *w, fd_set *e, const struct timespec *t, const sigset_t *m) { return sysret(pselect(n, r, w, e, t, m)); }
static long w_mprotect(void *a, size_t n, int p) { return sysret(mprotect(a, n, p)); }
static long w_sigaltstack(const stack_t *s, stack_t *o) { return sysret(sigaltstack(s, o)); }
static long w_getentropy(void *b, size_t n) { return sysret(getentropy(b, n)); }
static long w_sem_open(const char *n, int f, uint16_t m, unsigned v) {
	sem_t *s = sem_open(n, f, m, v);
	return s == SEM_FAILED ? -errno : (long)s;
}
static long w_sem_unlink(const char *n) { return sysret(sem_unlink(n)); }
static long w_sem_close(int *s) { return sysret(sem_close((sem_t *)s)); }
static long w_sem_post(int *s) { return sysret(sem_post((sem_t *)s)); }
static long w_sem_wait(int *s) { return sysret(sem_wait((sem_t *)s)); }
static long w_sem_trywait(int *s) { return sysret(sem_trywait((sem_t *)s)); }
static long w_getrlimit(int r, struct rlimit *l) { return sysret(getrlimit(r, l)); }
static long w_setrlimit(int r, const struct rlimit *l) { return sysret(setrlimit(r, l)); }
static long w_sysctl(int *m, u_int n, void *o, size_t *ol, void *nw, size_t nl) { return sysret(sysctl(m, n, o, ol, nw, nl)); }
static long w_sysctlbyname(const char *n, void *o, size_t *ol, void *nw, size_t nl) { return sysret(sysctlbyname(n, o, ol, nw, nl)); }
static long w_sysctlnametomib(const char *n, int *m, size_t *l) { return sysret(sysctlnametomib(n, m, l)); }

static void fill_syslib(void) {
	lib.magic = 's' | 'l' << 8 | 'i' << 16 | 'b' << 24;
	lib.version = 10;
	lib.fork = w_fork;
	lib.pipe = w_pipe;
	lib.clock_gettime = w_clock_gettime;
	lib.nanosleep = w_nanosleep;
	lib.mmap = w_mmap;
	lib.pthread_jit_write_protect_supported_np = pthread_jit_write_protect_supported_np;
	lib.pthread_jit_write_protect_np = pthread_jit_write_protect_np;
	lib.sys_icache_invalidate = sys_icache_invalidate;
	lib.pthread_create = pthread_create;
	lib.pthread_exit = pthread_exit;
	lib.pthread_kill = pthread_kill;
	lib.pthread_sigmask = pthread_sigmask;
	lib.pthread_setname_np = pthread_setname_np;
	lib.dispatch_semaphore_create = dispatch_semaphore_create;
	lib.dispatch_semaphore_signal = dispatch_semaphore_signal;
	lib.dispatch_semaphore_wait = dispatch_semaphore_wait;
	lib.dispatch_walltime = dispatch_walltime;
	lib.pthread_self = pthread_self;
	lib.dispatch_release = (void (*)(dispatch_semaphore_t))dispatch_release;
	lib.raise = w_raise;
	lib.pthread_join = pthread_join;
	lib.pthread_yield_np = pthread_yield_np;
	lib.pthread_stack_min = PTHREAD_STACK_MIN;
	lib.sizeof_pthread_attr_t = sizeof(pthread_attr_t);
	lib.pthread_attr_init = pthread_attr_init;
	lib.pthread_attr_destroy = pthread_attr_destroy;
	lib.pthread_attr_setstacksize = pthread_attr_setstacksize;
	lib.pthread_attr_setguardsize = pthread_attr_setguardsize;
	lib.exit = exit;
	lib.close = w_close;
	lib.munmap = w_munmap;
	lib.openat = w_openat;
	lib.write = w_write;
	lib.read = w_read;
	lib.sigaction = w_sigaction;
	lib.pselect = w_pselect;
	lib.mprotect = w_mprotect;
	lib.sigaltstack = w_sigaltstack;
	lib.getentropy = w_getentropy;
	lib.sem_open = w_sem_open;
	lib.sem_unlink = w_sem_unlink;
	lib.sem_close = w_sem_close;
	lib.sem_post = w_sem_post;
	lib.sem_wait = w_sem_wait;
	lib.sem_trywait = w_sem_trywait;
	lib.getrlimit = w_getrlimit;
	lib.setrlimit = w_setrlimit;
	lib.dlopen = dlopen;
	lib.dlsym = dlsym;
	lib.dlclose = dlclose;
	lib.dlerror = dlerror;
	lib.pthread_cpu_number_np = pthread_cpu_number_np;
	lib.sysctl = w_sysctl;
	lib.sysctlbyname = w_sysctlbyname;
	lib.sysctlnametomib = w_sysctlnametomib;
}

// Refuse a load range that already holds live memory: MAP_FIXED would
// replace it in silence, and a payload at 4 TiB can land on a malloc arena.
static void check_range_free(uint64_t lo, uint64_t hi) {
	for (mach_vm_address_t a = lo & -PAGESZ; a < hi;) {
		mach_vm_address_t ra = a;
		mach_vm_size_t rsize = 0;
		vm_region_basic_info_data_64_t ri;
		mach_msg_type_number_t ric = VM_REGION_BASIC_INFO_COUNT_64;
		mach_port_t obj = MACH_PORT_NULL;
		if (mach_vm_region(mach_task_self(), &ra, &rsize, VM_REGION_BASIC_INFO_64,
		                   (vm_region_info_t)&ri, &ric, &obj) != KERN_SUCCESS)
			return;
		if (ra >= hi) return;
		if (ri.protection != VM_PROT_NONE) die("live memory covers the image's load range");
		a = ra + rsize;
	}
}

static void map_segment(int fd, const Phdr *p) {
	int prot = 0;
	if (p->flags & PF_R) prot |= PROT_READ;
	if (p->flags & PF_W) prot |= PROT_WRITE;
	if (p->flags & PF_X) prot |= PROT_EXEC;
	uint64_t base = p->vaddr & -PAGESZ;
	uint64_t fend = p->vaddr + p->filesz;
	uint64_t bss0 = (fend + PAGESZ - 1) & -PAGESZ;
	uint64_t mend = p->vaddr + p->memsz;
	if (p->filesz) {
		uint64_t size = (p->vaddr & (PAGESZ - 1)) + p->filesz;
		uint64_t wipe = (bss0 < mend ? bss0 : mend) - fend;
		// An executable file mapping makes XNU hash the whole file under
		// SIP. Reading the bytes into anonymous memory sidesteps that.
		if (prot & PROT_EXEC) {
			if (mmap((void *)base, size, PROT_READ | PROT_WRITE,
			         MAP_PRIVATE | MAP_FIXED | MAP_ANONYMOUS, -1, 0) == MAP_FAILED)
				die("mmap anon for text failed");
			if (pread(fd, (void *)p->vaddr, p->filesz, p->offset) != (ssize_t)p->filesz)
				die("pread text failed");
		} else {
			if (mmap((void *)base, size, PROT_READ | PROT_WRITE,
			         MAP_PRIVATE | MAP_FIXED, fd, p->offset & -PAGESZ) == MAP_FAILED)
				die("mmap segment failed");
		}
		if (wipe) memset((void *)fend, 0, wipe);
		if (mprotect((void *)base, size, prot)) die("mprotect failed");
		if (mend > bss0 &&
		    mmap((void *)bss0, mend - bss0, prot, MAP_PRIVATE | MAP_FIXED | MAP_ANONYMOUS, -1, 0) == MAP_FAILED)
			die("mmap bss failed");
	} else if (mmap((void *)base, (p->vaddr & (PAGESZ - 1)) + p->memsz, prot,
	                MAP_PRIVATE | MAP_FIXED | MAP_ANONYMOUS, -1, 0) == MAP_FAILED) {
		die("mmap bss failed");
	}
}

__attribute__((noreturn)) static void enter(long *sp, const char *path, uint64_t entry) {
	register long *x0 __asm__("x0") = sp;
	register const char *x2 __asm__("x2") = path;
	register long x3 __asm__("x3") = 8;
	register struct Syslib *x15 __asm__("x15") = &lib;
	register uint64_t x16 __asm__("x16") = entry;
	__asm__ volatile(
	    "mov x1, #0\n\tmov x4, #0\n\tmov x5, #0\n\tmov x6, #0\n\tmov x7, #0\n\t"
	    "mov x8, #0\n\tmov x9, #0\n\tmov x10, #0\n\tmov x11, #0\n\tmov x12, #0\n\t"
	    "mov x13, #0\n\tmov x14, #0\n\tmov x17, #0\n\tmov x19, #0\n\tmov x20, #0\n\t"
	    "mov x21, #0\n\tmov x22, #0\n\tmov x23, #0\n\tmov x24, #0\n\tmov x25, #0\n\t"
	    "mov x26, #0\n\tmov x27, #0\n\tmov x28, #0\n\tmov x29, #0\n\tmov x30, #0\n\t"
	    "mov sp, x0\n\tmov x0, #0\n\tbr x16"
	    :
	    : "r"(x0), "r"(x2), "r"(x3), "r"(x15), "r"(x16)
	    : "memory");
	__builtin_unreachable();
}

int main(int argc, char **argv, char **envp) {
	if (argc < 2) die("usage: apeld PROG.com [args...]");
	const char *path = argv[1];
	int fd = open(path, O_RDONLY | O_CLOEXEC);
	if (fd < 0) die("cannot open program");

	// Find the arm64 payload on a 64K boundary. Its program headers already
	// hold absolute file offsets; only e_phoff is payload-relative.
	Ehdr eh;
	uint64_t off = PAYLOAD_ALIGN;
	for (;; off += PAYLOAD_ALIGN) {
		if (pread(fd, &eh, sizeof eh, off) != sizeof eh) die("no arm64 payload");
		if (memcmp(eh.ident, "\177ELF", 4) == 0 && eh.machine == EM_AARCH64) break;
	}
	if (eh.type != 2) die("payload is not ET_EXEC");
	if (eh.phentsize != sizeof(Phdr) || eh.phnum > MAX_PHDRS) die("bad program header table");
	if (pread(fd, phdrs, eh.phnum * sizeof(Phdr), eh.phoff + off) != (ssize_t)(eh.phnum * sizeof(Phdr)))
		die("cannot read program headers");

	uint64_t lo = ~0UL, hi = 0;
	int entry_ok = 0;
	for (int i = 0; i < eh.phnum; i++) {
		Phdr *p = &phdrs[i];
		if (p->type == PT_INTERP || p->type == PT_DYNAMIC) die("payload is dynamic");
		if (p->type != PT_LOAD || !p->memsz) continue;
		if (p->filesz > p->memsz) die("filesz exceeds memsz");
		if ((p->flags & (PF_W | PF_X)) == (PF_W | PF_X)) die("RWX segment");
		if ((p->vaddr & (PAGESZ - 1)) != (p->offset & (PAGESZ - 1))) die("segment misaligned for 16K pages");
		if (p->vaddr < lo) lo = p->vaddr;
		if (p->vaddr + p->memsz > hi) hi = p->vaddr + p->memsz;
		if ((p->flags & PF_X) && eh.entry >= p->vaddr && eh.entry < p->vaddr + p->memsz) entry_ok = 1;
	}
	if (!entry_ok) die("entry point outside executable segment");
	check_range_free(lo, (hi + PAGESZ - 1) & -PAGESZ);
	for (int i = 0; i < eh.phnum; i++)
		if (phdrs[i].type == PT_LOAD && phdrs[i].memsz) map_segment(fd, &phdrs[i]);
	close(fd);

	fill_syslib();
	if (getentropy(rando, sizeof rando)) die("getentropy failed");

	// New stack block: argc, argv[1..], NULL, envp, NULL, auxv, in a frame
	// that never returns. argv[0] of the loader is dropped.
	int envc = 0;
	while (envp[envc]) envc++;
	long words = 1 + (argc - 1) + 1 + envc + 1 + AUXV_WORDS;
	long *sp = __builtin_alloca(words * 8 + 16);
	sp = (long *)((uintptr_t)sp & -16);
	long *w = sp;
	*w++ = argc - 1;
	for (int i = 1; i < argc; i++) *w++ = (long)argv[i];
	*w++ = 0;
	for (int i = 0; i < envc; i++) *w++ = (long)envp[i];
	*w++ = 0;
	*w++ = AT_PHDR;   *w++ = (long)phdrs;
	*w++ = AT_PHENT;  *w++ = sizeof(Phdr);
	*w++ = AT_PHNUM;  *w++ = eh.phnum;
	*w++ = AT_ENTRY;  *w++ = (long)eh.entry;
	*w++ = AT_PAGESZ; *w++ = PAGESZ;
	*w++ = AT_UID;    *w++ = getuid();
	*w++ = AT_EUID;   *w++ = geteuid();
	*w++ = AT_GID;    *w++ = getgid();
	*w++ = AT_EGID;   *w++ = getegid();
	*w++ = AT_SECURE; *w++ = issetugid();
	*w++ = AT_RANDOM; *w++ = (long)rando;
	*w++ = AT_EXECFN; *w++ = (long)path;
	*w++ = AT_NULL;   *w++ = 0;
	enter(sp, path, eh.entry);
}
