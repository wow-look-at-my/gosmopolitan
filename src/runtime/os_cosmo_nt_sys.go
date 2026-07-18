// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Windows NT syscall emulation (wave 2): the Emulate dispatcher
// installed into internal/runtime/syscall/cosmo's WindowsFns table.
//
// Every user-level syscall on an NT host funnels here with LINUX
// amd64 numbering and must come back with Linux semantics: Linux
// errnos, Linux struct layouts (stat, dirent64), Linux flag values.
// The Win32 mapping follows cosmo libc's precedent throughout.
//
// Execution model (see syscall_cosmo_nt.go and syscall_cosmo.go): the
// syscall package skips entersyscall on the NT route, so the backends
// here are ordinary Go - they allocate for path translation and
// struct synthesis - and individually bracket the genuinely blocking
// Win32 calls (ReadFile/WriteFile/FlushFileBuffers) with
// entersyscall via ntcallSE. Quick metadata calls (CreateFileW,
// stat, delete, move, mkdir, cwd) stay plain ntcallE: they are
// bounded local-disk operations.
//
// Pointer discipline: syscall arguments may point into the calling
// goroutine's STACK. Raw uintptrs are not adjusted when a stack
// grows, so ntSyscallEmulate is nosplit (as is its entire caller
// chain from syscall.Syscall down) and re-types every
// pointer-carrying argument in the dispatch call expression itself;
// from there on the backends hold real pointers, which stack copying
// adjusts. Passing them back to Win32 as uintptr happens only inside
// the nosplit ntcallE/ntcallSE helpers, where no growth can occur.
//
// Unimplemented syscalls return ENOSYS so gaps stay visible (the
// cosmo graceful-stub philosophy); the probe's socket and exec checks
// currently fail through exactly that path.

package runtime

import "unsafe"

// Linux amd64 syscall numbers emulated here (the shared subset lives
// in internal/runtime/syscall/cosmo/defs_cosmo_amd64.go; duplicating
// the few overlapping values avoids importing more of that package's
// namespace into the runtime).
const (
	ntSysRead       = 0
	ntSysWrite      = 1
	ntSysClose      = 3
	ntSysStat       = 4
	ntSysFstat      = 5
	ntSysLstat      = 6
	ntSysLseek      = 8
	ntSysPread64    = 17
	ntSysPwrite64   = 18
	ntSysGetpid     = 39
	ntSysExit       = 60
	ntSysFcntl      = 72
	ntSysFsync      = 74
	ntSysFdatasync  = 75
	ntSysFtruncate  = 77
	ntSysGetcwd     = 79
	ntSysChdir      = 80
	ntSysFchmod     = 91
	ntSysUmask      = 95
	ntSysGetuid     = 102
	ntSysGetgid     = 104
	ntSysGeteuid    = 107
	ntSysGetegid    = 108
	ntSysGetppid    = 110
	ntSysGetpgrp    = 111
	ntSysGettid     = 186
	ntSysGetdents64 = 217
	ntSysExitGroup  = 231
	ntSysOpenat     = 257
	ntSysMkdirat    = 258
	ntSysNewfstatat = 262
	ntSysUnlinkat   = 263
	ntSysRenameat   = 264
	ntSysReadlinkat = 267
	ntSysFchmodat   = 268
	ntSysFaccessat  = 269
	ntSysGetrandom  = 318
)

// Linux errno values produced by the emulation.
const (
	ntENOENT       = 2
	ntEIO          = 5
	ntEBADF        = 9
	ntENOMEM       = 12
	ntEACCES       = 13
	ntEBUSY        = 16
	ntEEXIST       = 17
	ntEXDEV        = 18
	ntENOTDIR      = 20
	ntEISDIR       = 21
	ntEINVAL       = 22
	ntEMFILE       = 24
	ntENOSPC       = 28
	ntESPIPE       = 29
	ntEROFS        = 30
	ntEPIPE        = 32
	ntERANGE       = 34
	ntENAMETOOLONG = 36
	ntENOSYS       = 38
	ntENOTEMPTY    = 39
	ntELOOP        = 40
)

// Win32 constants.
const (
	_NT_GENERIC_READ         = 0x80000000
	_NT_GENERIC_WRITE        = 0x40000000
	_NT_FILE_APPEND_DATA     = 0x0004
	_NT_FILE_READ_ATTRIBUTES = 0x0080
	_NT_FILE_SHARE_ALL       = 0x7 // READ|WRITE|DELETE, unconditionally (unix openness)

	_NT_CREATE_NEW        = 1
	_NT_CREATE_ALWAYS     = 2
	_NT_OPEN_EXISTING     = 3
	_NT_OPEN_ALWAYS       = 4
	_NT_TRUNCATE_EXISTING = 5

	_NT_FILE_ATTRIBUTE_READONLY    = 0x1
	_NT_FILE_ATTRIBUTE_DIRECTORY   = 0x10
	_NT_FILE_ATTRIBUTE_NORMAL      = 0x80
	_NT_FILE_FLAG_BACKUP_SEMANTICS = 0x02000000

	_NT_INVALID_HANDLE_VALUE    = ^uintptr(0)
	_NT_INVALID_FILE_ATTRIBUTES = 0xFFFFFFFF
	_NT_CURRENT_PROCESS         = ^uintptr(0) // GetCurrentProcess() pseudo-handle

	_NT_MOVEFILE_REPLACE_EXISTING = 0x1

	_NT_FILE_BEGIN   = 0
	_NT_FILE_CURRENT = 1
	_NT_FILE_END     = 2

	_NT_FILE_TYPE_DISK = 1
	_NT_FILE_TYPE_CHAR = 2
	_NT_FILE_TYPE_PIPE = 3

	_NT_ERROR_NO_MORE_FILES = 18
	_NT_ERROR_HANDLE_EOF    = 38
	_NT_ERROR_BROKEN_PIPE   = 109

	_NT_FileIdBothDirectoryInfo        = 10
	_NT_FileIdBothDirectoryRestartInfo = 11

	_NT_ENABLE_VIRTUAL_TERMINAL_PROCESSING = 0x4
	_NT_CP_UTF8                            = 65001
)

