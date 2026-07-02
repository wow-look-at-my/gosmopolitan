// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

import "unsafe"

// Darwin (macOS ARM64) syscall emulation.
//
// On macOS the APE loader hands the runtime a Syslib table of Apple libc
// functions; raw SVC syscalls are forbidden by XNU. The assembly fast path
// in asm_cosmo_arm64.s dispatches the hot syscalls straight to Syslib
// entries. Everything else lands here (via a framed CALL from the
// dispatcher) and is emulated with libc functions the runtime resolved
// through the Syslib's dlsym at startup (see runtime.osArchInit).
//
// Results follow the Linux syscall convention the callers expect:
// (r1, r2, errno) with a positive LINUX errno number. Apple errnos are
// translated with the shared table in runtime/sys_cosmo_arm64.s.

// DarwinFns holds host libc function pointers resolved by the runtime at
// startup on macOS. A zero field means the symbol was unavailable; the
// emulation then fails with ENOSYS so the gap is visible instead of
// silently misbehaving.
type DarwinFns struct {
	// Resolved via Syslib dlsym(RTLD_DEFAULT, ...).
	Getpid     uintptr
	Getppid    uintptr
	Getuid     uintptr
	Geteuid    uintptr
	Getgid     uintptr
	Getegid    uintptr
	Umask      uintptr
	Fcntl      uintptr
	Mkdirat    uintptr
	Unlinkat   uintptr
	Renameat   uintptr
	Fstatat    uintptr
	Fstat      uintptr
	Getcwd     uintptr
	Chdir      uintptr
	Faccessat  uintptr
	Readlinkat uintptr
	// Getdirentries is Apple's __getdirentries64: the raw directory
	// read behind libc's readdir. It reads at the descriptor's file
	// offset like Linux getdents64 (so lseek/dup semantics carry over),
	// which is what makes a stateless emulation possible.
	Getdirentries uintptr
	Error         uintptr // int *__error(void): Apple's errno location

	// Socket layer (socket_cosmo_arm64.go).
	Socket      uintptr
	Socketpair  uintptr
	Bind        uintptr
	Listen      uintptr
	Accept      uintptr
	Connect     uintptr
	Getsockname uintptr
	Getpeername uintptr
	Sendto      uintptr
	Recvfrom    uintptr
	Setsockopt  uintptr
	Getsockopt  uintptr
	Shutdown    uintptr

	// Process layer (exec_cosmo_arm64.go). Dup2/Setsid/Setpgid/Execve
	// are called between fork and exec (see that file's safety notes).
	Pipe    uintptr
	Dup2    uintptr
	Setsid  uintptr
	Setpgid uintptr
	Execve  uintptr
	Wait4   uintptr
	Kill    uintptr

	// Taken directly from the Syslib table.
	PthreadSelf uintptr
	Getentropy  uintptr // Syslib v5+; sysret-wrapped (-errno on failure)
	Close       uintptr // Syslib v4+; used for error-path fd cleanup
}

var darwinFns DarwinFns

// darwinErrorFn mirrors darwinFns.Error for the assembly errno
// trampoline (asm reads a plain variable rather than a struct field so
// the offset cannot rot).
var darwinErrorFn uintptr

// SetDarwinFns installs the resolved function table. Called once from
// runtime.osArchInit before any user code runs.
func SetDarwinFns(f *DarwinFns) {
	darwinFns = *f
	darwinErrorFn = f.Error
}

// Linux arm64 syscall numbers emulated only by the slow path. The shared
// fast-path numbers live in defs_cosmo_arm64.go.
const (
	sysGETCWD     = 17
	sysMKDIRAT    = 34
	sysUNLINKAT   = 35
	sysRENAMEAT   = 38
	sysFACCESSAT  = 48
	sysCHDIR      = 49
	sysGETDENTS64 = 61
	sysREADLINKAT = 78
	sysNEWFSTATAT = 79
	sysFSTAT      = 80
	sysUMASK      = 166
	sysGETPPID    = 173
	sysGETUID     = 174
	sysGETEUID    = 175
	sysGETGID     = 176
	sysGETEGID    = 177
	sysRENAMEAT2  = 276
	sysGETRANDOM  = 278
)

