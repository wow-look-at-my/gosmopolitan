// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

import "unsafe"

// File and metadata syscalls emulated on macOS with dlsym-resolved Apple libc entries.

// Apple AT_* flags that the file layer translates.
const appleAT_SYMLINK_FOLLOW = 0x40

// Linux AT_SYMLINK_FOLLOW, the linkat flag with an Apple counterpart.
const linuxAT_SYMLINK_FOLLOW = 0x400

// whence values for lseek, identical on both systems.
const (
	seekSET = 0
	seekCUR = 1
)

//go:nosplit
func darwinLinkat(olddirfd, oldpath, newdirfd, newpath, flags uintptr) (r1, r2, errno uintptr) {
	if flags&^uintptr(linuxAT_SYMLINK_FOLLOW) != 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	aflags := uintptr(0)
	if flags&linuxAT_SYMLINK_FOLLOW != 0 {
		aflags = appleAT_SYMLINK_FOLLOW
	}
	return darwinCall(darwinFns.Linkat, darwinXlatDirfd(olddirfd), oldpath,
		darwinXlatDirfd(newdirfd), newpath, aflags, 0)
}

//go:nosplit
func darwinFchownat(dirfd, path, uid, gid, flags uintptr) (r1, r2, errno uintptr) {
	if flags&^uintptr(linuxAT_SYMLINK_NOFOLLOW) != 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	aflags := uintptr(0)
	if flags&linuxAT_SYMLINK_NOFOLLOW != 0 {
		aflags = appleAT_SYMLINK_NOFOLLOW
	}
	return darwinCall(darwinFns.Fchownat, darwinXlatDirfd(dirfd), path, uid, gid, aflags, 0)
}

// darwinMknodat emulates mknodat with Apple's mknod, which has no
// directory-relative form.
//
//go:nosplit
func darwinMknodat(dirfd, path, mode, dev uintptr) (r1, r2, errno uintptr) {
	if int32(dirfd) != linuxAT_FDCWD {
		return ^uintptr(0), 0, darwinENOSYS
	}
	// S_IFMT file-type bits and the permission bits share their values.
	return darwinCall(darwinFns.Mknod, path, mode, dev, 0, 0, 0)
}

//go:nosplit
func darwinUtimensat(dirfd, path, times, flags uintptr) (r1, r2, errno uintptr) {
	if flags&^uintptr(linuxAT_SYMLINK_NOFOLLOW) != 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	aflags := uintptr(0)
	if flags&linuxAT_SYMLINK_NOFOLLOW != 0 {
		aflags = appleAT_SYMLINK_NOFOLLOW
	}
	if times == 0 {
		// A nil times array sets both stamps to now on both systems.
		return darwinCall(darwinFns.Utimensat, darwinXlatDirfd(dirfd), path, 0, aflags, 0, 0)
	}
	src := (*[2]linuxTimespec)(unsafe.Pointer(times))
	var ats [2]appleTimespec
	for i := 0; i < 2; i++ {
		ats[i].Sec = src[i].Sec
		ats[i].Nsec = darwinXlatUtimeNsec(src[i].Nsec)
	}
	return darwinCall(darwinFns.Utimensat, darwinXlatDirfd(dirfd), path,
		uintptr(unsafe.Pointer(&ats[0])), aflags, 0, 0)
}

// darwinSendfile emulates the Linux sendfile syscall.
//
// Things differ. Apple takes the FILE first and the SOCKET second, the
// reverse of Linux. Apple reports the transferred count through a
// value-result pointer instead of the return value, and it fills that
// count in even when the call fails - a short transfer that stopped on
// EAGAIN still moved bytes. And Apple never moves the file offset, so a
// Linux caller that passed no offset (meaning "start where the file is
// and advance it") needs the offset read and written back here.
//
//go:nosplit
func darwinSendfile(outfd, infd, offptr, count uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Sendfile == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	// Apple reads a zero count as "send to EOF". Linux sends nothing.
	if count == 0 {
		return 0, 0, 0
	}
	var off int64
	if offptr != 0 {
		off = *(*int64)(unsafe.Pointer(offptr))
	} else {
		r, _, e := darwinCall(darwinFns.Lseek, infd, 0, seekCUR, 0, 0, 0)
		if e != 0 {
			// A pipe or a socket has no offset to read, so it is a file type sendfile cannot serve.
			return ^uintptr(0), 0, darwinEINVAL
		}
		off = int64(r)
	}
	n := int64(count)
	r := darwinLibcCall6(darwinFns.Sendfile, infd, outfd, uintptr(off),
		uintptr(unsafe.Pointer(&n)), 0, 0)
	// errno belongs to the call that failed, so read it before the offset fixup below issues an lseek.
	var e uintptr
	if int64(r) == -1 {
		e = darwinErrno()
	}
	if n < 0 || n > int64(count) {
		return ^uintptr(0), 0, darwinEIO
	}
	if n > 0 {
		if offptr != 0 {
			*(*int64)(unsafe.Pointer(offptr)) = off + n
		} else if _, _, se := darwinCall(darwinFns.Lseek, infd, uintptr(off+n), seekSET, 0, 0, 0); se != 0 {
			return ^uintptr(0), 0, se
		}
	}
	if e != 0 && n == 0 {
		return ^uintptr(0), 0, e
	}
	// Bytes moved: report them as Linux does and let the caller meet the error on its next call.
	return uintptr(n), 0, 0
}