// Linux flag and mode-bit values (amd64).
const (
	_NT_O_ACCMODE   = 3
	_NT_O_RDONLY    = 0
	_NT_O_WRONLY    = 1
	_NT_O_RDWR      = 2
	_NT_O_CREAT     = 0x40
	_NT_O_EXCL      = 0x80
	_NT_O_TRUNC     = 0x200
	_NT_O_APPEND    = 0x400
	_NT_O_NONBLOCK  = 0x800
	_NT_O_DIRECTORY = 0x10000
	_NT_O_CLOEXEC   = 0x80000

	_NT_AT_FDCWD      = -100
	_NT_AT_REMOVEDIR  = 0x200
	_NT_AT_EMPTY_PATH = 0x1000

	_NT_S_IFIFO = 0x1000
	_NT_S_IFCHR = 0x2000
	_NT_S_IFDIR = 0x4000
	_NT_S_IFREG = 0x8000

	_NT_DT_DIR = 4
	_NT_DT_REG = 8
)

// ntErrno maps a Win32 GetLastError code to the Linux errno the
// unix-shaped standard library expects. One table, used by every
// emulated file syscall (cosmo libc keeps an equivalent
// __dosemapping table).
func ntErrno(werr uintptr) uintptr {
	switch werr {
	case 2, 3, 123, 161: // FILE_NOT_FOUND, PATH_NOT_FOUND, INVALID_NAME, BAD_PATHNAME
		return ntENOENT
	case 267: // ERROR_DIRECTORY ("the directory name is invalid")
		return ntENOTDIR
	case 5, 32, 33: // ACCESS_DENIED, SHARING_VIOLATION, LOCK_VIOLATION
		return ntEACCES
	case 80, 183: // FILE_EXISTS, ALREADY_EXISTS
		return ntEEXIST
	case 145: // DIR_NOT_EMPTY
		return ntENOTEMPTY
	case 6: // INVALID_HANDLE
		return ntEBADF
	case 8, 14: // NOT_ENOUGH_MEMORY, OUTOFMEMORY
		return ntENOMEM
	case 87: // INVALID_PARAMETER
		return ntEINVAL
	case _NT_ERROR_BROKEN_PIPE, 232: // BROKEN_PIPE, NO_DATA
		return ntEPIPE
	case 112: // DISK_FULL
		return ntENOSPC
	case 4: // TOO_MANY_OPEN_FILES
		return ntEMFILE
	case 17: // NOT_SAME_DEVICE
		return ntEXDEV
	case 16: // CURRENT_DIRECTORY (removing the cwd)
		return ntEBUSY
	case 19: // WRITE_PROTECT
		return ntEROFS
	case 131: // NEGATIVE_SEEK
		return ntEINVAL
	case 25: // SEEK (device cannot seek)
		return ntESPIPE
	case 206: // FILENAME_EXCED_RANGE
		return ntENAMETOOLONG
	case 1921: // CANT_RESOLVE_FILENAME
		return ntELOOP
	}
	return ntEIO
}

//go:nosplit
func ntFail3(errno uintptr) (uintptr, uintptr, uintptr) {
	return ^uintptr(0), 0, errno
}

// ntSyscallEmulate is the WindowsFns.Emulate hook: dispatch by Linux
// syscall number to the typed backends. MUST stay nosplit and MUST
// convert pointer-carrying uintptr arguments to pointer types right
// here in the call expressions - see the pointer-discipline note at
// the top of the file.
//
//go:nosplit
func ntSyscallEmulate(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr) {
	switch num {
	case ntSysRead:
		return ntEmuRead(int32(a1), unsafe.Pointer(a2), int32(a3))
	case ntSysWrite:
		return ntEmuWrite(int32(a1), unsafe.Pointer(a2), int32(a3))
	case ntSysOpenat:
		return ntEmuOpenat(int32(a1), (*byte)(unsafe.Pointer(a2)), int32(a3), uint32(a4))
	case ntSysClose:
		return ntEmuClose(int32(a1))
	case ntSysStat, ntSysLstat:
		// No symlink support this wave: lstat == stat.
		return ntEmuStat((*byte)(unsafe.Pointer(a1)), (*ntLinuxStat)(unsafe.Pointer(a2)))
	case ntSysFstat:
		return ntEmuFstat(int32(a1), (*ntLinuxStat)(unsafe.Pointer(a2)))
	case ntSysNewfstatat:
		return ntEmuFstatat(int32(a1), (*byte)(unsafe.Pointer(a2)), (*ntLinuxStat)(unsafe.Pointer(a3)), int32(a4))
	case ntSysLseek:
		return ntEmuLseek(int32(a1), int64(a2), a3)
	case ntSysPread64:
		return ntEmuPreadPwrite(int32(a1), unsafe.Pointer(a2), int32(a3), int64(a4), false)
	case ntSysPwrite64:
		return ntEmuPreadPwrite(int32(a1), unsafe.Pointer(a2), int32(a3), int64(a4), true)
	case ntSysGetdents64:
		return ntEmuGetdents(int32(a1), unsafe.Pointer(a2), a3)
	case ntSysMkdirat:
		return ntEmuMkdirat(int32(a1), (*byte)(unsafe.Pointer(a2)))
	case ntSysUnlinkat:
		return ntEmuUnlinkat(int32(a1), (*byte)(unsafe.Pointer(a2)), int32(a3))
	case ntSysRenameat:
		return ntEmuRenameat(int32(a1), (*byte)(unsafe.Pointer(a2)), int32(a3), (*byte)(unsafe.Pointer(a4)))
	case ntSysReadlinkat:
		return ntEmuReadlinkat(int32(a1), (*byte)(unsafe.Pointer(a2)), unsafe.Pointer(a3), a4)
	case ntSysFaccessat:
		return ntEmuFaccessat(int32(a1), (*byte)(unsafe.Pointer(a2)), uint32(a3))
	case ntSysChdir:
		return ntEmuChdir((*byte)(unsafe.Pointer(a1)))
	case ntSysGetcwd:
		return ntEmuGetcwd(unsafe.Pointer(a1), a2)
	case ntSysFtruncate:
		return ntEmuFtruncate(int32(a1), int64(a2))
	case ntSysFsync, ntSysFdatasync:
		return ntEmuFsync(int32(a1))
	case ntSysFchmod:
		return ntEmuFchmod(int32(a1))
	case ntSysFchmodat:
		return ntEmuFchmodat(int32(a1), (*byte)(unsafe.Pointer(a2)))
	case ntSysFcntl:
		ret, eno := ntFcntl(int32(a1), int32(a2), int32(a3))
		if eno != 0 {
			return ntFail3(uintptr(eno))
		}
		return uintptr(ret), 0, 0
	case ntSysGetrandom:
		return ntEmuGetrandom(unsafe.Pointer(a1), a2)

	case ntSysGetpid, ntSysGetpgrp:
		// getpgrp: no process groups on NT; report the pid, which is
		// its own group leader.
		return uintptr(uint32(ntcall(ntGetCurrentProcessIdFn, 0, 0, 0, 0, 0, 0))), 0, 0
	case ntSysGetppid:
		return ntEmuGetppid()
	case ntSysGettid:
		// Must agree with minitProcid (os_cosmo_amd64.go), which also
		// uses GetCurrentThreadId.
		return uintptr(uint32(ntcall(ntGetCurrentThreadIdFn, 0, 0, 0, 0, 0, 0))), 0, 0
	case ntSysGetuid, ntSysGeteuid, ntSysGetgid, ntSysGetegid:
		// No unix identity on NT; report 0 (cosmo reports root-ish
		// ids too; nothing in the library gates on them).
		return 0, 0, 0
	case ntSysUmask:
		// No umask concept; report the conventional 022 and ignore
		// the new value.
		return 0o22, 0, 0

	case ntSysExit, ntSysExitGroup:
		exit(int32(a1))
		return 0, 0, 0 // unreachable
	}
	return ntFail3(ntENOSYS)
}

