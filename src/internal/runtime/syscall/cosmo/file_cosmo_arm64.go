// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

import "unsafe"

// File and metadata syscalls emulated on macOS with dlsym-resolved Apple
// libc entries. Everything here runs on the nosplit dispatch spine, so a
// handler keeps its frame small and never allocates. See
// syscall_cosmo_arm64.go for the darwinCall conventions.

// Apple AT_* flags that the file layer translates. AT_FDCWD,
// AT_SYMLINK_NOFOLLOW and AT_REMOVEDIR live in syscall_cosmo_arm64.go.
const appleAT_SYMLINK_FOLLOW = 0x40

// Linux AT_SYMLINK_FOLLOW, the one linkat flag with an Apple counterpart.
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
// directory-relative form. A dirfd other than AT_FDCWD therefore fails
// with ENOSYS rather than silently resolving the path against the
// process working directory. syscall.Mknod and syscall.Mkfifo both pass
// AT_FDCWD, so the common paths work.
//
//go:nosplit
func darwinMknodat(dirfd, path, mode, dev uintptr) (r1, r2, errno uintptr) {
	if int32(dirfd) != linuxAT_FDCWD {
		return ^uintptr(0), 0, darwinENOSYS
	}
	// S_IFMT file-type bits and the permission bits share their values.
	// dev keeps Apple's device encoding, like Stat_t.Rdev does.
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
// Three things differ. Apple takes the FILE first and the SOCKET second,
// the reverse of Linux. Apple reports the transferred count through a
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
	var off int64
	if offptr != 0 {
		off = *(*int64)(unsafe.Pointer(offptr))
	} else {
		r, _, e := darwinCall(darwinFns.Lseek, infd, 0, seekCUR, 0, 0, 0)
		if e != 0 {
			return ^uintptr(0), 0, e
		}
		off = int64(r)
	}
	n := int64(count)
	r := darwinLibcCall6(darwinFns.Sendfile, infd, outfd, uintptr(off),
		uintptr(unsafe.Pointer(&n)), 0, 0)
	// errno belongs to the call that just failed, so read it before the
	// offset fixup below issues an lseek.
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
	// Bytes moved: report them as Linux does and let the caller meet the
	// error on its next call.
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
// The buffer therefore belongs to the syscall package, which allocates
// the APPLE-layout struct and converts it (see syscall/bigbuf_cosmo.go).
// The size argument is what makes that contract checkable: a caller that
// passed a Linux-layout buffer by mistake gets EINVAL instead of a
// two-kilobyte write into a 120-byte struct.
//
//go:nosplit
func darwinStatfs(fn, pathOrFd, buf, size uintptr) (r1, r2, errno uintptr) {
	if buf == 0 || size < darwinStatfsSize {
		return ^uintptr(0), 0, darwinEINVAL
	}
	return darwinCall(fn, pathOrFd, buf, 0, 0, 0, 0)
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
