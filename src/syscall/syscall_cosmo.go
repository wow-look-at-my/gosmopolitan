// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Cosmopolitan system calls.
// This file is compiled as ordinary Go code,
// but it is also input to mksyscall,
// which parses the //sys lines and generates system call stubs.

//go:build cosmo

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// Pull in entersyscall/exitsyscall for Syscall/Syscall6.

//go:linkname runtime_entersyscall runtime.entersyscall
func runtime_entersyscall()

//go:linkname runtime_exitsyscall runtime.exitsyscall
func runtime_exitsyscall()

//go:uintptrkeepalive
//go:nosplit
//go:norace
//go:linkname RawSyscall
func RawSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	return RawSyscall6(trap, a1, a2, a3, 0, 0, 0)
}

//go:uintptrkeepalive
//go:nosplit
//go:norace
//go:linkname RawSyscall6
func RawSyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	var errno uintptr
	r1, r2, errno = cosmo.Syscall6(trap, a1, a2, a3, a4, a5, a6)
	err = Errno(errno)
	return
}

//go:uintptrkeepalive
//go:nosplit
//go:linkname Syscall
func Syscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	runtime_entersyscall()
	r1, r2, err = RawSyscall6(trap, a1, a2, a3, 0, 0, 0)
	runtime_exitsyscall()
	return
}

//go:uintptrkeepalive
//go:nosplit
//go:linkname Syscall6
func Syscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	runtime_entersyscall()
	r1, r2, err = RawSyscall6(trap, a1, a2, a3, a4, a5, a6)
	runtime_exitsyscall()
	return
}

func rawSyscallNoError(trap, a1, a2, a3 uintptr) (r1, r2 uintptr)
func rawVforkSyscall(trap, a1, a2, a3 uintptr) (r1 uintptr, err Errno)

/*
 * Wrapped
 */

func Access(path string, mode uint32) (err error) {
	return Faccessat(_AT_FDCWD, path, mode, 0)
}

func Chmod(path string, mode uint32) (err error) {
	return Fchmodat(_AT_FDCWD, path, mode, 0)
}

func Chown(path string, uid int, gid int) (err error) {
	return Fchownat(_AT_FDCWD, path, uid, gid, 0)
}

func Creat(path string, mode uint32) (fd int, err error) {
	return Open(path, O_CREAT|O_WRONLY|O_TRUNC, mode)
}

//sys	faccessat(dirfd int, path string, mode uint32) (err error)

func Faccessat(dirfd int, path string, mode uint32, flags int) (err error) {
	if flags == 0 {
		return faccessat(dirfd, path, mode)
	}
	// Fall back to faccessat without flags
	return faccessat(dirfd, path, mode)
}

//sys	fchmodat(dirfd int, path string, mode uint32) (err error)

func Fchmodat(dirfd int, path string, mode uint32, flags int) error {
	return fchmodat(dirfd, path, mode)
}

//sys	linkat(olddirfd int, oldpath string, newdirfd int, newpath string, flags int) (err error)

func Link(oldpath string, newpath string) (err error) {
	return linkat(_AT_FDCWD, oldpath, _AT_FDCWD, newpath, 0)
}

func Mkdir(path string, mode uint32) (err error) {
	return Mkdirat(_AT_FDCWD, path, mode)
}

func Mknod(path string, mode uint32, dev int) (err error) {
	return Mknodat(_AT_FDCWD, path, mode, dev)
}

func Open(path string, mode int, perm uint32) (fd int, err error) {
	return openat(_AT_FDCWD, path, mode|O_LARGEFILE, perm)
}

//sys	openat(dirfd int, path string, flags int, mode uint32) (fd int, err error)

func Openat(dirfd int, path string, flags int, mode uint32) (fd int, err error) {
	return openat(dirfd, path, flags|O_LARGEFILE, mode)
}

func Pipe(p []int) error {
	return Pipe2(p, 0)
}

//sysnb pipe2(p *[2]_C_int, flags int) (err error)

func Pipe2(p []int, flags int) error {
	if len(p) != 2 {
		return EINVAL
	}
	var pp [2]_C_int
	err := pipe2(&pp, flags)
	if err == nil {
		p[0] = int(pp[0])
		p[1] = int(pp[1])
	}
	return err
}

//sys	readlinkat(dirfd int, path string, buf []byte) (n int, err error)

func Readlink(path string, buf []byte) (n int, err error) {
	return readlinkat(_AT_FDCWD, path, buf)
}