// ---- identity ----

// ntPBI is PROCESS_BASIC_INFORMATION on x64 (48 bytes): ExitStatus,
// PebBaseAddress, AffinityMask, BasePriority, UniqueProcessId,
// InheritedFromUniqueProcessId - we want word 5.
func ntEmuGetppid() (r1, r2, errno uintptr) {
	if ntQueryInformationProcessFn == 0 {
		return ntFail3(ntENOSYS)
	}
	var pbi [6]uint64
	st := ntcall(ntQueryInformationProcessFn, _NT_CURRENT_PROCESS, 0, /* ProcessBasicInformation */
		uintptr(unsafe.Pointer(&pbi[0])), unsafe.Sizeof(pbi), 0, 0)
	if int32(uint32(st)) < 0 { // NTSTATUS failure = high bit set
		return ntFail3(ntEINVAL)
	}
	return uintptr(uint32(pbi[5])), 0, 0
}

// ---- entropy ----

func ntEmuGetrandom(p unsafe.Pointer, n uintptr) (r1, r2, errno uintptr) {
	if ntProcessPrngFn == 0 {
		return ntFail3(ntENOSYS)
	}
	if n == 0 {
		return 0, 0, 0
	}
	if ntcall(ntProcessPrngFn, uintptr(p), n, 0, 0, 0, 0) == 0 {
		return ntFail3(ntEIO)
	}
	return n, 0, 0
}

// ntReadRandom backs the runtime's own readRandom (os_cosmo.go) on NT
// hosts. Plain ntcall - runs at boot on g0, so no entersyscall.
func ntReadRandom(r []byte) int {
	if ntProcessPrngFn == 0 || len(r) == 0 {
		return 0
	}
	if ntcall(ntProcessPrngFn, uintptr(unsafe.Pointer(&r[0])), uintptr(len(r)), 0, 0, 0, 0) == 0 {
		return 0
	}
	return len(r)
}

// ---- read/write ----

func ntEmuRead(fd int32, p unsafe.Pointer, n int32) (r1, r2, errno uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if e.kind == ntFDDir {
		return ntFail3(ntEISDIR)
	}
	if e.flags&_NT_O_ACCMODE == _NT_O_WRONLY {
		return ntFail3(ntEBADF)
	}
	if n < 0 {
		return ntFail3(ntEINVAL)
	}
	if n == 0 {
		return 0, 0, 0
	}
	var got uint32
	r, werr := ntcallSE(ntReadFileFn, e.handle, uintptr(p), uintptr(uint32(n)),
		uintptr(unsafe.Pointer(&got)), 0, 0, 0)
	if r == 0 {
		// A pipe closed by the writer or an explicit EOF both mean
		// end-of-file in Linux terms.
		if werr == _NT_ERROR_BROKEN_PIPE || werr == _NT_ERROR_HANDLE_EOF {
			return 0, 0, 0
		}
		return ntFail3(ntErrno(werr))
	}
	return uintptr(got), 0, 0
}

func ntEmuWrite(fd int32, p unsafe.Pointer, n int32) (r1, r2, errno uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if e.kind == ntFDDir {
		return ntFail3(ntEBADF)
	}
	if e.flags&_NT_O_ACCMODE == _NT_O_RDONLY {
		return ntFail3(ntEBADF)
	}
	if n < 0 {
		return ntFail3(ntEINVAL)
	}
	if n == 0 {
		return 0, 0, 0
	}
	var written uint32
	r, werr := ntcallSE(ntWriteFileFn, e.handle, uintptr(p), uintptr(uint32(n)),
		uintptr(unsafe.Pointer(&written)), 0, 0, 0)
	if r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return uintptr(written), 0, 0
}

// ---- open/close ----

// ntAtPathW resolves an *at-style (dirfd, path) pair to a Win32 path.
// Absolute paths and AT_FDCWD use the translation policy directly;
// relative paths against a real dirfd are joined onto the directory's
// recorded Win32 path (NT has no native openat, cosmo does the same
// join).
func ntAtPathW(dirfd int32, path string) ([]uint16, uintptr) {
	if path == "" {
		return nil, ntENOENT
	}
	abs := path[0] == '/' || (len(path) >= 2 && ntIsAlpha(path[0]) && path[1] == ':')
	if abs || dirfd == _NT_AT_FDCWD {
		w := ntPathW(path)
		if w == nil {
			return nil, ntENOENT
		}
		return w, 0
	}
	e, ok := ntFDLookup(dirfd)
	if !ok {
		return nil, ntEBADF
	}
	if e.kind != ntFDDir || len(e.pathW) < 2 {
		return nil, ntENOTDIR
	}
	base := e.pathW[:len(e.pathW)-1] // strip NUL
	w := make([]uint16, 0, len(base)+1+len(path)+1)
	w = append(w, base...)
	if w[len(w)-1] != '\\' {
		w = append(w, '\\')
	}
	w = ntUTF16Append(w, path, true)
	return append(w, 0), 0
}

