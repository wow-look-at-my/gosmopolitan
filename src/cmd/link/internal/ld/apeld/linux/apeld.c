// apeld: boot an APE from a memfd on Linux. Freestanding, raw syscalls only.
//
// The APE's program headers hold absolute file offsets, so the whole file
// is copied into a memfd and only the first 64 bytes change: the payload's
// own ELF header, with e_phoff rebased to the payload's file offset. The
// kernel then execs the memfd directly. Nothing touches the disk.
//
// The host arch selects the payload. This binary is compiled per arch, so
// the selector is a compile-time constant.

typedef unsigned long u64;
typedef unsigned int u32;
typedef unsigned short u16;
typedef long i64;

#if defined(__x86_64__)
#define EM_HOST 62
static inline i64 sc6(i64 n, i64 a, i64 b, i64 c, i64 d, i64 e, i64 f) {
	i64 r;
	register i64 r10 __asm__("r10") = d;
	register i64 r8 __asm__("r8") = e;
	register i64 r9 __asm__("r9") = f;
	__asm__ volatile("syscall"
	                 : "=a"(r)
	                 : "a"(n), "D"(a), "S"(b), "d"(c), "r"(r10), "r"(r8), "r"(r9)
	                 : "rcx", "r11", "memory");
	return r;
}
enum { SYS_write = 1, SYS_pread = 17, SYS_pwrite = 18, SYS_sendfile = 40,
	SYS_exit = 60, SYS_openat = 257, SYS_memfd_create = 319, SYS_execveat = 322,
	SYS_unlinkat = 263 };
#elif defined(__aarch64__)
#define EM_HOST 183
static inline i64 sc6(i64 n, i64 a, i64 b, i64 c, i64 d, i64 e, i64 f) {
	register i64 x8 __asm__("x8") = n;
	register i64 x0 __asm__("x0") = a;
	register i64 x1 __asm__("x1") = b;
	register i64 x2 __asm__("x2") = c;
	register i64 x3 __asm__("x3") = d;
	register i64 x4 __asm__("x4") = e;
	register i64 x5 __asm__("x5") = f;
	__asm__ volatile("svc #0"
	                 : "+r"(x0)
	                 : "r"(x8), "r"(x1), "r"(x2), "r"(x3), "r"(x4), "r"(x5)
	                 : "memory");
	return x0;
}
enum { SYS_write = 64, SYS_pread = 67, SYS_pwrite = 68, SYS_sendfile = 71,
	SYS_exit = 93, SYS_openat = 56, SYS_memfd_create = 279, SYS_execveat = 281,
	SYS_unlinkat = 35 };
#else
#error "unsupported arch"
#endif

#define sc1(n, a) sc6(n, (i64)(a), 0, 0, 0, 0, 0)
#define sc3(n, a, b, c) sc6(n, (i64)(a), (i64)(b), (i64)(c), 0, 0, 0)
#define sc4(n, a, b, c, d) sc6(n, (i64)(a), (i64)(b), (i64)(c), (i64)(d), 0, 0)
#define sc5(n, a, b, c, d, e) sc6(n, (i64)(a), (i64)(b), (i64)(c), (i64)(d), (i64)(e), 0)

enum {
	AT_FDCWD = -100,
	AT_EMPTY_PATH = 0x1000,
	O_RDONLY = 0,
	O_CLOEXEC = 02000000,
	MFD_CLOEXEC = 1,
	PAYLOAD_ALIGN = 0x10000,
};

// The main body runs in the C ABI; the entry stub below hands it argv.
__attribute__((noreturn)) static void die(const char *msg) {
	// Length by hand: no libc.
	u64 n = 0;
	while (msg[n]) n++;
	sc3(SYS_write, 2, "apeld: ", 7);
	sc3(SYS_write, 2, msg, n);
	sc3(SYS_write, 2, "\n", 1);
	sc1(SYS_exit, 127);
	__builtin_unreachable();
}

__attribute__((noreturn, used)) static void run(i64 argc, char **argv) {
	if (argc < 2) die("usage: apeld [-u] PROG.com [args...]");
	char **envp = argv + argc + 1;

	// -u removes this loader's own file before anything else. A caller that
	// unpacked a throwaway copy passes it, so an APE leaves no second file on
	// the host. It is argv and not an environment variable on purpose: an
	// environment variable reaches the payload, and a nested run could then
	// delete a loader somebody installed.
	if (argc > 2 && argv[1][0] == '-' && argv[1][1] == 'u' && argv[1][2] == 0) {
		sc3(SYS_unlinkat, AT_FDCWD, argv[0], 0);
		argv++;
		argc--;
	}
	const char *path = argv[1];

	i64 fd = sc4(SYS_openat, AT_FDCWD, path, O_RDONLY | O_CLOEXEC, 0);
	if (fd < 0) die("cannot open program");

	// The memfd name is what /proc/self/exe reads back, so use the basename.
	const char *name = path;
	for (const char *p = path; *p; p++)
		if (*p == '/') name = p + 1;
	i64 mfd = sc3(SYS_memfd_create, name, MFD_CLOEXEC, 0);
	if (mfd < 0) die("memfd_create failed");

	// sendfile copies until the source is drained. A zero return is EOF.
	for (;;) {
		i64 n = sc4(SYS_sendfile, mfd, fd, 0, 1 << 30);
		if (n < 0) die("copy failed");
		if (n == 0) break;
	}

	// Find the payload for this arch on a 64K boundary.
	union {
		unsigned char b[64];
		struct {
			u32 magic;
			unsigned char ident[12];
			u16 type, machine;
			u32 version;
			u64 entry, phoff;
		} h;
	} eh;
	u64 off = PAYLOAD_ALIGN;
	for (;; off += PAYLOAD_ALIGN) {
		if (sc4(SYS_pread, mfd, eh.b, 64, off) != 64) die("no payload for this machine");
		if (eh.h.magic == 0x464c457f && eh.h.machine == EM_HOST) break;
	}
	eh.h.phoff += off;
	if (sc4(SYS_pwrite, mfd, eh.b, 64, 0) != 64) die("cannot write boot header");

	sc5(SYS_execveat, mfd, "", argv + 1, envp, AT_EMPTY_PATH);
	die("exec failed");
}

// Entry: the kernel leaves argc at sp and argv right above it.
#if defined(__x86_64__)
__asm__(".text\n.global _start\n_start:\n"
        "\tmov (%rsp), %rdi\n"
        "\tlea 8(%rsp), %rsi\n"
        "\tand $-16, %rsp\n"
        "\tcall run\n");
#else
__asm__(".text\n.global _start\n_start:\n"
        "\tldr x0, [sp]\n"
        "\tadd x1, sp, #8\n"
        "\tb run\n");
#endif
