// apeld: boot an APE from a memfd on Linux. Freestanding, raw syscalls only.
//
// The APE's program headers hold absolute file offsets. Only the span the
// host payload's program headers reference, from the first PT_LOAD page
// through the last one, is copied into a memfd. The payload's e_phoff, its
// program header table, and every p_offset inside the span are rebased by
// the span's start. Data appended past the payload is not boot-reachable:
// the payload reads it through the original file path, not the memfd. The
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
	SYS_ftruncate = 77, SYS_exit = 60, SYS_openat = 257,
	SYS_memfd_create = 319, SYS_execveat = 322, SYS_unlinkat = 263 };
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
	SYS_ftruncate = 46, SYS_exit = 93, SYS_openat = 56,
	SYS_memfd_create = 279, SYS_execveat = 281, SYS_unlinkat = 35 };
#else
#error "unsupported arch"
#endif

#define sc1(n, a) sc6(n, (i64)(a), 0, 0, 0, 0, 0)
#define sc2(n, a, b) sc6(n, (i64)(a), (i64)(b), 0, 0, 0, 0)
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
	PAGE_MASK = 0xfff,
	PT_LOAD = 1,
	MAX_PHDRS = 16,
	PHDR_SIZE = 56,
};

// ELF is little-endian here and both hosts are too; explicit loads keep the
// reader honest about alignment and byte order.
static u64 le64(const unsigned char *p) {
	return (u64)p[0] | (u64)p[1] << 8 | (u64)p[2] << 16 | (u64)p[3] << 24 |
	       (u64)p[4] << 32 | (u64)p[5] << 40 | (u64)p[6] << 48 | (u64)p[7] << 56;
}

static u32 le32(const unsigned char *p) {
	return (u32)((u64)p[0] | (u64)p[1] << 8 | (u64)p[2] << 16 | (u64)p[3] << 24);
}

static void put64(unsigned char *p, u64 v) {
	for (u64 i = 0; i < 8; i++) {
		p[i] = (unsigned char)v;
		v >>= 8;
	}
}

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
	// the host. It is argv and not an environment variable on purpose. An
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

	// Find the payload for this arch on a 64K boundary, in the source file.
	union {
		unsigned char b[64];
		struct {
			u32 magic;
			unsigned char ident[12];
			u16 type, machine;
			u32 version;
			u64 entry, phoff, shoff;
			u32 flags;
			u16 ehsize, phentsize, phnum;
		} h;
	} eh;
	u64 off = PAYLOAD_ALIGN;
	for (;; off += PAYLOAD_ALIGN) {
		if (sc4(SYS_pread, fd, eh.b, 64, off) != 64) die("no payload for this machine");
		if (eh.h.magic == 0x464c457f && eh.h.machine == EM_HOST) break;
	}

	// The boot image is the smallest page-aligned span holding the payload's
	// program header table and every PT_LOAD file range. The payload's
	// program headers must be phentsize-wide fixed-size records; anything
	// else is not an ELF this loader knows how to rebase.
	if (eh.h.phentsize != PHDR_SIZE) die("bad program header size");
	if (eh.h.phnum == 0 || eh.h.phnum > MAX_PHDRS) die("bad program header count");
	u64 phabs = off + eh.h.phoff;
	u64 phsz = (u64)eh.h.phnum * PHDR_SIZE;
	unsigned char ph[MAX_PHDRS * PHDR_SIZE];
	if (sc4(SYS_pread, fd, ph, phsz, phabs) != (i64)phsz) die("cannot read program headers");
	u64 lo = phabs, hi = phabs + phsz;
	for (u64 i = 0; i < eh.h.phnum; i++) {
		const unsigned char *e = ph + i * PHDR_SIZE;
		if (le32(e) != PT_LOAD) continue;
		u64 po = le64(e + 8), pf = le64(e + 32);
		if (po + pf < po) die("bad program headers");
		if (po < lo) lo = po;
		if (po + pf > hi) hi = po + pf;
	}
	lo &= ~(u64)PAGE_MASK;
	hi = (hi + PAGE_MASK) & ~(u64)PAGE_MASK;

	// The memfd name is what /proc/self/exe reads back, so use the basename.
	const char *name = path;
	for (const char *p = path; *p; p++)
		if (*p == '/') name = p + 1;
	i64 mfd = sc3(SYS_memfd_create, name, MFD_CLOEXEC, 0);
	if (mfd < 0) die("memfd_create failed");

	// Copy [lo, hi) and nothing else. A non-NULL sendfile offset pins the
	// source position and advances it. Hitting source EOF short of hi is a
	// corrupt APE, not a reason to copy more.
	i64 pos = (i64)lo;
	u64 left = hi - lo;
	while (left > 0) {
		i64 want = left > (1 << 30) ? (1 << 30) : (i64)left;
		i64 n = sc4(SYS_sendfile, mfd, fd, (i64)&pos, want);
		if (n < 0) die("copy failed");
		if (n == 0) die("short copy");
		left -= (u64)n;
	}
	if (sc2(SYS_ftruncate, mfd, (i64)(hi - lo)) < 0) die("ftruncate failed");

	// Rebase into span coordinates: e_phoff and every p_offset that pointed
	// into [lo, hi). Offsets outside the span (a zero-offset PT_GNU_STACK,
	// for example) keep their value; no mapping resolves them.
	eh.h.phoff = phabs - lo;
	if (sc4(SYS_pwrite, mfd, eh.b, 64, 0) != 64) die("cannot write boot header");
	for (u64 i = 0; i < eh.h.phnum; i++) {
		unsigned char *e = ph + i * PHDR_SIZE;
		u64 po = le64(e + 8);
		if (po >= lo && po < hi) put64(e + 8, po - lo);
	}
	if (sc4(SYS_pwrite, mfd, ph, phsz, phabs - lo) != (i64)phsz) die("cannot write program headers");

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