func ntEmuOpenat(dirfd int32, cpath *byte, flags int32, mode uint32) (r1, r2, errno uintptr) {
	path := ntCPath(cpath)
	w, eno := ntAtPathW(dirfd, path)
	if eno != 0 {
		return ntFail3(eno)
	}

	accmode := flags & _NT_O_ACCMODE
	access := uintptr(_NT_FILE_READ_ATTRIBUTES) // so fstat works on write-only fds
	if accmode == _NT_O_RDONLY || accmode == _NT_O_RDWR {
		access |= _NT_GENERIC_READ
	}
	if accmode == _NT_O_WRONLY || accmode == _NT_O_RDWR {
		if flags&_NT_O_APPEND != 0 {
			// FILE_APPEND_DATA without FILE_WRITE_DATA makes every
			// WriteFile an atomic append (upstream Go does the same).
			access |= _NT_FILE_APPEND_DATA
		} else {
			access |= _NT_GENERIC_WRITE
		}
	}
	var disp uintptr
	switch {
	case flags&(_NT_O_CREAT|_NT_O_EXCL) == _NT_O_CREAT|_NT_O_EXCL:
		disp = _NT_CREATE_NEW
	case flags&(_NT_O_CREAT|_NT_O_TRUNC) == _NT_O_CREAT|_NT_O_TRUNC:
		disp = _NT_CREATE_ALWAYS
	case flags&_NT_O_CREAT != 0:
		disp = _NT_OPEN_ALWAYS
	case flags&_NT_O_TRUNC != 0:
		disp = _NT_TRUNCATE_EXISTING
	default:
		disp = _NT_OPEN_EXISTING
	}
	attrs := uintptr(_NT_FILE_ATTRIBUTE_NORMAL)
	if flags&_NT_O_CREAT != 0 && mode&0o200 == 0 {
		attrs = _NT_FILE_ATTRIBUTE_READONLY
	}
	// FILE_FLAG_BACKUP_SEMANTICS unconditionally: it is what allows
	// CreateFileW to open a DIRECTORY (os.Open of a directory is a
	// perfectly normal unix operation), and it is harmless for files.
	h, werr := ntcallE(ntCreateFileWFn, uintptr(unsafe.Pointer(&w[0])), access,
		_NT_FILE_SHARE_ALL, 0, disp, attrs|_NT_FILE_FLAG_BACKUP_SEMANTICS, 0)
	if h == _NT_INVALID_HANDLE_VALUE {
		KeepAlive(w)
		return ntFail3(ntErrno(werr))
	}

	// Classify what we opened.
	kind := ntFDFile
	var ftype uint8
	var info ntByHandleFileInformation
	if r, _ := ntcallE(ntGetFileInformationByHandleFn, h, uintptr(unsafe.Pointer(&info)), 0, 0, 0, 0, 0); r != 0 {
		if info.FileAttributes&_NT_FILE_ATTRIBUTE_DIRECTORY != 0 {
			kind = ntFDDir
		}
	} else {
		// Not a disk object (NUL, console device): stdio-like.
		kind = ntFDStdio
		ftype = uint8(ntcall(ntGetFileTypeFn, h, 0, 0, 0, 0, 0))
	}
	if flags&_NT_O_DIRECTORY != 0 && kind != ntFDDir {
		ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
		return ntFail3(ntENOTDIR)
	}
	if kind == ntFDDir && accmode != _NT_O_RDONLY {
		ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
		return ntFail3(ntEISDIR)
	}

	fd := ntFDAlloc(h, kind, flags, flags&_NT_O_CLOEXEC != 0, w)
	if fd < 0 {
		ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
		return ntFail3(uintptr(-fd))
	}
	if kind == ntFDStdio {
		ntFDSetFtype(fd, ftype)
	}
	return uintptr(fd), 0, 0
}