// Errno values (Linux numbering) produced by the emulation itself.
const (
	darwinEIO    = 5
	darwinEINVAL = 22
	darwinENOSYS = 38
)

// Linux AT_* values as passed by the syscall package.
const (
	linuxAT_FDCWD            = -100
	linuxAT_SYMLINK_NOFOLLOW = 0x100
	linuxAT_REMOVEDIR        = 0x200
	linuxAT_EMPTY_PATH       = 0x1000
)

// Apple equivalents.
const (
	appleAT_FDCWD            = -2
	appleAT_SYMLINK_NOFOLLOW = 0x20
	appleAT_REMOVEDIR        = 0x80
)

// appleTimespec matches Apple's struct timespec on arm64.
type appleTimespec struct {
	Sec  int64
	Nsec int64
}

// appleStat matches Apple's struct stat (__DARWIN_STRUCT_STAT64) on
// arm64: 144 bytes.
type appleStat struct {
	Dev      int32
	Mode     uint16
	Nlink    uint16
	Ino      uint64
	Uid      uint32
	Gid      uint32
	Rdev     int32
	_        int32
	Atim     appleTimespec
	Mtim     appleTimespec
	Ctim     appleTimespec
	Birthtim appleTimespec
	Size     int64
	Blocks   int64
	Blksize  int32
	Flags    uint32
	Gen      uint32
	Lspare   int32
	Qspare   [2]int64
}

// linuxTimespec matches syscall.Timespec for GOOS=cosmo.
type linuxTimespec struct {
	Sec  int64
	Nsec int64
}

// linuxStat must match syscall.Stat_t in syscall/ztypes_cosmo_arm64.go,
// which follows the arm64 Linux kernel layout (the same buffer is
// filled by the raw syscall on Linux hosts, so the emulation must write
// exactly what the kernel would).
type linuxStat struct {
	Dev     uint64
	Ino     uint64
	Mode    uint32
	Nlink   uint32
	Uid     uint32
	Gid     uint32
	Rdev    uint64
	_       uint64
	Size    int64
	Blksize int32
	_       int32
	Blocks  int64
	Atim    linuxTimespec
	Mtim    linuxTimespec
	Ctim    linuxTimespec
	_       [2]int32
}

// darwinLibcCall6 calls a C function pointer with up to six integer
// arguments following the Apple ARM64 ABI. Thin tail jump to
// runtime.cosmoLibcCall6 (see asm_cosmo_arm64.s in this package).
//
//go:noescape
func darwinLibcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) uintptr

// xlatErrnoDarwin translates a positive Apple errno to the Linux value.
// Thin wrapper over runtime.cosmo_xlat_errno_r0 (asm) so the byte table
// has a single definition.
//
//go:noescape
func xlatErrnoDarwin(errno uintptr) uintptr

// darwinErrno fetches the calling thread's errno via Apple's __error()
// and translates it to Linux numbering (EIO if __error is unavailable).
// Must be called immediately after a failed libc call, before anything
// else can clobber errno. Implemented in assembly with a minimal frame:
// every function on this path must be nosplit (syscall.Syscall has
// already run entersyscall, and growing the stack in _Gsyscall is a
// fatal "stack split at bad time"), and the assembly version keeps the
// deepest chains inside the 792-byte nosplit budget.
//
//go:noescape
func darwinErrno() uintptr

// darwinXlatDirfd converts the Linux AT_FDCWD sentinel (-100) to Apple's
// (-2). Other descriptors pass through unchanged.
//
//go:nosplit
func darwinXlatDirfd(fd uintptr) uintptr {
	if int32(fd) == linuxAT_FDCWD {
		return ^uintptr(1) // appleAT_FDCWD (-2) as a 64-bit bit pattern
	}
	return fd
}