func Rename(oldpath string, newpath string) (err error) {
	return Renameat(_AT_FDCWD, oldpath, _AT_FDCWD, newpath)
}

func Rmdir(path string) error {
	return unlinkat(_AT_FDCWD, path, _AT_REMOVEDIR)
}

//sys	symlinkat(oldpath string, newdirfd int, newpath string) (err error)

func Symlink(oldpath string, newpath string) (err error) {
	return symlinkat(oldpath, _AT_FDCWD, newpath)
}

func Unlink(path string) error {
	return unlinkat(_AT_FDCWD, path, 0)
}

//sys	unlinkat(dirfd int, path string, flags int) (err error)

func Unlinkat(dirfd int, path string) error {
	return unlinkat(dirfd, path, 0)
}

func Utimes(path string, tv []Timeval) (err error) {
	if len(tv) != 2 {
		return EINVAL
	}
	return utimes(path, (*[2]Timeval)(unsafe.Pointer(&tv[0])))
}

//sys	utimensat(dirfd int, path string, times *[2]Timespec, flag int) (err error)

func UtimesNano(path string, ts []Timespec) (err error) {
	if len(ts) != 2 {
		return EINVAL
	}
	return utimensat(_AT_FDCWD, path, (*[2]Timespec)(unsafe.Pointer(&ts[0])), 0)
}

const ImplementsGetwd = true

//sys	Getcwd(buf []byte) (n int, err error)

func Getwd() (wd string, err error) {
	var buf [PathMax]byte
	n, err := Getcwd(buf[0:])
	if err != nil {
		return "", err
	}
	if n < 1 || n > len(buf) || buf[n-1] != 0 {
		return "", EINVAL
	}
	if buf[0] != '/' {
		return "", ENOENT
	}
	return string(buf[0 : n-1]), nil
}

func Getgroups() (gids []int, err error) {
	n, err := getgroups(0, nil)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}

	if n < 0 || n > 1<<20 {
		return nil, EINVAL
	}
	a := make([]_Gid_t, n)
	n, err = getgroups(n, &a[0])
	if err != nil {
		return nil, err
	}
	gids = make([]int, n)
	for i, v := range a[0:n] {
		gids[i] = int(v)
	}
	return
}

func Setgroups(gids []int) (err error) {
	if len(gids) == 0 {
		return setgroups(0, nil)
	}

	a := make([]_Gid_t, len(gids))
	for i, v := range gids {
		a[i] = _Gid_t(v)
	}
	return setgroups(len(a), &a[0])
}

type WaitStatus uint32

func (w WaitStatus) Exited() bool { return w&0x7f == 0 }

func (w WaitStatus) ExitStatus() int {
	if !w.Exited() {
		return -1
	}
	return int(w>>8) & 0xff
}

func (w WaitStatus) Signaled() bool { return w&0x7f != 0x7f && w&0x7f != 0 }

func (w WaitStatus) Signal() Signal {
	if !w.Signaled() {
		return -1
	}
	return Signal(w & 0x7f)
}

func (w WaitStatus) CoreDump() bool { return w.Signaled() && w&0x80 == 0x80 }

func (w WaitStatus) Stopped() bool { return w&0xff == 0x7f }

func (w WaitStatus) Continued() bool { return w == 0xffff }

func (w WaitStatus) StopSignal() Signal {
	if !w.Stopped() {
		return -1
	}
	return Signal(w>>8) & 0xff
}

func (w WaitStatus) TrapCause() int {
	if w.StopSignal() != SIGTRAP {
		return -1
	}
	return int(w>>8) >> 8
}

//sys	wait4(pid int, wstatus *_C_int, options int, rusage *Rusage) (wpid int, err error)

func Wait4(pid int, wstatus *WaitStatus, options int, rusage *Rusage) (wpid int, err error) {
	var status _C_int
	wpid, err = wait4(pid, &status, options, rusage)
	if wstatus != nil {
		*wstatus = WaitStatus(status)
	}
	return
}

//sys	Mkdirat(dirfd int, path string, mode uint32) (err error)
//sys	Mknodat(dirfd int, path string, mode uint32, dev int) (err error)
//sys	Fchownat(dirfd int, path string, uid int, gid int, flags int) (err error)
//sys	Renameat(olddirfd int, oldpath string, newdirfd int, newpath string) (err error)

//sys	sendfile(outfd int, infd int, offset *int64, count int) (written int, err error)
//sys	Fstatfs(fd int, buf *Statfs_t) (err error)
//sys	Statfs(path string, buf *Statfs_t) (err error)

