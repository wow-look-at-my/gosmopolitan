// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "unsafe"

// System administration calls. GOOS=cosmo presents the Linux ABI, so a
// program written against the linux port names these. The linux port
// declares them in syscall_linux.go and zsyscall_linux_amd64.go, which
// cosmo does not build. The signatures and the bodies are those files',
// unchanged.
//
// A Linux kernel is the only host that serves them. The macOS emulation
// (internal/runtime/syscall/cosmo.syscall6SlowDarwin) and the Windows
// emulation (runtime.ntSyscallEmulate) have no case for these syscall
// numbers, so both answer ENOSYS.

// The magic values the kernel demands from reboot(2). The linux port names
// them LINUX_REBOOT_MAGIC1 and LINUX_REBOOT_MAGIC2 in zerrors_linux_amd64.go
// and zerrors_linux_arm64.go, which cosmo does not build.
const (
	linuxRebootMagic1 = 0xfee1dead
	linuxRebootMagic2 = 0x28121969
)

func reboot(magic1 uint, magic2 uint, cmd int, arg string) (err error) {
	var _p0 *byte
	_p0, err = BytePtrFromString(arg)
	if err != nil {
		return
	}
	_, _, e1 := Syscall6(SYS_REBOOT, uintptr(magic1), uintptr(magic2), uintptr(cmd), uintptr(unsafe.Pointer(_p0)), 0, 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Reboot(cmd int) (err error) {
	return reboot(linuxRebootMagic1, linuxRebootMagic2, cmd, "")
}

func mount(source string, target string, fstype string, flags uintptr, data *byte) (err error) {
	var _p0 *byte
	_p0, err = BytePtrFromString(source)
	if err != nil {
		return
	}
	var _p1 *byte
	_p1, err = BytePtrFromString(target)
	if err != nil {
		return
	}
	var _p2 *byte
	_p2, err = BytePtrFromString(fstype)
	if err != nil {
		return
	}
	_, _, e1 := Syscall6(SYS_MOUNT, uintptr(unsafe.Pointer(_p0)), uintptr(unsafe.Pointer(_p1)), uintptr(unsafe.Pointer(_p2)), uintptr(flags), uintptr(unsafe.Pointer(data)), 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Mount(source string, target string, fstype string, flags uintptr, data string) (err error) {
	// Certain file systems get rather angry and EINVAL if you give
	// them an empty string of data, rather than NULL.
	if data == "" {
		return mount(source, target, fstype, flags, nil)
	}
	datap, err := BytePtrFromString(data)
	if err != nil {
		return err
	}
	return mount(source, target, fstype, flags, datap)
}

func Unmount(target string, flags int) (err error) {
	var _p0 *byte
	_p0, err = BytePtrFromString(target)
	if err != nil {
		return
	}
	_, _, e1 := Syscall(SYS_UMOUNT2, uintptr(unsafe.Pointer(_p0)), uintptr(flags), 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func PivotRoot(newroot string, putold string) (err error) {
	var _p0 *byte
	_p0, err = BytePtrFromString(newroot)
	if err != nil {
		return
	}
	var _p1 *byte
	_p1, err = BytePtrFromString(putold)
	if err != nil {
		return
	}
	_, _, e1 := Syscall(SYS_PIVOT_ROOT, uintptr(unsafe.Pointer(_p0)), uintptr(unsafe.Pointer(_p1)), 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Acct(path string) (err error) {
	var _p0 *byte
	_p0, err = BytePtrFromString(path)
	if err != nil {
		return
	}
	_, _, e1 := Syscall(SYS_ACCT, uintptr(unsafe.Pointer(_p0)), 0, 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

// Klogctl reads and controls the kernel message ring. The kernel calls this
// syscall syslog(2); the linux port builds Klogctl on SYS_SYSLOG.
func Klogctl(typ int, buf []byte) (n int, err error) {
	var _p0 unsafe.Pointer
	if len(buf) > 0 {
		_p0 = unsafe.Pointer(&buf[0])
	} else {
		_p0 = unsafe.Pointer(&_zero)
	}
	r0, _, e1 := Syscall(SYS_SYSLOG, uintptr(typ), uintptr(_p0), uintptr(len(buf)))
	n = int(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Setdomainname(p []byte) (err error) {
	var _p0 unsafe.Pointer
	if len(p) > 0 {
		_p0 = unsafe.Pointer(&p[0])
	} else {
		_p0 = unsafe.Pointer(&_zero)
	}
	_, _, e1 := Syscall(SYS_SETDOMAINNAME, uintptr(_p0), uintptr(len(p)), 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func Sethostname(p []byte) (err error) {
	var _p0 unsafe.Pointer
	if len(p) > 0 {
		_p0 = unsafe.Pointer(&p[0])
	} else {
		_p0 = unsafe.Pointer(&_zero)
	}
	_, _, e1 := Syscall(SYS_SETHOSTNAME, uintptr(_p0), uintptr(len(p)), 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

// setresIgnore is the -1 sentinel that tells setresgid(2) and setresuid(2)
// to leave that identity unchanged.
const setresIgnore = ^uintptr(0)

// Setegid sets the effective group ID. There is no setegid syscall, so the
// linux port issues setresgid(2) and changes the effective ID alone. It
// applies the change to every thread; cosmo has no all-threads mechanism, so
// this changes the calling thread only. Setgid and Setuid on cosmo carry the
// same limit.
func Setegid(egid int) (err error) {
	if _, _, e1 := RawSyscall(SYS_SETRESGID, setresIgnore, uintptr(egid), setresIgnore); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

// Seteuid sets the effective user ID. See Setegid for the mechanism and for
// the single-thread limit.
func Seteuid(euid int) (err error) {
	if _, _, e1 := RawSyscall(SYS_SETRESUID, setresIgnore, uintptr(euid), setresIgnore); e1 != 0 {
		err = errnoErr(e1)
	}
	return
}
