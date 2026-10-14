<<<<<<< HEAD
<<<<<<< HEAD
// apeld: boot an APE on Linux by mapping its payload from the file.
// Freestanding, raw syscalls only.
//
// The APE's program headers hold absolute file offsets. Each PT_LOAD is
// mapped MAP_PRIVATE straight from the APE at its own offset. Its pages
// come from the page cache and every process running the same APE shares
// them. Nothing is copied and nothing is written. The loader then builds
// the payload's stack below its own, from the kernel's argv, envp and auxv,
// and jumps to the entry point.
//
// /proc/self/exe names this loader, not the APE. AT_EXECFN names the APE,
// and that is how the payload finds its own file. The entry also gets the
// path in rdx (amd64) or x2 (arm64), the register the APE boot protocol
// assigns it.
=======
// apeld: boot an APE on Linux. Freestanding, raw syscalls only.
//
=======
// apeld: boot an APE on Linux. Freestanding, raw syscalls only.
//
>>>>>>> origin/master
// The APE's program headers hold absolute file offsets, so each PT_LOAD
// segment maps straight from the APE file. Nothing is copied and nothing
// touches the disk. The loader then rebuilds the start stack the kernel
// would give the payload and jumps to its entry. AT_EXECFD names the APE,
// because /proc/self/exe names this loader.
<<<<<<< HEAD
>>>>>>> origin/master
=======
>>>>>>> origin/master
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
<<<<<<< HEAD
<<<<<<< HEAD
enum { SYS_write = 1, SYS_close = 3, SYS_mmap = 9, SYS_mprotect = 10,
	SYS_pread = 17, SYS_exit = 60, SYS_prctl = 157, SYS_openat = 257,
	SYS_unlinkat = 263 };
=======
=======
>>>>>>> origin/master
enum { SYS_write = 1, SYS_lseek = 8, SYS_pread = 17, SYS_pwrite = 18, SYS_mmap = 9,
	SYS_sendfile = 40, SYS_exit = 60, SYS_openat = 257, SYS_unlinkat = 263,
	SYS_prctl = 157, SYS_memfd_create = 319, SYS_execveat = 322,
	SYS_close = 3, SYS_fcntl = 72 };
<<<<<<< HEAD
>>>>>>> origin/master
=======
>>>>>>> origin/master
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
<<<<<<< HEAD
<<<<<<< HEAD
enum { SYS_write = 64, SYS_close = 57, SYS_mmap = 222, SYS_mprotect = 226,
	SYS_pread = 67, SYS_exit = 93, SYS_prctl = 167, SYS_openat = 56,
	SYS_unlinkat = 35 };
=======
=======
>>>>>>> origin/master
enum { SYS_write = 64, SYS_lseek = 62, SYS_pread = 67, SYS_pwrite = 68, SYS_mmap = 222,
	SYS_sendfile = 71, SYS_exit = 93, SYS_openat = 56, SYS_unlinkat = 35,
	SYS_prctl = 167, SYS_memfd_create = 279, SYS_execveat = 281,
	SYS_close = 57, SYS_fcntl = 25 };
<<<<<<< HEAD
>>>>>>> origin/master
=======
>>>>>>> origin/master
#else
#error "unsupported arch"
#endif

#define sc1(n, a) sc6(n, (i64)(a), 0, 0, 0, 0, 0)
#define sc2(n, a, b) sc6(n, (i64)(a), (i64)(b), 0, 0, 0, 0)
#define sc3(n, a, b, c) sc6(n, (i64)(a), (i64)(b), (i64)(c), 0, 0, 0)
#define sc4(n, a, b, c, d) sc6(n, (i64)(a), (i64)(b), (i64)(c), (i64)(d), 0, 0)
#define sc5(n, a, b, c, d, e) sc6(n, (i64)(a), (i64)(b), (i64)(c), (i64)(d), (i64)(e), 0)

#define sc2(n, a, b) sc6(n, (i64)(a), (i64)(b), 0, 0, 0, 0)