// darwinCall runs a dlsym-resolved libc function that reports failure by
// returning -1 with errno set, shaping the result for Syscall6.
//
//go:nosplit
func darwinCall(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr) {
	if fn == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	r := darwinLibcCall6(fn, a1, a2, a3, a4, a5, a6)
	if int64(r) == -1 {
		return ^uintptr(0), 0, darwinErrno()
	}
	return r, 0, 0
}

// darwinCallNoError invokes a libc function that cannot fail (getpid and
// friends) and shapes the result for Syscall6.
//
//go:nosplit
func darwinCallNoError(fn uintptr) (r1, r2, errno uintptr) {
	if fn == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	return darwinLibcCall6(fn, 0, 0, 0, 0, 0, 0), 0, 0
}

// syscall6SlowDarwin emulates Linux syscalls that the assembly fast path
// does not handle, using dlsym-resolved Apple libc functions. It is
// called from Syscall6's darwin path, so it must keep exactly Syscall6's
// signature.
//
// The dispatch spine and every syscall a forked child can reach (the
// id family, umask, fcntl, chdir) are nosplit so that path never grows
// the stack; the linker verifies the bound. The stat family, getcwd
// and getrandom are deliberately NOT nosplit - they are never invoked
// between fork and exec, and their Apple stat buffers would blow the
// nosplit budget.
//
//go:nosplit
func syscall6SlowDarwin(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr) {
	switch num {
	case SYS_GETPID:
		return darwinCallNoError(darwinFns.Getpid)
	case sysGETPPID:
		return darwinCallNoError(darwinFns.Getppid)
	case sysGETUID:
		return darwinCallNoError(darwinFns.Getuid)
	case sysGETEUID:
		return darwinCallNoError(darwinFns.Geteuid)
	case sysGETGID:
		return darwinCallNoError(darwinFns.Getgid)
	case sysGETEGID:
		return darwinCallNoError(darwinFns.Getegid)
	case SYS_GETTID:
		// No Linux-style tid exists; use pthread_self like the
		// runtime's gettid does, so the two agree.
		return darwinCallNoError(darwinFns.PthreadSelf)
	case sysUMASK:
		if darwinFns.Umask == 0 {
			return ^uintptr(0), 0, darwinENOSYS
		}
		// umask cannot fail; mode bits have identical values on
		// Linux and Apple.
		return darwinLibcCall6(darwinFns.Umask, a1, 0, 0, 0, 0, 0), 0, 0

	case sysGETCWD:
		return darwinGetcwd(a1, a2)
	case sysMKDIRAT:
		// Argument layouts agree; mode bits are identical.
		return darwinCall(darwinFns.Mkdirat, darwinXlatDirfd(a1), a2, a3, 0, 0, 0)
	case sysUNLINKAT:
		flags := a3
		if flags&^uintptr(linuxAT_REMOVEDIR) != 0 {
			return ^uintptr(0), 0, darwinEINVAL
		}
		if flags&linuxAT_REMOVEDIR != 0 {
			flags = appleAT_REMOVEDIR
		}
		return darwinCall(darwinFns.Unlinkat, darwinXlatDirfd(a1), a2, flags, 0, 0, 0)
	case sysRENAMEAT:
		return darwinCall(darwinFns.Renameat, darwinXlatDirfd(a1), a2, darwinXlatDirfd(a3), a4, 0, 0)
	case sysRENAMEAT2:
		// Plain renames map to renameat; RENAME_NOREPLACE and friends
		// have no Apple equivalent (Linux likewise reports EINVAL on
		// filesystems without renameat2 support).
		if a5 != 0 {
			return ^uintptr(0), 0, darwinEINVAL
		}
		return darwinCall(darwinFns.Renameat, darwinXlatDirfd(a1), a2, darwinXlatDirfd(a3), a4, 0, 0)
	case sysFACCESSAT:
		// The Linux faccessat syscall has no flags argument; Apple's
		// libc faccessat takes one - pass 0. F_OK/X_OK/W_OK/R_OK
		// values are identical.
		return darwinCall(darwinFns.Faccessat, darwinXlatDirfd(a1), a2, a3, 0, 0, 0)
	case sysCHDIR:
		return darwinCall(darwinFns.Chdir, a1, 0, 0, 0, 0, 0)
	case sysREADLINKAT:
		return darwinCall(darwinFns.Readlinkat, darwinXlatDirfd(a1), a2, a3, a4, 0, 0)
	case sysNEWFSTATAT:
		return darwinFstatat(a1, a2, a3, a4)
	case sysFSTAT:
		return darwinFstat(a1, a2)
	case sysGETDENTS64:
		return darwinGetdents64(a1, a2, a3)
	case SYS_FCNTL:
		return darwinFcntl(a1, a2, a3)
	case sysGETRANDOM:
		return darwinGetrandom(a1, a2)

	case sysSOCKET:
		return darwinSocket(a1, a2, a3)
	case sysSOCKETPAIR:
		return darwinSocketpair(a1, a2, a3, a4)
	case sysBIND:
		return darwinBindConnect(darwinFns.Bind, a1, a2, a3)
	case sysCONNECT:
		return darwinBindConnect(darwinFns.Connect, a1, a2, a3)
	case sysLISTEN:
		return darwinCall(darwinFns.Listen, a1, a2, 0, 0, 0, 0)
	case sysACCEPT:
		return darwinAccept4(a1, a2, a3, 0)
	case sysACCEPT4:
		return darwinAccept4(a1, a2, a3, a4)
	case sysGETSOCKNAME:
		return darwinSockname(darwinFns.Getsockname, a1, a2, a3)
	case sysGETPEERNAME:
		return darwinSockname(darwinFns.Getpeername, a1, a2, a3)
	case sysSENDTO:
		return darwinSendto(a1, a2, a3, a4, a5, a6)
	case sysRECVFROM:
		return darwinRecvfrom(a1, a2, a3, a4, a5, a6)
	case sysSETSOCKOPT:
		return darwinSetsockopt(a1, a2, a3, a4, a5)
	case sysGETSOCKOPT:
		return darwinGetsockopt(a1, a2, a3, a4, a5)
	case sysSHUTDOWN:
		// SHUT_RD/SHUT_WR/SHUT_RDWR are 0/1/2 on both systems.
		return darwinCall(darwinFns.Shutdown, a1, a2, 0, 0, 0, 0)

	case sysPIPE2:
		return darwinPipe2(a1, a2)
	case sysDUP3:
		return darwinDup3(a1, a2, a3)
	case sysSETSID:
		return darwinCall(darwinFns.Setsid, 0, 0, 0, 0, 0, 0)
	case sysSETPGID:
		return darwinCall(darwinFns.Setpgid, a1, a2, 0, 0, 0, 0)
	case sysEXECVE:
		// argv/envp are NULL-terminated pointer arrays on both systems.
		// Success does not return.
		return darwinCall(darwinFns.Execve, a1, a2, a3, 0, 0, 0)
	case sysWAIT4:
		return darwinWait4(a1, a2, a3, a4)
	case sysKILL:
		return darwinKill(a1, a2)
	}
	// Not emulated. Return ENOSYS so the failure is visible rather than
	// pretending the call succeeded.
	return ^uintptr(0), 0, darwinENOSYS
}