func ntEmuClose(fd int32) (r1, r2, errno uintptr) {
	h, ok := ntFDRelease(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
	return 0, 0, 0
}

// ---- stat ----

// ntByHandleFileInformation is BY_HANDLE_FILE_INFORMATION (52 bytes,
// all DWORDs; the three FILETIMEs are split into lo/hi pairs).
type ntByHandleFileInformation struct {
	FileAttributes     uint32
	CreationTimeLo     uint32
	CreationTimeHi     uint32
	LastAccessTimeLo   uint32
	LastAccessTimeHi   uint32
	LastWriteTimeLo    uint32
	LastWriteTimeHi    uint32
	VolumeSerialNumber uint32
	FileSizeHigh       uint32
	FileSizeLow        uint32
	NumberOfLinks      uint32
	FileIndexHigh      uint32
	FileIndexLow       uint32
}

type ntLinuxTimespec struct {
	sec  int64
	nsec int64
}

// ntLinuxStat must match syscall.Stat_t in
// syscall/ztypes_cosmo_amd64.go (the Linux amd64 kernel layout, 144
// bytes): the same buffer is filled by the raw syscall on Linux
// hosts, so the emulation writes exactly what the kernel would.
type ntLinuxStat struct {
	dev     uint64
	ino     uint64
	nlink   uint64
	mode    uint32
	uid     uint32
	gid     uint32
	_       int32
	rdev    uint64
	size    int64
	blksize int64
	blocks  int64
	atim    ntLinuxTimespec
	mtim    ntLinuxTimespec
	ctim    ntLinuxTimespec
	_       [3]int64
}

// ntFiletimeToTimespec converts a Windows FILETIME (100ns ticks since
// 1601-01-01) to a unix timespec.
func ntFiletimeToTimespec(lo, hi uint32) ntLinuxTimespec {
	ft := uint64(hi)<<32 | uint64(lo)
	if ft == 0 {
		return ntLinuxTimespec{}
	}
	const epochDelta = 116444736000000000 // 1601 -> 1970 in 100ns units
	t := int64(ft) - epochDelta
	sec := t / 1e7
	nsec := (t % 1e7) * 100
	if nsec < 0 {
		sec--
		nsec += 1e9
	}
	return ntLinuxTimespec{sec, nsec}
}

// ntStatFromInfo synthesizes a Linux stat from Win32 handle
// information. Decisions (documented once, here):
//   - dev/ino come from VolumeSerialNumber and the NTFS FileIndex, so
//     os.SameFile works across path spellings (/tmp vs /c/...).
//   - Modes are synthetic: directories 0755, files 0755 (0555 when
//     the READONLY attribute is set). Everything stays "executable"
//     because NT has no x bit and path-based lookups (exec.LookPath)
//     gate on 0111.
//   - ctime is filled from the CreationTime (NT has no status-change
//     time; BY_HANDLE_FILE_INFORMATION has no ChangeTime field).
func ntStatFromInfo(dst *ntLinuxStat, info *ntByHandleFileInformation) {
	*dst = ntLinuxStat{}
	dst.dev = uint64(info.VolumeSerialNumber)
	dst.ino = uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	nlink := info.NumberOfLinks
	if nlink == 0 {
		nlink = 1
	}
	dst.nlink = uint64(nlink)
	switch {
	case info.FileAttributes&_NT_FILE_ATTRIBUTE_DIRECTORY != 0:
		dst.mode = _NT_S_IFDIR | 0o755
	case info.FileAttributes&_NT_FILE_ATTRIBUTE_READONLY != 0:
		dst.mode = _NT_S_IFREG | 0o555
	default:
		dst.mode = _NT_S_IFREG | 0o755
	}
	if dst.mode&_NT_S_IFDIR == 0 {
		dst.size = int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow)
	}
	dst.blksize = 4096
	dst.blocks = (dst.size + 511) / 512
	dst.atim = ntFiletimeToTimespec(info.LastAccessTimeLo, info.LastAccessTimeHi)
	dst.mtim = ntFiletimeToTimespec(info.LastWriteTimeLo, info.LastWriteTimeHi)
	dst.ctim = ntFiletimeToTimespec(info.CreationTimeLo, info.CreationTimeHi)
}

// ntStatSynthDevice fills a stat for a non-disk handle (console,
// pipe, NUL).
func ntStatSynthDevice(dst *ntLinuxStat, ftype uint8) {
	*dst = ntLinuxStat{}
	if ftype == _NT_FILE_TYPE_PIPE {
		dst.mode = _NT_S_IFIFO | 0o600
	} else {
		dst.mode = _NT_S_IFCHR | 0o620
	}
	dst.nlink = 1
	dst.blksize = 4096
}

// ntFstatHandle stats an open handle: disk objects get real
// information, others a synthesized character-device stat.
func ntFstatHandle(h uintptr, dst *ntLinuxStat) uintptr {
	var info ntByHandleFileInformation
	r, werr := ntcallE(ntGetFileInformationByHandleFn, h, uintptr(unsafe.Pointer(&info)), 0, 0, 0, 0, 0)
	if r == 0 {
		ftype := uint8(ntcall(ntGetFileTypeFn, h, 0, 0, 0, 0, 0))
		if ftype == _NT_FILE_TYPE_CHAR || ftype == _NT_FILE_TYPE_PIPE {
			ntStatSynthDevice(dst, ftype)
			return 0
		}
		return ntErrno(werr)
	}
	ntStatFromInfo(dst, &info)
	return 0
}

// ntStatW opens w for attributes only and stats it.
func ntStatW(w []uint16, dst *ntLinuxStat) uintptr {
	h, werr := ntcallE(ntCreateFileWFn, uintptr(unsafe.Pointer(&w[0])), _NT_FILE_READ_ATTRIBUTES,
		_NT_FILE_SHARE_ALL, 0, _NT_OPEN_EXISTING, _NT_FILE_FLAG_BACKUP_SEMANTICS, 0)
	KeepAlive(w)
	if h == _NT_INVALID_HANDLE_VALUE {
		return ntErrno(werr)
	}
	eno := ntFstatHandle(h, dst)
	ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
	return eno
}

func ntEmuStat(cpath *byte, dst *ntLinuxStat) (r1, r2, errno uintptr) {
	if dst == nil {
		return ntFail3(ntEINVAL)
	}
	w := ntPathW(ntCPath(cpath))
	if w == nil {
		return ntFail3(ntENOENT)
	}
	if eno := ntStatW(w, dst); eno != 0 {
		return ntFail3(eno)
	}
	return 0, 0, 0
}

func ntEmuFstat(fd int32, dst *ntLinuxStat) (r1, r2, errno uintptr) {
	if dst == nil {
		return ntFail3(ntEINVAL)
	}
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if e.kind == ntFDStdio {
		ntStatSynthDevice(dst, e.ftype)
		return 0, 0, 0
	}
	if eno := ntFstatHandle(e.handle, dst); eno != 0 {
		return ntFail3(eno)
	}
	return 0, 0, 0
}

func ntEmuFstatat(dirfd int32, cpath *byte, dst *ntLinuxStat, flags int32) (r1, r2, errno uintptr) {
	if dst == nil {
		return ntFail3(ntEINVAL)
	}
	path := ntCPath(cpath)
	if flags&_NT_AT_EMPTY_PATH != 0 && path == "" {
		return ntEmuFstat(dirfd, dst)
	}
	// AT_SYMLINK_NOFOLLOW is accepted and ignored: no symlink support
	// this wave, lstat == stat.
	w, eno := ntAtPathW(dirfd, path)
	if eno != 0 {
		return ntFail3(eno)
	}
	if eno := ntStatW(w, dst); eno != 0 {
		return ntFail3(eno)
	}
	return 0, 0, 0
}

// ---- seek / pread / pwrite / truncate / sync ----

// ntSeekHandle wraps SetFilePointerEx; returns the new position or a
// Win32 error.
func ntSeekHandle(h uintptr, off int64, whence uintptr) (int64, uintptr) {
	var newpos int64
	r, werr := ntcallE(ntSetFilePointerExFn, h, uintptr(off),
		uintptr(unsafe.Pointer(&newpos)), whence, 0, 0, 0)
	if r == 0 {
		return 0, werr
	}
	return newpos, 0
}