// The Apple structs themselves are in darwinabi_cosmo.go.
const (
	darwinStatfsSize  = unsafe.Sizeof(DarwinStatfs{})
	darwinUtsnameSize = unsafe.Sizeof(DarwinUtsname{})
)

// darwinStatfs emulates statfs/fstatfs, whose out-parameter is far too
// large to build on the nosplit dispatch spine.
//
//go:nosplit
func darwinStatfs(fn, pathOrFd, buf, size uintptr) (r1, r2, errno uintptr) {
	if buf == 0 || size < darwinStatfsSize {
		return ^uintptr(0), 0, darwinEINVAL
	}
	return darwinCall(fn, pathOrFd, buf, 0, 0, 0, 0)
}

// darwinFdatasync emulates fdatasync.
//
//go:nosplit
func darwinFdatasync(fd uintptr) (r1, r2, errno uintptr) {
	fn := darwinFns.Fdatasync
	if fn == 0 {
		fn = darwinFns.Fsync
	}
	return darwinCall(fn, fd, 0, 0, 0, 0, 0)
}

// darwinSync emulates sync.
//
//go:nosplit
func darwinSync() (r1, r2, errno uintptr) {
	if darwinFns.Sync == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	darwinLibcCall6(darwinFns.Sync, 0, 0, 0, 0, 0, 0)
	return 0, 0, 0
}

// darwinIoctl emulates ioctl for the requests whose argument means the same
// on both systems: both window-size calls, where struct winsize is uint16s
// either way, and those job-control calls, whose argument is an int or
// nothing.
//
//go:nosplit
func darwinIoctl(fd, req, arg uintptr) (r1, r2, errno uintptr) {
	if areq, ok := DarwinXlatIoctl(req); ok {
		return darwinCallVariadic1(darwinFns.Ioctl, fd, areq, arg)
	}
	if _, ok := darwinXlatTermiosIoctl(req); ok {
		return darwinTermiosIoctl(fd, req, arg)
	}
	return ^uintptr(0), 0, darwinENOSYS
}

// darwinTermiosIoctl serves TCGETS and the TCSETS forms over
// Apple's TIOCGETA/TIOCSETA family, converting the struct in both
// directions (termios_cosmo.go).
//
// A set is a read-modify-write, never a plain write: Apple's termios
// carries settings a Linux caller cannot name, and writing only what the
// caller passed would clear them. The read also fails first, with the
// right errno, when the descriptor is not a terminal.
//
// Nosplit, and so is everything it calls: the spine reaches this after
// entersyscall, where a stack growth is fatal.
//
//go:nosplit
func darwinTermiosIoctl(fd, req, arg uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Ioctl == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if arg == 0 {
		return ^uintptr(0), 0, darwinEFAULT
	}
	areq, ok := darwinXlatTermiosIoctl(req)
	if !ok {
		return ^uintptr(0), 0, darwinENOSYS
	}

	var at DarwinTermios
	if _, _, e := darwinCallVariadic1(darwinFns.Ioctl, fd, appleTIOCGETA,
		uintptr(unsafe.Pointer(&at))); e != 0 {
		return ^uintptr(0), 0, e
	}
	if req == linuxTCGETS {
		if !DarwinTermiosToLinux(&at, (*LinuxTermios)(unsafe.Pointer(arg))) {
			// The terminal reports a line speed with no Linux code. A wrong speed would be worse than a refused call.
			return ^uintptr(0), 0, darwinEINVAL
		}
		return 0, 0, 0
	}
	if !DarwinTermiosFromLinux((*LinuxTermios)(unsafe.Pointer(arg)), &at) {
		return ^uintptr(0), 0, darwinEINVAL
	}
	return darwinCallVariadic1(darwinFns.Ioctl, fd, areq, uintptr(unsafe.Pointer(&at)))
}

// darwinUname emulates uname. Same caller-owned-buffer contract as
// darwinStatfs, with the buffer as the only libc argument.
//
//go:nosplit
func darwinUname(buf, size uintptr) (r1, r2, errno uintptr) {
	if buf == 0 || size < darwinUtsnameSize {
		return ^uintptr(0), 0, darwinEINVAL
	}
	return darwinCall(darwinFns.Uname, buf, 0, 0, 0, 0, 0)
}