// darwinGetcwd emulates the Linux getcwd syscall, which returns the
// number of bytes written including the trailing NUL, on top of Apple's
// libc getcwd, which returns the buffer pointer or NULL.
//
//go:nosplit
func darwinGetcwd(buf, size uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Getcwd == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if buf == 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	r := darwinLibcCall6(darwinFns.Getcwd, buf, size, 0, 0, 0, 0)
	if r == 0 {
		return ^uintptr(0), 0, darwinErrno()
	}
	n := uintptr(0)
	for n < size && *(*byte)(unsafe.Pointer(buf + n)) != 0 {
		n++
	}
	if n >= size {
		return ^uintptr(0), 0, darwinEINVAL // no NUL: shouldn't happen
	}
	return n + 1, 0, 0
}

// darwinStatConvert fills a Go (Linux-layout) Stat_t from an Apple stat.
// File type and permission bits share the same encoding on both systems,
// so Mode passes through. Dev/Rdev keep Apple's device encoding; nothing
// in the standard library depends on the major/minor split.
//
//go:nosplit
func darwinStatConvert(dst *linuxStat, src *appleStat) {
	*dst = linuxStat{}
	dst.Dev = uint64(uint32(src.Dev))
	dst.Ino = src.Ino
	dst.Nlink = uint32(src.Nlink)
	dst.Mode = uint32(src.Mode)
	dst.Uid = src.Uid
	dst.Gid = src.Gid
	dst.Rdev = uint64(uint32(src.Rdev))
	dst.Size = src.Size
	dst.Blksize = src.Blksize
	dst.Blocks = src.Blocks
	dst.Atim = linuxTimespec(src.Atim)
	dst.Mtim = linuxTimespec(src.Mtim)
	dst.Ctim = linuxTimespec(src.Ctim)
}