enum {
	AT_FDCWD = -100,
	O_RDONLY = 0,
	O_CLOEXEC = 02000000,
<<<<<<< HEAD
<<<<<<< HEAD
	PROT_READ = 1,
	PROT_WRITE = 2,
	PROT_EXEC = 4,
	MAP_PRIVATE = 0x02,
	MAP_ANONYMOUS = 0x20,
	MAP_FIXED_NOREPLACE = 0x100000,
	PR_SET_NAME = 15,
	PAYLOAD_ALIGN = 0x10000,
	ET_EXEC = 2,
	PT_LOAD = 1,
	PT_DYNAMIC = 2,
	PT_INTERP = 3,
	PF_X = 1,
	PF_W = 2,
	PF_R = 4,
	MAX_PHDRS = 16,
	PHDR_SIZE = 56,
	AT_NULL = 0,
	AT_PHDR = 3,
	AT_PHENT = 4,
	AT_PHNUM = 5,
	AT_PAGESZ = 6,
	AT_BASE = 7,
	AT_ENTRY = 9,
	AT_EXECFN = 31,
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
=======
	MFD_CLOEXEC = 1,
	F_DUPFD_CLOEXEC = 1030,
	PAYLOAD_ALIGN = 0x10000,
	PROT_READ = 1, PROT_WRITE = 2, PROT_EXEC = 4,
	MAP_PRIVATE = 2, MAP_FIXED = 0x10, MAP_ANONYMOUS = 0x20,
	PT_LOAD = 1, PT_PHDR = 6,
	PF_X = 1, PF_W = 2, PF_R = 4,
	AT_NULL = 0, AT_EXECFD = 2, AT_PHDR = 3, AT_PHENT = 4, AT_PHNUM = 5,
	AT_PAGESZ = 6, AT_ENTRY = 9, AT_EXECFN = 31,
	PR_SET_NAME = 15,
	MAX_PHDRS = 16,
	// The start stack this loader can rebuild, in words. A larger one dies by name.
	MAX_STACK_WORDS = 1 << 16,
};

=======
	MFD_CLOEXEC = 1,
	F_DUPFD_CLOEXEC = 1030,
	PAYLOAD_ALIGN = 0x10000,
	PROT_READ = 1, PROT_WRITE = 2, PROT_EXEC = 4,
	MAP_PRIVATE = 2, MAP_FIXED = 0x10, MAP_ANONYMOUS = 0x20,
	PT_LOAD = 1, PT_PHDR = 6,
	PF_X = 1, PF_W = 2, PF_R = 4,
	AT_NULL = 0, AT_EXECFD = 2, AT_PHDR = 3, AT_PHENT = 4, AT_PHNUM = 5,
	AT_PAGESZ = 6, AT_ENTRY = 9, AT_EXECFN = 31,
	PR_SET_NAME = 15,
	MAX_PHDRS = 16,
	// The start stack this loader can rebuild, in words. A larger one dies by name.
	MAX_STACK_WORDS = 1 << 16,
};

>>>>>>> origin/master
typedef struct {
	u32 type, flags;
	u64 offset, vaddr, paddr, filesz, memsz, align;
} phdr;

static int mapfailed(i64 r) { return r < 0 && r > -4096; }
<<<<<<< HEAD
>>>>>>> origin/master
=======
>>>>>>> origin/master

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

<<<<<<< HEAD
<<<<<<< HEAD
// place maps exactly at addr. MAP_FIXED_NOREPLACE refuses a range that
// holds anything, this loader and its stack included.
static void place(u64 addr, u64 len, i64 prot, i64 flags, i64 fd, u64 off) {
	i64 got = sc6(SYS_mmap, (i64)addr, (i64)len, prot, flags | MAP_PRIVATE | MAP_FIXED_NOREPLACE, fd, (i64)off);
	if (got == -17) die("a load segment overlaps memory already mapped");
	if (got < 0 || (u64)got != addr) die("cannot map a load segment at its address");
}

// map_load maps one PT_LOAD. The file part maps from the APE itself. The
// rest of the last file page, when the segment's bss starts inside it, is
// zeroed in this process's private copy of that one page. Whole bss pages
// are anonymous.
static void map_load(i64 fd, const unsigned char *phdr, u64 pg) {
	u32 flags = le32(phdr + 4);
	u64 off = le64(phdr + 8), va = le64(phdr + 16), fsz = le64(phdr + 32), msz = le64(phdr + 40);
	i64 prot = (flags & PF_R ? PROT_READ : 0) | (flags & PF_W ? PROT_WRITE : 0) |
	           (flags & PF_X ? PROT_EXEC : 0);
	u64 base = va & -pg;
	u64 fend = va + fsz, mend = va + msz;
	u64 bss0 = (fend + pg - 1) & -pg;
	u64 top = (mend + pg - 1) & -pg;
	u64 anon = base;
	if (fsz) {
		u64 tail = msz > fsz ? (bss0 < mend ? bss0 : mend) - fend : 0;
		place(base, bss0 - base, prot | (tail ? PROT_WRITE : 0), 0, fd, off & -pg);
		// volatile keeps the compiler from turning this into a memset call.
		volatile unsigned char *bss = (volatile unsigned char *)fend;
		for (u64 idx = 0; idx < tail; idx++) bss[idx] = 0;
		if (tail && !(prot & PROT_WRITE) && sc3(SYS_mprotect, base, bss0 - base, prot) < 0)
			die("mprotect failed");
		anon = bss0;
	}
	if (top > anon) place(anon, top - anon, prot, MAP_ANONYMOUS, -1, 0);
}