func ntEmuLseek(fd int32, off int64, whence uintptr) (r1, r2, errno uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if e.kind == ntFDStdio {
		return ntFail3(ntESPIPE)
	}
	if whence > _NT_FILE_END {
		return ntFail3(ntEINVAL)
	}
	newpos, werr := ntSeekHandle(e.handle, off, whence)
	if werr != 0 {
		return ntFail3(ntErrno(werr))
	}
	return uintptr(newpos), 0, 0
}

// ntEmuPreadPwrite implements pread64/pwrite64 by seeking around the
// shared file pointer (save, seek, transfer, restore). Linux's
// pointer-untouched guarantee holds only against concurrent users of
// OTHER descriptors; concurrent plain reads on the SAME fd can
// observe the temporary seek. internal/poll only mixes them per-fd
// under its own locks, so this is sound for the standard library.
func ntEmuPreadPwrite(fd int32, p unsafe.Pointer, n int32, off int64, isWrite bool) (r1, r2, errno uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if e.kind != ntFDFile {
		return ntFail3(ntESPIPE)
	}
	if am := e.flags & _NT_O_ACCMODE; (isWrite && am == _NT_O_RDONLY) || (!isWrite && am == _NT_O_WRONLY) {
		return ntFail3(ntEBADF)
	}
	if n < 0 || off < 0 {
		return ntFail3(ntEINVAL)
	}
	if n == 0 {
		return 0, 0, 0
	}
	cur, werr := ntSeekHandle(e.handle, 0, _NT_FILE_CURRENT)
	if werr != 0 {
		return ntFail3(ntErrno(werr))
	}
	if _, werr = ntSeekHandle(e.handle, off, _NT_FILE_BEGIN); werr != 0 {
		return ntFail3(ntErrno(werr))
	}
	var moved uint32
	fn := ntReadFileFn
	if isWrite {
		fn = ntWriteFileFn
	}
	r, werr2 := ntcallSE(fn, e.handle, uintptr(p), uintptr(uint32(n)),
		uintptr(unsafe.Pointer(&moved)), 0, 0, 0)
	ntSeekHandle(e.handle, cur, _NT_FILE_BEGIN) // best-effort restore
	if r == 0 {
		if !isWrite && (werr2 == _NT_ERROR_BROKEN_PIPE || werr2 == _NT_ERROR_HANDLE_EOF) {
			return 0, 0, 0
		}
		return ntFail3(ntErrno(werr2))
	}
	return uintptr(moved), 0, 0
}

func ntEmuFtruncate(fd int32, length int64) (r1, r2, errno uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if e.kind != ntFDFile {
		return ntFail3(ntEINVAL)
	}
	if length < 0 {
		return ntFail3(ntEINVAL)
	}
	cur, werr := ntSeekHandle(e.handle, 0, _NT_FILE_CURRENT)
	if werr != 0 {
		return ntFail3(ntErrno(werr))
	}
	if _, werr = ntSeekHandle(e.handle, length, _NT_FILE_BEGIN); werr != 0 {
		return ntFail3(ntErrno(werr))
	}
	r, werr2 := ntcallE(ntSetEndOfFileFn, e.handle, 0, 0, 0, 0, 0, 0)
	ntSeekHandle(e.handle, cur, _NT_FILE_BEGIN) // Linux keeps the offset
	if r == 0 {
		return ntFail3(ntErrno(werr2))
	}
	return 0, 0, 0
}

func ntEmuFsync(fd int32) (r1, r2, errno uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	r, werr := ntcallSE(ntFlushFileBuffersFn, e.handle, 0, 0, 0, 0, 0, 0)
	if r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}

// ---- directory modification / access / chmod ----

func ntEmuMkdirat(dirfd int32, cpath *byte) (r1, r2, errno uintptr) {
	w, eno := ntAtPathW(dirfd, ntCPath(cpath))
	if eno != 0 {
		return ntFail3(eno)
	}
	// The Linux mode is ignored: NT directories carry no unix modes
	// (stat synthesizes 0755).
	r, werr := ntcallE(ntCreateDirectoryWFn, uintptr(unsafe.Pointer(&w[0])), 0, 0, 0, 0, 0, 0)
	KeepAlive(w)
	if r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}

func ntEmuUnlinkat(dirfd int32, cpath *byte, flags int32) (r1, r2, errno uintptr) {
	w, eno := ntAtPathW(dirfd, ntCPath(cpath))
	if eno != 0 {
		return ntFail3(eno)
	}
	fn := ntDeleteFileWFn
	if flags&_NT_AT_REMOVEDIR != 0 {
		fn = ntRemoveDirectoryWFn
	}
	r, werr := ntcallE(fn, uintptr(unsafe.Pointer(&w[0])), 0, 0, 0, 0, 0, 0)
	KeepAlive(w)
	if r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}

func ntEmuRenameat(olddirfd int32, oldp *byte, newdirfd int32, newp *byte) (r1, r2, errno uintptr) {
	wold, eno := ntAtPathW(olddirfd, ntCPath(oldp))
	if eno != 0 {
		return ntFail3(eno)
	}
	wnew, eno := ntAtPathW(newdirfd, ntCPath(newp))
	if eno != 0 {
		return ntFail3(eno)
	}
	r, werr := ntcallE(ntMoveFileExWFn, uintptr(unsafe.Pointer(&wold[0])),
		uintptr(unsafe.Pointer(&wnew[0])), _NT_MOVEFILE_REPLACE_EXISTING, 0, 0, 0, 0)
	KeepAlive(wold)
	KeepAlive(wnew)
	if r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}

func ntEmuFaccessat(dirfd int32, cpath *byte, mode uint32) (r1, r2, errno uintptr) {
	w, eno := ntAtPathW(dirfd, ntCPath(cpath))
	if eno != 0 {
		return ntFail3(eno)
	}
	attrs, werr := ntcallE(ntGetFileAttributesWFn, uintptr(unsafe.Pointer(&w[0])), 0, 0, 0, 0, 0, 0)
	KeepAlive(w)
	if uint32(attrs) == _NT_INVALID_FILE_ATTRIBUTES {
		return ntFail3(ntErrno(werr))
	}
	// W_OK (2) against a read-only file is the only refusable
	// combination; F_OK/R_OK/X_OK are satisfied by existence (modes
	// are synthetic 0755, see ntStatFromInfo).
	if mode&2 != 0 && uint32(attrs)&_NT_FILE_ATTRIBUTE_READONLY != 0 &&
		uint32(attrs)&_NT_FILE_ATTRIBUTE_DIRECTORY == 0 {
		return ntFail3(ntEACCES)
	}
	return 0, 0, 0
}