// Constants
const (
	_AT_FDCWD            = -0x64
	_AT_REMOVEDIR        = 0x200
	_AT_SYMLINK_NOFOLLOW = 0x100
	_AT_EACCESS          = 0x200
)

//sys	fstat(fd int, stat *Stat_t) (err error)
//sys	fstatat(dirfd int, path string, stat *Stat_t, flags int) (err error)
//sys	Lstat(path string, stat *Stat_t) (err error)
//sys	Stat(path string, stat *Stat_t) (err error)

func Fstat(fd int, stat *Stat_t) (err error) {
	return fstat(fd, stat)
}

func Lchown(path string, uid int, gid int) (err error) {
	return Fchownat(_AT_FDCWD, path, uid, gid, _AT_SYMLINK_NOFOLLOW)
}

//sys	getgroups(n int, list *_Gid_t) (nn int, err error)
//sys	setgroups(n int, list *_Gid_t) (err error)
//sys	utimes(path string, times *[2]Timeval) (err error)
//sys	futimesat(dirfd int, path string, times *[2]Timeval) (err error)

func Gettimeofday(tv *Timeval) (err error) {
	return gettimeofday(tv, nil)
}

//sys	gettimeofday(tv *Timeval, tz *byte) (err error)

// Compatibility aliases

func EpollCreate(size int) (fd int, err error) {
	if size <= 0 {
		return -1, EINVAL
	}
	return EpollCreate1(0)
}

//sys	EpollCreate1(flag int) (fd int, err error)
//sys	EpollCtl(epfd int, op int, fd int, event *EpollEvent) (err error)
//sys	EpollWait(epfd int, events []EpollEvent, msec int) (n int, err error)

//sys	Dup(oldfd int) (fd int, err error)
//sys	Dup3(oldfd int, newfd int, flags int) (err error)

func Dup2(oldfd int, newfd int) (err error) {
	return Dup3(oldfd, newfd, 0)
}

//sysnb	Getpid() (pid int)
//sysnb	Getppid() (ppid int)
//sysnb	Gettid() (tid int)
//sysnb	Getuid() (uid int)
//sysnb	Geteuid() (euid int)
//sysnb	Getgid() (gid int)
//sysnb	Getegid() (egid int)
//sys	Chdir(path string) (err error)
//sys	Chroot(path string) (err error)
//sys	Close(fd int) (err error)
//sys	Fchdir(fd int) (err error)
//sys	Fchmod(fd int, mode uint32) (err error)
//sys	Fchown(fd int, uid int, gid int) (err error)
//sys	Fdatasync(fd int) (err error)
//sys	Fsync(fd int) (err error)
//sys	Ftruncate(fd int, length int64) (err error)
//sys	Getpgid(pid int) (pgid int, err error)
//sys	Getpgrp() (pid int)
//sys	Kill(pid int, sig Signal) (err error)
//sys	Nanosleep(time *Timespec, leftover *Timespec) (err error)
//sys	Read(fd int, p []byte) (n int, err error)
//sys	Pread(fd int, p []byte, offset int64) (n int, err error)
//sys	Pwrite(fd int, p []byte, offset int64) (n int, err error)
//sys	Seek(fd int, offset int64, whence int) (off int64, err error) = SYS_LSEEK
//sys	Setfsgid(gid int) (err error)
//sys	Setfsuid(uid int) (err error)
//sys	Setpgid(pid int, pgid int) (err error)
//sys	Setsid() (pid int, err error)
//sys	Setuid(uid int) (err error)
//sys	Setgid(gid int) (err error)
//sys	Setreuid(ruid int, euid int) (err error)
//sys	Setregid(rgid int, egid int) (err error)
//sys	Setresuid(ruid int, euid int, suid int) (err error)
//sys	Setresgid(rgid int, egid int, sgid int) (err error)
//sys	Sync()
//sys	Truncate(path string, length int64) (err error)
//sys	Umask(mask int) (oldmask int)
//sys	Uname(buf *Utsname) (err error)
//sys	Write(fd int, p []byte) (n int, err error)
//sys	munmap(addr uintptr, length uintptr) (err error)
//sys	mmap(addr uintptr, length uintptr, prot int, flags int, fd int, offset int64) (xaddr uintptr, err error)

func Mmap(fd int, offset int64, length int, prot int, flags int) (data []byte, err error) {
	return mapper.Mmap(fd, offset, length, prot, flags)
}