// enter hands the payload its stack and the program path, with every other
// register zero. A zero CL (amd64) or a non-8 x3 (arm64) tells rt0 the host
// is Linux.
__attribute__((noreturn)) static void enter(u64 *sp, u64 entry, const char *path) {
#if defined(__x86_64__)
	__asm__ volatile(
	    "mov %0, %%rsp\n\t"
	    "xor %%eax, %%eax\n\txor %%ebx, %%ebx\n\txor %%ecx, %%ecx\n\t"
	    "xor %%edi, %%edi\n\txor %%ebp, %%ebp\n\txor %%r8d, %%r8d\n\t"
	    "xor %%r9d, %%r9d\n\txor %%r10d, %%r10d\n\txor %%r11d, %%r11d\n\t"
	    "xor %%r12d, %%r12d\n\txor %%r13d, %%r13d\n\txor %%r14d, %%r14d\n\t"
	    "xor %%r15d, %%r15d\n\tjmp *%%rsi"
	    :
	    : "D"(sp), "S"(entry), "d"(path)
	    : "memory");
#else
	register u64 *x0 __asm__("x0") = sp;
	register const char *x2 __asm__("x2") = path;
	register u64 x16 __asm__("x16") = entry;
	__asm__ volatile(
	    "mov x1, #0\n\tmov x3, #0\n\tmov x4, #0\n\tmov x5, #0\n\tmov x6, #0\n\t"
	    "mov x7, #0\n\tmov x8, #0\n\tmov x9, #0\n\tmov x10, #0\n\tmov x11, #0\n\t"
	    "mov x12, #0\n\tmov x13, #0\n\tmov x14, #0\n\tmov x15, #0\n\tmov x17, #0\n\t"
	    "mov x19, #0\n\tmov x20, #0\n\tmov x21, #0\n\tmov x22, #0\n\tmov x23, #0\n\t"
	    "mov x24, #0\n\tmov x25, #0\n\tmov x26, #0\n\tmov x27, #0\n\tmov x28, #0\n\t"
	    "mov x29, #0\n\tmov x30, #0\n\tmov sp, x0\n\tmov x0, #0\n\tbr x16"
	    :
	    : "r"(x0), "r"(x2), "r"(x16)
	    : "memory");
#endif
	__builtin_unreachable();
=======
=======
>>>>>>> origin/master
// bootcopy copies the payload's PT_LOAD ranges into a memfd and execs that.
// A memfd runs where the APE's own mount forbids an executable mapping, as
// noexec does. Each range keeps its APE offset, so the rest of the memfd is a
// hole that takes no memory. Each range widens to 64K, the largest page a
// kernel maps a segment with.
__attribute__((noreturn)) static void bootcopy(i64 fd, const char *name, u64 off, unsigned char *ehdr, phdr *ph, u32 phnum, char **pargv, char **envp) {
	i64 mfd = sc3(SYS_memfd_create, name, MFD_CLOEXEC, 0);
	if (mfd < 0) die("memfd_create failed");
	for (u32 idx = 0; idx < phnum; idx++) {
		if (ph[idx].type != PT_LOAD) continue;
		i64 pos = ph[idx].offset & -(u64)PAYLOAD_ALIGN;
		u64 end = (ph[idx].offset + ph[idx].filesz + PAYLOAD_ALIGN - 1) & -(u64)PAYLOAD_ALIGN;
		if (sc3(SYS_lseek, mfd, pos, 0) != pos) die("seek failed");
		// sendfile moves pos. A zero return is EOF.
		while ((u64)pos < end) {
			i64 n = sc4(SYS_sendfile, mfd, fd, &pos, end - pos);
			if (n < 0) die("copy failed");
			if (n == 0) break;
		}
	}
	u64 *phoff = (u64 *)(ehdr + 32);
	*phoff += off;
	if (sc4(SYS_pwrite, mfd, ehdr, 64, 0) != 64) die("cannot write boot header");
	sc5(SYS_execveat, mfd, "", pargv, envp, AT_EMPTY_PATH);
	die("exec failed");
<<<<<<< HEAD
>>>>>>> origin/master
=======
>>>>>>> origin/master
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
	char **pargv = argv + 1;
	i64 pargc = argc - 1;

	// The descriptor stays open: AT_EXECFD hands it to the payload.
	i64 fd = sc4(SYS_openat, AT_FDCWD, path, O_RDONLY | O_CLOEXEC, 0);
	if (fd < 0) die("cannot open program");
<<<<<<< HEAD
<<<<<<< HEAD

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
=======
=======
>>>>>>> origin/master
	if (fd < 3) {
		i64 high = sc3(SYS_fcntl, fd, F_DUPFD_CLOEXEC, 3);
		if (high < 0) die("cannot move the program descriptor");
		sc1(SYS_close, fd);
		fd = high;
	}

	// Find the payload for this arch on a 64K boundary.
	struct {
		u32 magic;
		unsigned char ident[12];
		u16 type, machine;
		u32 version;
		u64 entry, phoff, shoff;
		u32 flags;
		u16 ehsize, phentsize, phnum, shentsize, shnum, shstrndx;
<<<<<<< HEAD
>>>>>>> origin/master
=======
>>>>>>> origin/master
	} eh;
	u64 off = PAYLOAD_ALIGN;
	for (;; off += PAYLOAD_ALIGN) {
		if (sc4(SYS_pread, fd, &eh, 64, off) != 64) die("no payload for this machine");
		if (eh.magic == 0x464c457f && eh.machine == EM_HOST) break;
	}
<<<<<<< HEAD
<<<<<<< HEAD

	// The kernel's auxv follows envp. Its page size is the one every
	// mapping below has to agree with.
	char **ep = envp;
	while (*ep) ep++;
	u64 *auxv = (u64 *)(ep + 1);
	u64 pg = 0;
	u64 auxc = 0;
	for (; auxv[auxc * 2] != AT_NULL; auxc++)
		if (auxv[auxc * 2] == AT_PAGESZ) pg = auxv[auxc * 2 + 1];
	if (pg == 0 || (pg & (pg - 1))) die("the kernel passed no page size");

	// The payload must be a static ET_EXEC. Each PT_LOAD must have its file
	// offset and its address equal modulo. The page size, so that it maps
	// from the file as it stands. This loader refuses any other payload. It
	// never copies one.
	if (eh.h.type != ET_EXEC) die("payload is not ET_EXEC");
	if (eh.h.phentsize != PHDR_SIZE) die("bad program header size");
	if (eh.h.phnum == 0 || eh.h.phnum > MAX_PHDRS) die("bad program header count");
	u64 phabs = off + eh.h.phoff;
	u64 phsz = (u64)eh.h.phnum * PHDR_SIZE;
	unsigned char ph[MAX_PHDRS * PHDR_SIZE];
	if (sc4(SYS_pread, fd, ph, phsz, phabs) != (i64)phsz) die("cannot read program headers");
	int entry_ok = 0;
	// AT_PHDR is where the table sits in the payload's memory. A table
	// outside every PT_LOAD is handed over as this frame's copy, which
	// stays live under the payload's stack.
	u64 phmem = (u64)ph;
	for (u64 idx = 0; idx < eh.h.phnum; idx++) {
		const unsigned char *phdr = ph + idx * PHDR_SIZE;
		u32 type = le32(phdr);
		if (type == PT_INTERP || type == PT_DYNAMIC) die("payload is dynamic");
		if (type != PT_LOAD) continue;
		u64 po = le64(phdr + 8), va = le64(phdr + 16), fsz = le64(phdr + 32), msz = le64(phdr + 40);
		if (msz == 0) continue;
		if (fsz > msz || po + fsz < po || va + msz < va) die("bad program headers");
		if ((po ^ va) & (pg - 1)) die("a load segment's offset and address are not page-congruent");
		if ((le32(phdr + 4) & PF_X) && eh.h.entry >= va && eh.h.entry < va + msz) entry_ok = 1;
		if (phabs >= po && phabs + phsz <= po + fsz) phmem = va + (phabs - po);
	}
	if (!entry_ok) die("entry point outside an executable segment");
	for (u64 idx = 0; idx < eh.h.phnum; idx++) {
		const unsigned char *phdr = ph + idx * PHDR_SIZE;
		if (le32(phdr) == PT_LOAD && le64(phdr + 40)) map_load(fd, phdr, pg);
	}
	sc1(SYS_close, fd);

	// comm is the kernel's name for the process in ps and top. It would
	// otherwise read "apeld" for every program this loader starts.
	const char *name = path;
	for (const char *cur = path; *cur; cur++)
		if (*cur == '/') name = cur + 1;
	sc2(SYS_prctl, PR_SET_NAME, name);

	// The payload's stack holds argc, then argv without this loader's own
	// words, then envp, then the kernel's auxv. The auxv entries that
	// describe the image name the payload's instead. Every string stays
	// where the kernel put it.
	u64 nargs = (u64)argc - 1;
	u64 envc = (u64)(ep - envp);
	u64 words = 1 + nargs + 1 + envc + 1 + (auxc + 1) * 2;
	u64 *sp = __builtin_alloca(words * 8 + 16);
	sp = (u64 *)((u64)sp & -16);
	u64 *out = sp;
	*out++ = nargs;
	for (u64 idx = 0; idx < nargs; idx++) *out++ = (u64)argv[idx + 1];
	*out++ = 0;
	for (u64 idx = 0; idx < envc; idx++) *out++ = (u64)envp[idx];
	*out++ = 0;
	for (u64 idx = 0; idx <= auxc; idx++) {
		u64 tag = auxv[idx * 2], val = auxv[idx * 2 + 1];
		switch (tag) {
		case AT_PHDR: val = phmem; break;
		case AT_PHENT: val = PHDR_SIZE; break;
		case AT_PHNUM: val = eh.h.phnum; break;
		case AT_BASE: val = 0; break;
		case AT_ENTRY: val = eh.h.entry; break;
		case AT_EXECFN: val = (u64)path; break;
		}
		*out++ = tag;
		*out++ = val;
	}
	enter(sp, eh.h.entry, path);
=======
	if (eh.phentsize != sizeof(phdr) || eh.phnum == 0 || eh.phnum > MAX_PHDRS) die("bad program headers");
	phdr ph[MAX_PHDRS];
	i64 phlen = (i64)eh.phnum * sizeof(phdr);
	if (sc4(SYS_pread, fd, ph, phlen, off + eh.phoff) != phlen) die("cannot read program headers");

=======
	if (eh.phentsize != sizeof(phdr) || eh.phnum == 0 || eh.phnum > MAX_PHDRS) die("bad program headers");
	phdr ph[MAX_PHDRS];
	i64 phlen = (i64)eh.phnum * sizeof(phdr);
	if (sc4(SYS_pread, fd, ph, phlen, off + eh.phoff) != phlen) die("cannot read program headers");

>>>>>>> origin/master
	// The environment ends in a null, and the auxv follows it.
	char **e = envp;
	while (*e) e++;
	u64 *auxv = (u64 *)(e + 1);
	u64 pagesz = 4096;
	u64 naux = 0;
	for (; auxv[2 * naux] != AT_NULL; naux++)
		if (auxv[2 * naux] == AT_PAGESZ) pagesz = auxv[2 * naux + 1];

	const char *name = path;
	for (const char *c = path; *c; c++)
		if (*c == '/') name = c + 1;

	// Map each PT_LOAD from the file, then zero what lies past its file bytes.
	u64 phdrva = 0;
	// Without PT_PHDR the headers lie in the PT_LOAD that holds their file
	// offset. libc finds PT_TLS through AT_PHDR, so it must name them.
	u64 phfile = off + eh.phoff;
	for (u32 i = 0; i < eh.phnum; i++)
		if (ph[i].type == PT_LOAD && phfile >= ph[i].offset && phfile < ph[i].offset + ph[i].filesz)
			phdrva = ph[i].vaddr + (phfile - ph[i].offset);
	for (u32 i = 0; i < eh.phnum; i++) {
		phdr *p = &ph[i];
		if (p->type == PT_PHDR) phdrva = p->vaddr;
		if (p->type != PT_LOAD || p->memsz == 0) continue;
		i64 prot = (p->flags & PF_R ? PROT_READ : 0) | (p->flags & PF_W ? PROT_WRITE : 0) | (p->flags & PF_X ? PROT_EXEC : 0);
		u64 start = p->vaddr & ~(pagesz - 1);
		u64 lead = p->vaddr - start;
		if (p->offset < lead || (p->offset - lead) % pagesz) die("segment is not page aligned");
		u64 fend = p->vaddr + p->filesz;
		u64 mend = p->vaddr + p->memsz;
		if (p->filesz) {
			if (mapfailed(sc6(SYS_mmap, start, lead + p->filesz, prot, MAP_PRIVATE | MAP_FIXED, fd, p->offset - lead)))
				bootcopy(fd, name, off, (unsigned char *)&eh, ph, eh.phnum, pargv, envp);
		}
		u64 zend = (fend + pagesz - 1) & ~(pagesz - 1);
		if (mend > fend && p->filesz) {
			if (!(p->flags & PF_W)) die("a read-only segment has zero bytes past its file bytes");
			for (volatile unsigned char *z = (unsigned char *)fend; (u64)z < zend && (u64)z < mend; z++) *z = 0;
		}
		u64 astart = p->filesz ? zend : start;
		u64 aend = (mend + pagesz - 1) & ~(pagesz - 1);
		if (aend > astart) {
			if (mapfailed(sc6(SYS_mmap, astart, aend - astart, prot, MAP_PRIVATE | MAP_FIXED | MAP_ANONYMOUS, -1, 0)))
				die("cannot map zero pages");
		}
	}

	// ps and /proc/self/comm name the program, not this loader.
	sc2(SYS_prctl, PR_SET_NAME, name);

	// The payload's start stack: argc, argv, envp, then auxv with AT_EXECFD
	// added. It sits below this frame on a 16-byte boundary, as the ABI asks
	// of the kernel's.
	u64 nenv = (u64)(e - envp);
	u64 words = 1 + pargc + 1 + nenv + 1 + 2 * (naux + 2);
	if (words > MAX_STACK_WORDS) die("the start stack is too large");
	u64 here = (u64)&words;
	u64 *sp = (u64 *)((here - 256 - words * 8) & ~(u64)15);
	u64 *w = sp;
	*w++ = (u64)pargc;
	for (i64 i = 0; i < pargc; i++) *w++ = (u64)pargv[i];
	*w++ = 0;
	for (u64 i = 0; i < nenv; i++) *w++ = (u64)envp[i];
	*w++ = 0;
	for (u64 i = 0; i < naux; i++) {
		u64 tag = auxv[2 * i], val = auxv[2 * i + 1];
		if (tag == AT_EXECFD) continue;
		if (tag == AT_PHDR && phdrva) val = phdrva;
		if (tag == AT_PHNUM) val = eh.phnum;
		if (tag == AT_PHENT) val = sizeof(phdr);
		if (tag == AT_ENTRY) val = eh.entry;
		if (tag == AT_EXECFN) val = (u64)path;
		*w++ = tag;
		*w++ = val;
	}
	*w++ = AT_EXECFD;
	*w++ = (u64)fd;
	*w++ = AT_NULL;
	*w++ = 0;

#if defined(__x86_64__)
	// Cosmo libc's _start reads RDX as the program path, which is how dlopen
	// finds the program. /proc/self/exe names this loader. It reads a nonzero
	// RDI as the stack a macOS loader passed. A zero CL lets it detect the host.
	register u64 rsp __asm__("r8") = (u64)sp;
	register u64 rip __asm__("r9") = eh.entry;
	register u64 exe __asm__("r10") = (u64)path;
	__asm__ volatile("mov %0, %%rsp\n\tmov %2, %%rdx\n\txor %%eax, %%eax\n\txor %%ebx, %%ebx\n\txor %%ecx, %%ecx\n\t"
	                 "xor %%esi, %%esi\n\txor %%edi, %%edi\n\txor %%ebp, %%ebp\n\tjmp *%1"
	                 : : "r"(rsp), "r"(rip), "r"(exe) : "memory");
#else
	register u64 nsp __asm__("x9") = (u64)sp;
	register u64 pc __asm__("x10") = eh.entry;
	register u64 exe __asm__("x11") = (u64)path;
	// X2 is the program path. X3 and X15 name the host and the Syslib on a
	// macOS boot, and zero means Linux.
	__asm__ volatile("mov sp, %0\n\tmov x0, xzr\n\tmov x2, %2\n\tmov x3, xzr\n\tmov x15, xzr\n\tbr %1" : : "r"(nsp), "r"(pc), "r"(exe) : "memory");
#endif
	__builtin_unreachable();
<<<<<<< HEAD
>>>>>>> origin/master
=======
>>>>>>> origin/master
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