//go:nosplit
func darwinFstatat(dirfd, path, statbuf, flags uintptr) (r1, r2, errno uintptr) {
	if statbuf == 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	var ast appleStat
	if flags&linuxAT_EMPTY_PATH != 0 && (path == 0 || *(*byte)(unsafe.Pointer(path)) == 0) {
		// fstatat(fd, "", AT_EMPTY_PATH) means fstat(fd) on Linux;
		// Apple has no AT_EMPTY_PATH, so call fstat directly.
		if darwinFns.Fstat == 0 {
			return ^uintptr(0), 0, darwinENOSYS
		}
		r := darwinLibcCall6(darwinFns.Fstat, dirfd, uintptr(unsafe.Pointer(&ast)), 0, 0, 0, 0)
		if int64(r) == -1 {
			return ^uintptr(0), 0, darwinErrno()
		}
		darwinStatConvert((*linuxStat)(unsafe.Pointer(statbuf)), &ast)
		return 0, 0, 0
	}
	if flags&^uintptr(linuxAT_SYMLINK_NOFOLLOW|linuxAT_EMPTY_PATH) != 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	aflags := uintptr(0)
	if flags&linuxAT_SYMLINK_NOFOLLOW != 0 {
		aflags |= appleAT_SYMLINK_NOFOLLOW
	}
	if darwinFns.Fstatat == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	r := darwinLibcCall6(darwinFns.Fstatat, darwinXlatDirfd(dirfd), path, uintptr(unsafe.Pointer(&ast)), aflags, 0, 0)
	if int64(r) == -1 {
		return ^uintptr(0), 0, darwinErrno()
	}
	darwinStatConvert((*linuxStat)(unsafe.Pointer(statbuf)), &ast)
	return 0, 0, 0
}

//go:nosplit
func darwinFstat(fd, statbuf uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Fstat == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if statbuf == 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	var ast appleStat
	r := darwinLibcCall6(darwinFns.Fstat, fd, uintptr(unsafe.Pointer(&ast)), 0, 0, 0, 0)
	if int64(r) == -1 {
		return ^uintptr(0), 0, darwinErrno()
	}
	darwinStatConvert((*linuxStat)(unsafe.Pointer(statbuf)), &ast)
	return 0, 0, 0
}