// chmod is accepted and discarded (after an existence/validity
// check): NT has no unix permission bits, and mapping mode&0200 onto
// the READONLY attribute would make later unlinks fail in surprising
// places. Documented no-op, matching stat's synthetic modes.
func ntEmuFchmod(fd int32) (r1, r2, errno uintptr) {
	if _, ok := ntFDLookup(fd); !ok {
		return ntFail3(ntEBADF)
	}
	return 0, 0, 0
}

func ntEmuFchmodat(dirfd int32, cpath *byte) (r1, r2, errno uintptr) {
	w, eno := ntAtPathW(dirfd, ntCPath(cpath))
	if eno != 0 {
		return ntFail3(eno)
	}
	attrs, werr := ntcallE(ntGetFileAttributesWFn, uintptr(unsafe.Pointer(&w[0])), 0, 0, 0, 0, 0, 0)
	KeepAlive(w)
	if uint32(attrs) == _NT_INVALID_FILE_ATTRIBUTES {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}

// ---- readlink (os.Executable) ----

// ntEmuReadlinkat supports exactly one link: /proc/self/exe, which
// os/executable_cosmo.go reads first on every host. It answers with
// GetModuleFileNameW in /c/-form, so os.Executable works without any
// os-package changes. Everything else is EINVAL - the Linux errno for
// "not a symlink" - because this wave has no symlink support.
func ntEmuReadlinkat(dirfd int32, cpath *byte, buf unsafe.Pointer, bufsiz uintptr) (r1, r2, errno uintptr) {
	path := ntCPath(cpath)
	if path != "/proc/self/exe" {
		return ntFail3(ntEINVAL)
	}
	if buf == nil || bufsiz == 0 {
		return ntFail3(ntEINVAL)
	}
	wbuf := make([]uint16, 4096)
	n := ntcall(ntGetModuleFileNameWFn, 0, uintptr(unsafe.Pointer(&wbuf[0])), uintptr(len(wbuf)), 0, 0, 0)
	if n == 0 || n >= uintptr(len(wbuf)) {
		return ntFail3(ntEIO)
	}
	// The OS reports the mapped module path; APE self-assimilation
	// does not apply on NT (the PE header maps directly), so this is
	// the real on-disk binary.
	s := ntPathToLinux(wbuf[:n])
	cnt := uintptr(len(s))
	if cnt > bufsiz {
		cnt = bufsiz // silent truncation, readlink(2) semantics
	}
	dst := unsafe.Slice((*byte)(buf), bufsiz)
	copy(dst[:cnt], s)
	return cnt, 0, 0
}

// ---- working directory ----

func ntEmuChdir(cpath *byte) (r1, r2, errno uintptr) {
	path := ntCPath(cpath)
	w := ntPathW(path)
	if w == nil {
		return ntFail3(ntENOENT)
	}
	r, werr := ntcallE(ntSetCurrentDirectoryWFn, uintptr(unsafe.Pointer(&w[0])), 0, 0, 0, 0, 0, 0)
	KeepAlive(w)
	if r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}

// ntEmuGetcwd implements the Linux getcwd syscall (returns byte count
// INCLUDING the trailing NUL) over GetCurrentDirectoryW, translated
// to the /c/-form so unix-shaped path code round-trips (see
// os_cosmo_nt_path.go).
func ntEmuGetcwd(buf unsafe.Pointer, size uintptr) (r1, r2, errno uintptr) {
	if buf == nil {
		return ntFail3(ntEINVAL)
	}
	var wbuf [4096]uint16
	n := ntcall(ntGetCurrentDirectoryWFn, uintptr(len(wbuf)), uintptr(unsafe.Pointer(&wbuf[0])), 0, 0, 0, 0)
	if n == 0 || n >= uintptr(len(wbuf)) {
		return ntFail3(ntEIO)
	}
	s := ntPathToLinux(wbuf[:n])
	if uintptr(len(s))+1 > size {
		return ntFail3(ntERANGE)
	}
	dst := unsafe.Slice((*byte)(buf), size)
	copy(dst, s)
	dst[len(s)] = 0
	return uintptr(len(s)) + 1, 0, 0
}

// ---- getdents64 ----

// FILE_ID_BOTH_DIR_INFO field offsets (x64): NextEntryOffset 0,
// FileAttributes 56, FileNameLength 60 (bytes), FileId 96, FileName
// 104. Verified against ntifs.h; records are 8-aligned.
const (
	ntFIBDNextOff  = 0
	ntFIBDAttrs    = 56
	ntFIBDNameLen  = 60
	ntFIBDFileId   = 96
	ntFIBDFileName = 104
)

// Linux dirent64 header: ino 0, off 8, reclen 16, type 18, name 19.
const ntLinuxDirentHdr = 19

// ntEmuGetdents emulates Linux getdents64 over
// GetFileInformationByHandleEx(FileIdBothDirectoryInfo), the same
// role Apple's __getdirentries64 plays in the darwin port. The
// directory HANDLE holds the kernel-side enumeration cursor
// (RestartInfo on the first query re-anchors it); entries that were
// returned by the kernel but do not fit the caller's buffer are
// parked in the fd's pending list so nothing is ever lost between
// calls.
func ntEmuGetdents(fd int32, buf unsafe.Pointer, count uintptr) (r1, r2, errno uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if e.kind != ntFDDir {
		return ntFail3(ntENOTDIR)
	}
	if buf == nil {
		return ntFail3(ntEINVAL)
	}
	pending := e.pending
	started := e.dirStarted
	out := uintptr(0)
	full := false
	for !full {
		for len(pending) > 0 {
			var ok bool
			out, ok = ntEmitDirent(buf, out, count, pending[0])
			if !ok {
				full = true
				break
			}
			pending = pending[1:]
		}
		if full {
			break
		}
		cls := uintptr(_NT_FileIdBothDirectoryInfo)
		if !started {
			cls = _NT_FileIdBothDirectoryRestartInfo
		}
		tmp := make([]byte, 8192)
		r, werr := ntcallE(ntGetFileInformationByHandleExFn, e.handle, cls,
			uintptr(unsafe.Pointer(&tmp[0])), uintptr(len(tmp)), 0, 0, 0)
		if r == 0 {
			if werr == _NT_ERROR_NO_MORE_FILES {
				break // end of directory
			}
			if out == 0 && len(pending) == 0 {
				ntFDSetDirState(fd, started, nil)
				return ntFail3(ntErrno(werr))
			}
			break // deliver what we have; the error resurfaces next call
		}
		started = true
		pending = ntParseDirInfo(tmp, pending)
	}
	if out == 0 && len(pending) > 0 {
		// The caller's buffer cannot hold even one record.
		ntFDSetDirState(fd, started, pending)
		return ntFail3(ntEINVAL)
	}
	if len(pending) == 0 {
		pending = nil
	}
	ntFDSetDirState(fd, started, pending)
	return out, 0, 0
}

// ntParseDirInfo appends every record in a FileIdBothDirectoryInfo
// batch to dst.
func ntParseDirInfo(tmp []byte, dst []ntDirEnt) []ntDirEnt {
	base := unsafe.Pointer(&tmp[0])
	off := uintptr(0)
	for {
		if off+ntFIBDFileName > uintptr(len(tmp)) {
			break
		}
		rec := unsafe.Add(base, off)
		next := *(*uint32)(unsafe.Add(rec, ntFIBDNextOff))
		attrs := *(*uint32)(unsafe.Add(rec, ntFIBDAttrs))
		nameLen := uintptr(*(*uint32)(unsafe.Add(rec, ntFIBDNameLen))) // bytes
		fileID := *(*uint64)(unsafe.Add(rec, ntFIBDFileId))
		if off+ntFIBDFileName+nameLen > uintptr(len(tmp)) {
			break // malformed; refuse to guess
		}
		name := ntUTF16ToString(unsafe.Slice((*uint16)(unsafe.Add(rec, ntFIBDFileName)), nameLen/2))
		typ := byte(_NT_DT_REG)
		if attrs&_NT_FILE_ATTRIBUTE_DIRECTORY != 0 {
			typ = _NT_DT_DIR
		}
		if fileID == 0 {
			fileID = 1 // syscall.ParseDirent skips ino==0 entries
		}
		dst = append(dst, ntDirEnt{ino: fileID, typ: typ, name: name})
		if next == 0 {
			break
		}
		off += uintptr(next)
	}
	return dst
}

// ntEmitDirent writes one Linux dirent64 record at buf+out; reports
// false when it does not fit.
func ntEmitDirent(buf unsafe.Pointer, out, count uintptr, de ntDirEnt) (uintptr, bool) {
	nl := uintptr(len(de.name))
	rec := (ntLinuxDirentHdr + nl + 1 + 7) &^ 7
	if out+rec > count {
		return out, false
	}
	p := unsafe.Add(buf, out)
	*(*uint64)(p) = de.ino
	*(*int64)(unsafe.Add(p, 8)) = 0 // d_off: opaque cookie, unused by callers
	*(*uint16)(unsafe.Add(p, 16)) = uint16(rec)
	*(*byte)(unsafe.Add(p, 18)) = de.typ
	dst := unsafe.Slice((*byte)(unsafe.Add(p, ntLinuxDirentHdr)), nl+1)
	copy(dst, de.name)
	dst[nl] = 0
	return out + rec, true
}

// ---- boot ----

// ntBootInit runs from osArchInit, right after ntResolve, still
// single-threaded and pre-mallocinit (nothing here allocates):
//   - upgrades the RDTSC-mixed boot AT_RANDOM bytes (rt0 fabricates
//     them; sysargs already pointed startupRand at them) to real
//     ProcessPrng entropy before randinit consumes them;
//   - seeds fd table slots 0/1/2 from the std handles - ALWAYS marked
//     open, even with a null handle (see os_cosmo_nt_fd.go);
//   - switches the console to UTF-8 (byte-exact output is a CI
//     requirement) and enables VT processing where stdout/stderr are
//     real consoles. All console calls are fire-and-forget: under
//     redirection (pipes, CI) they fail harmlessly.
func ntBootInit() {
	if ntProcessPrngFn != 0 && len(startupRand) >= 16 {
		ntcall(ntProcessPrngFn, uintptr(unsafe.Pointer(&startupRand[0])), uintptr(len(startupRand)), 0, 0, 0, 0)
	}

	stdio := [3]uintptr{ntStdin, ntStdout, ntStderr}
	for fd, h := range stdio {
		flags := int32(_NT_O_WRONLY)
		if fd == 0 {
			flags = _NT_O_RDONLY
		}
		kind := ntFDStdio
		ftype := uint8(0)
		if h != 0 && h != _NT_INVALID_HANDLE_VALUE {
			ftype = uint8(ntcall(ntGetFileTypeFn, h, 0, 0, 0, 0, 0))
			if ftype == _NT_FILE_TYPE_DISK {
				kind = ntFDFile // redirected to a file: seekable, statable
			}
		}
		// Direct writes: osinit is single-threaded, no lock needed.
		ntFDTable[fd] = ntFDEntry{handle: h, kind: kind, ftype: ftype, flags: flags}
	}

	ntcall(ntSetConsoleOutputCPFn, _NT_CP_UTF8, 0, 0, 0, 0, 0)
	ntcall(ntSetConsoleCPFn, _NT_CP_UTF8, 0, 0, 0, 0, 0)
	for _, h := range [2]uintptr{ntStdout, ntStderr} {
		if h == 0 || h == _NT_INVALID_HANDLE_VALUE {
			continue
		}
		var mode uint32
		if ntcall(ntGetConsoleModeFn, h, uintptr(unsafe.Pointer(&mode)), 0, 0, 0, 0) != 0 {
			ntcall(ntSetConsoleModeFn, h, uintptr(mode)|_NT_ENABLE_VIRTUAL_TERMINAL_PROCESSING, 0, 0, 0, 0)
		}
	}
}
