// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "internal/runtime/syscall/cosmo"

// statfs, fstatfs and uname each fill a struct whose shape belongs to
// the host. Apple's are far bigger than the Linux ones this package
// exposes - struct statfs is 2168 bytes against 120, and struct utsname
// 1280 against 390 - and too big to build inside the nosplit syscall
// emulation. So the Apple-layout buffer is allocated here, where
// allocation is legal, and converted on the way back. A Linux host takes
// the generated wrapper unchanged.

func Statfs(path string, buf *Statfs_t) (err error) {
	if cosmo.Darwin() {
		return darwinStatfsPath(path, buf)
	}
	return statfs(path, buf)
}

func Fstatfs(fd int, buf *Statfs_t) (err error) {
	if cosmo.Darwin() {
		return darwinStatfsFd(fd, buf)
	}
	return fstatfs(fd, buf)
}

func Uname(buf *Utsname) (err error) {
	if cosmo.Darwin() {
		return darwinUname(buf)
	}
	return uname(buf)
}

// Apple mount flags (sys/mount.h) and the Linux statfs f_flags bits
// (ST_*) that mean the same thing. Apple has many more; only the ones
// with a Linux counterpart are reported.
const (
	appleMNT_RDONLY      = 0x1
	appleMNT_SYNCHRONOUS = 0x2
	appleMNT_NOEXEC      = 0x4
	appleMNT_NOSUID      = 0x8
	appleMNT_NODEV       = 0x10
	appleMNT_NOATIME     = 0x10000000

	linuxST_RDONLY      = 0x1
	linuxST_NOSUID      = 0x2
	linuxST_NODEV       = 0x4
	linuxST_NOEXEC      = 0x8
	linuxST_SYNCHRONOUS = 0x10
	linuxST_NOATIME     = 0x400
)

func darwinMntFlagsToLinux(f uint32) int64 {
	var out int64
	if f&appleMNT_RDONLY != 0 {
		out |= linuxST_RDONLY
	}
	if f&appleMNT_NOSUID != 0 {
		out |= linuxST_NOSUID
	}
	if f&appleMNT_NODEV != 0 {
		out |= linuxST_NODEV
	}
	if f&appleMNT_NOEXEC != 0 {
		out |= linuxST_NOEXEC
	}
	if f&appleMNT_SYNCHRONOUS != 0 {
		out |= linuxST_SYNCHRONOUS
	}
	if f&appleMNT_NOATIME != 0 {
		out |= linuxST_NOATIME
	}
	return out
}

// darwinStatfsToLinux fills a Linux-layout Statfs_t from an Apple one.
//
// Two Linux fields have no Apple source. Type keeps Apple's own
// filesystem-type number rather than a Linux magic, the same choice
// Stat_t.Dev makes for device numbers. Namelen stays zero: Apple's
// statfs has no maximum-name-length field, and a guessed 255 would be a
// number this code never measured.
func darwinStatfsToLinux(dst *Statfs_t, src *cosmo.DarwinStatfs) {
	*dst = Statfs_t{}
	dst.Type = int64(src.Type)
	// Apple's f_bsize is the filesystem's fundamental block size, which
	// is what Linux reports in both Bsize and Frsize. Its f_iosize (the
	// optimal transfer size) has no Linux statfs counterpart.
	dst.Bsize = int64(src.Bsize)
	dst.Frsize = int64(src.Bsize)
	dst.Blocks = src.Blocks
	dst.Bfree = src.Bfree
	dst.Bavail = src.Bavail
	dst.Files = src.Files
	dst.Ffree = src.Ffree
	dst.Fsid = Fsid{X__val: src.Fsid}
	dst.Flags = darwinMntFlagsToLinux(src.Flags)
}

// darwinUtsField copies one NUL-terminated Apple utsname field into a
// Linux one. Apple gives each field 256 bytes and Linux 65, so a longer
// value is truncated with its terminator kept.
func darwinUtsField(dst *[65]byte, src []byte) {
	n := 0
	for n < len(src) && src[n] != 0 && n < len(dst)-1 {
		dst[n] = src[n]
		n++
	}
	dst[n] = 0
}

// darwinUtsnameToLinux fills a Linux Utsname from Apple's. Domainname
// stays empty: Apple's utsname has no such field, and the value a Linux
// kernel puts there is not one this code can obtain.
func darwinUtsnameToLinux(dst *Utsname, src *cosmo.DarwinUtsname) {
	*dst = Utsname{}
	darwinUtsField(&dst.Sysname, src.Sysname[:])
	darwinUtsField(&dst.Nodename, src.Nodename[:])
	darwinUtsField(&dst.Release, src.Release[:])
	darwinUtsField(&dst.Version, src.Version[:])
	darwinUtsField(&dst.Machine, src.Machine[:])
}