// Dirent record header sizes for the getdents64 emulation. The fixed
// fields before d_name:
//
//	Apple __getdirentries64 record     Linux dirent64 record
//	 0  d_ino     uint64                0  d_ino    uint64
//	 8  d_seekoff uint64                8  d_off    int64
//	16  d_reclen  uint16               16  d_reclen uint16
//	18  d_namlen  uint16               18  d_type   uint8
//	20  d_type    uint8                19  d_name   [] (NUL-terminated)
//	21  d_name    [] (NUL-terminated)
//
// d_ino/d_reclen line up; d_seekoff is the next-entry seek cookie, the
// same role Linux gives d_off. The d_type VALUES are identical on both
// systems (shared BSD lineage, DT_* = S_IFMT>>12): DT_UNKNOWN 0,
// DT_FIFO 1, DT_CHR 2, DT_DIR 4, DT_BLK 6, DT_REG 8, DT_LNK 10,
// DT_SOCK 12, DT_WHT 14 - verified against Linux include/dirent.h and
// Apple bsd/sys/dirent.h, so d_type passes through untranslated.
const (
	appleDirentHdrLen = 21
	linuxDirentHdrLen = 19
)

// darwinGetdents64 emulates the Linux getdents64 syscall with Apple's
// __getdirentries64 (the raw fd-offset-based directory read behind
// libc readdir; resolved via dlsym - xnu's own userspace tests link it,
// it is exported from libSystem). Apple fills the caller's buffer with
// Apple-layout records, which are then rewritten IN PLACE into Linux
// dirent64 records.
//
// The in-place rewrite is safe front to back: for any name length the
// Linux record is never longer than the Apple record (19- vs 21-byte
// header, both padded to 8), so the write cursor can never pass the
// read cursor; the header fields are copied into locals before the
// destination header is stored, and the name copy runs low-to-high
// with dst+19 <= src+21. This also means every rewritten record fits
// where its source stood - no partial-record truncation is possible.
//
// Quirk (xnu bsd/sys/dirent_private.h): when bufsize >= 1024, the
// kernel reserves the FINAL 4 bytes of the buffer for a flags word
// (GETDIRENTRIES64_EOF) and fills at most bufsize-4 bytes of records.
// Returning slightly fewer bytes per call than Linux would is fine;
// the flags word lands beyond the returned length, where callers
// (syscall.ParseDirent, os) never look.
//
//go:nosplit
func darwinGetdents64(fd, buf, count uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Getdirentries == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if buf == 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	// The kernel writes the resulting directory offset here; the fd's
	// file offset advances identically (Linux getdents64 semantics), so
	// the value is not needed - but the pointer must be valid.
	var basep int64
	r := darwinLibcCall6(darwinFns.Getdirentries, fd, buf, count,
		uintptr(unsafe.Pointer(&basep)), 0, 0)
	if int64(r) < 0 {
		return ^uintptr(0), 0, darwinErrno()
	}
	n := r // bytes of Apple records; 0 means end of directory
	if n > count {
		return ^uintptr(0), 0, darwinEIO
	}
	var src, dst uintptr
	for src+appleDirentHdrLen <= n {
		ino := *(*uint64)(unsafe.Pointer(buf + src))
		seekoff := *(*uint64)(unsafe.Pointer(buf + src + 8))
		areclen := uintptr(*(*uint16)(unsafe.Pointer(buf + src + 16)))
		namlen := uintptr(*(*uint16)(unsafe.Pointer(buf + src + 18)))
		typ := *(*byte)(unsafe.Pointer(buf + src + 20))
		if areclen < appleDirentHdrLen || src+areclen > n ||
			namlen > areclen-appleDirentHdrLen {
			// Malformed record: refuse to guess at the rest.
			return ^uintptr(0), 0, darwinEIO
		}
		lreclen := (linuxDirentHdrLen + namlen + 1 + 7) &^ 7
		if dst+lreclen > src+areclen {
			// Unreachable with kernel-packed (8-aligned) records; kept
			// so a pathological record cannot overrun unread input.
			return ^uintptr(0), 0, darwinEIO
		}
		*(*uint64)(unsafe.Pointer(buf + dst)) = ino
		*(*uint64)(unsafe.Pointer(buf + dst + 8)) = seekoff
		*(*uint16)(unsafe.Pointer(buf + dst + 16)) = uint16(lreclen)
		*(*byte)(unsafe.Pointer(buf + dst + 18)) = typ
		for i := uintptr(0); i < namlen; i++ {
			*(*byte)(unsafe.Pointer(buf + dst + linuxDirentHdrLen + i)) =
				*(*byte)(unsafe.Pointer(buf + src + appleDirentHdrLen + i))
		}
		// Callers find the name end by NUL scan (Linux d_namlen does not
		// exist); the +1 in lreclen guarantees room.
		*(*byte)(unsafe.Pointer(buf + dst + linuxDirentHdrLen + namlen)) = 0
		src += areclen
		dst += lreclen
	}
	return dst, 0, 0
}