func Munmap(b []byte) (err error) {
	return mapper.Munmap(b)
}

var mapper = &mmapper{
	active: make(map[*byte][]byte),
	mmap:   mmap,
	munmap: munmap,
}

//sys	Ioctl(fd int, req int, arg uintptr) (err error)
//sys	Fcntl(fd int, cmd int, arg int) (val int, err error)
//sys	Socket(domain int, typ int, proto int) (fd int, err error)
//sys	bind(s int, addr unsafe.Pointer, addrlen _Socklen) (err error)
//sys	connect(s int, addr unsafe.Pointer, addrlen _Socklen) (err error)
//sys	listen(s int, backlog int) (err error)
//sys	accept4(s int, rsa *RawSockaddrAny, addrlen *_Socklen, flags int) (fd int, err error)
//sys	getsockopt(s int, level int, name int, val unsafe.Pointer, vallen *_Socklen) (err error)
//sys	setsockopt(s int, level int, name int, val unsafe.Pointer, vallen uintptr) (err error)
//sys	recvfrom(fd int, p []byte, flags int, from *RawSockaddrAny, fromlen *_Socklen) (n int, err error)
//sys	sendto(s int, buf []byte, flags int, to unsafe.Pointer, addrlen _Socklen) (err error)
//sys	shutdown(s int, how int) (err error)
//sys	recvmsg(s int, msg *Msghdr, flags int) (n int, err error)
//sys	sendmsg(s int, msg *Msghdr, flags int) (n int, err error)
//sys	getpeername(fd int, rsa *RawSockaddrAny, addrlen *_Socklen) (err error)
//sys	getsockname(fd int, rsa *RawSockaddrAny, addrlen *_Socklen) (err error)

func Accept(fd int) (nfd int, sa Sockaddr, err error) {
	var rsa RawSockaddrAny
	var len _Socklen = SizeofSockaddrAny
	nfd, err = accept4(fd, &rsa, &len, 0)
	if err != nil {
		return
	}
	sa, err = anyToSockaddr(&rsa)
	if err != nil {
		Close(nfd)
		nfd = 0
	}
	return
}

func Accept4(fd int, flags int) (nfd int, sa Sockaddr, err error) {
	var rsa RawSockaddrAny
	var len _Socklen = SizeofSockaddrAny
	nfd, err = accept4(fd, &rsa, &len, flags)
	if err != nil {
		return
	}
	if len > SizeofSockaddrAny {
		panic("RawSockaddrAny too small")
	}
	sa, err = anyToSockaddr(&rsa)
	if err != nil {
		Close(nfd)
		nfd = 0
	}
	return
}

func Getsockname(fd int) (sa Sockaddr, err error) {
	var rsa RawSockaddrAny
	var len _Socklen = SizeofSockaddrAny
	if err = getsockname(fd, &rsa, &len); err != nil {
		return
	}
	return anyToSockaddr(&rsa)
}

//sys	fcntl(fd int, cmd int, arg int) (val int, err error)

func direntIno(buf []byte) (uint64, bool) {
	return readInt(buf, unsafe.Offsetof(Dirent{}.Ino), unsafe.Sizeof(Dirent{}.Ino))
}

func direntReclen(buf []byte) (uint64, bool) {
	return readInt(buf, unsafe.Offsetof(Dirent{}.Reclen), unsafe.Sizeof(Dirent{}.Reclen))
}

func direntNamlen(buf []byte) (uint64, bool) {
	reclen, ok := direntReclen(buf)
	if !ok {
		return 0, false
	}
	return reclen - uint64(unsafe.Offsetof(Dirent{}.Name)), true
}

//sys	getrlimit(resource int, rlim *Rlimit) (err error) = SYS_GETRLIMIT
//sys	prlimit(pid int, resource int, newlimit *Rlimit, old *Rlimit) (err error) = SYS_PRLIMIT64

func Getrlimit(resource int, rlim *Rlimit) (err error) {
	return prlimit(0, resource, nil, rlim)
}

// setrlimit sets a resource limit.
func setrlimit(resource int, rlim *Rlimit) (err error) {
	return prlimit(0, resource, rlim, nil)
}

func ReadDirent(fd int, buf []byte) (n int, err error) {
	return Getdents(fd, buf)
}

func Shutdown(s int, how int) (err error) {
	return shutdown(s, how)
}

func Listen(s int, backlog int) (err error) {
	return listen(s, backlog)
}

const (
	RUSAGE_SELF     = 0
	RUSAGE_CHILDREN = -1
)