// fcntl commands F_DUPFD..F_SETFL share values 0..4 on Linux and Apple;
// F_DUPFD_CLOEXEC and the O_ status flags differ.
const (
	fcntlF_DUPFD = 0
	fcntlF_GETFD = 1
	fcntlF_SETFD = 2
	fcntlF_GETFL = 3
	fcntlF_SETFL = 4

	linuxF_DUPFD_CLOEXEC = 1030
	appleF_DUPFD_CLOEXEC = 67

	linuxO_NONBLOCK = 0x800
	appleO_NONBLOCK = 0x4
	linuxO_APPEND   = 0x400
	appleO_APPEND   = 0x8
)

//go:nosplit
func darwinFcntl(fd, cmd, arg uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Fcntl == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	switch cmd {
	case fcntlF_DUPFD, fcntlF_GETFD, fcntlF_SETFD:
		// Identical commands; FD_CLOEXEC is 1 on both systems.
	case fcntlF_GETFL:
		r1, r2, errno = darwinCall(darwinFns.Fcntl, fd, cmd, 0, 0, 0, 0)
		if errno == 0 {
			// Translate returned status flags Apple -> Linux.
			out := r1 & 3 // access mode bits agree
			if r1&appleO_NONBLOCK != 0 {
				out |= linuxO_NONBLOCK
			}
			if r1&appleO_APPEND != 0 {
				out |= linuxO_APPEND
			}
			r1 = out
		}
		return r1, r2, errno
	case fcntlF_SETFL:
		aarg := arg & 3
		if arg&linuxO_NONBLOCK != 0 {
			aarg |= appleO_NONBLOCK
		}
		if arg&linuxO_APPEND != 0 {
			aarg |= appleO_APPEND
		}
		arg = aarg
	case linuxF_DUPFD_CLOEXEC:
		cmd = appleF_DUPFD_CLOEXEC
	default:
		// Locking, owner and lease commands have incompatible
		// argument structures; refuse rather than corrupt.
		return ^uintptr(0), 0, darwinENOSYS
	}
	return darwinCall(darwinFns.Fcntl, fd, cmd, arg, 0, 0, 0)
}

// darwinGetrandom emulates getrandom(2) with the Syslib's getentropy,
// which is limited to 256 bytes per call and never blocks (the flags
// argument is therefore ignored). getentropy is sysret-wrapped by the
// loader: it returns -errno (Apple numbering) on failure.
//
//go:nosplit
func darwinGetrandom(buf, n uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Getentropy == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	var off uintptr
	for off < n {
		chunk := n - off
		if chunk > 256 {
			chunk = 256
		}
		r := int64(darwinLibcCall6(darwinFns.Getentropy, buf+off, chunk, 0, 0, 0, 0))
		if r < 0 {
			return ^uintptr(0), 0, xlatErrnoDarwin(uintptr(-r))
		}
		off += chunk
	}
	return n, 0, 0
}
