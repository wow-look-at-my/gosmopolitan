package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// The three host-identity primitives a consumer needs from an APE, each
// checked on the host that has to answer it. All three were previously
// unavailable or unreliable on a darwin host, and every one of them fails
// CLOSED, which is why nothing noticed: a caller that cannot tell where it
// is running takes the wrong branch quietly.

// checkHostOS asserts the runtime reports the host it is actually running
// on. runtime.GOOS is "cosmo" everywhere, so anything that must know the
// real host has had to infer it - and every inference is deniable by a
// sandbox (filesystem probes) or unimplemented (syscall.Uname is ENOSYS on
// darwin and NT). cosmoHostOS reads what the entry stub recorded and the
// runtime dispatches every syscall on, so it cannot disagree with reality.
func checkHostOS() {
	host := cosmoHostOS()
	switch host {
	case "linux", "darwin", "windows":
		ok("hostos", host)
	default:
		fail("hostos", "reported %q, want the real host", host)
	}
}

// checkFdPath exercises fcntl(F_GETPATH), the darwin way to resolve a
// descriptor back to a path. There is no Linux counterpart, so it is
// passed through under Apple's own command number (50) and answers ENOSYS
// on every other host.
func checkFdPath() {
	dir, err := os.MkdirTemp("", "rp-fdpath")
	if err != nil {
		fail("fdpath", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)
	name := filepath.Join(dir, "target")
	f, err := os.Create(name)
	if err != nil {
		fail("fdpath", "create: %v", err)
		return
	}
	defer f.Close()

	const fGetPath = 50
	var buf [1024]byte
	_, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), fGetPath, uintptr(unsafe.Pointer(&buf[0])))

	if cosmoHostOS() != "darwin" {
		// Not a failure anywhere else: report the errno so the day a
		// host grows the command, this line says so.
		ok("fdpath", fmt.Sprintf("skipped on %s (errno %v)", cosmoHostOS(), e))
		return
	}
	if e != 0 {
		fail("fdpath", "F_GETPATH: errno %v", e)
		return
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	got := string(buf[:n])
	// The path must resolve back to the same file: a plausible-looking
	// string that names something else is the failure mode worth
	// catching, not an empty one.
	gotInfo, err := os.Stat(got)
	if err != nil {
		fail("fdpath", "F_GETPATH returned %q, which does not stat: %v", got, err)
		return
	}
	wantInfo, err := os.Stat(name)
	if err != nil {
		fail("fdpath", "stat %s: %v", name, err)
		return
	}
	if !os.SameFile(gotInfo, wantInfo) {
		fail("fdpath", "F_GETPATH returned %q, which is not %s", got, name)
		return
	}
	ok("fdpath", got)
}

// linuxUcred is struct ucred, what SO_PEERCRED fills in.
type linuxUcred struct {
	Pid int32
	Uid uint32
	Gid uint32
}

// checkPeercred asks a unix socket for its peer's identity through the
// Linux spelling, SO_PEERCRED. On darwin that has no direct equivalent -
// Apple splits it across SOL_LOCAL's LOCAL_PEERPID and LOCAL_PEERCRED, at
// level 0, whose option numbers collide with Linux IPPROTO_IP - so the
// emulation answers the Linux option rather than exposing level 0. Both
// ends here are this process, so the pid is known exactly.
func checkPeercred() {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		fail("peercred", "socketpair: %v", err)
		return
	}
	defer syscall.Close(fds[0])
	defer syscall.Close(fds[1])

	const soPeercred = 17
	var uc linuxUcred
	ln := uint32(unsafe.Sizeof(uc))
	_, _, e := syscall.Syscall6(syscall.SYS_GETSOCKOPT, uintptr(fds[0]),
		uintptr(syscall.SOL_SOCKET), soPeercred,
		uintptr(unsafe.Pointer(&uc)), uintptr(unsafe.Pointer(&ln)), 0)

	if cosmoHostOS() == "windows" {
		// NT has no peer-credential option; report the errno rather
		// than asserting a behavior nothing depends on yet.
		ok("peercred", fmt.Sprintf("skipped on windows (errno %v)", e))
		return
	}
	if e != 0 {
		fail("peercred", "SO_PEERCRED: errno %v", e)
		return
	}
	if int(uc.Pid) != os.Getpid() {
		fail("peercred", "peer pid %d, want %d (both ends are this process)", uc.Pid, os.Getpid())
		return
	}
	if int(uc.Uid) != os.Getuid() {
		fail("peercred", "peer uid %d, want %d", uc.Uid, os.Getuid())
		return
	}
	ok("peercred", fmt.Sprintf("pid=%d uid=%d gid=%d", uc.Pid, uc.Uid, uc.Gid))
}

// checkDupFile records what dup(2) does to a FILE descriptor. Socket dup
// works everywhere; files and pipes are documented ENOSYS on NT, and
// nothing in the standard library is known to reach it there. This check
// exists so that stops being a claim nobody tested: it asserts dup works
// on unix hosts and prints the NT errno rather than failing, so the day
// something does reach it, the line is already in the log.
func checkDupFile() {
	dir, err := os.MkdirTemp("", "rp-dup")
	if err != nil {
		fail("dupfile", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)
	f, err := os.Create(filepath.Join(dir, "target"))
	if err != nil {
		fail("dupfile", "create: %v", err)
		return
	}
	defer f.Close()

	nfd, _, e := syscall.Syscall(syscall.SYS_DUP, f.Fd(), 0, 0)
	if cosmoHostOS() == "windows" {
		if e == 0 {
			syscall.Close(int(nfd))
			ok("dupfile", "windows: dup(2) on a file WORKS (documented ENOSYS is stale)")
			return
		}
		ok("dupfile", fmt.Sprintf("windows: errno %v (documented ENOSYS)", e))
		return
	}
	if e != 0 {
		fail("dupfile", "dup: errno %v", e)
		return
	}
	defer syscall.Close(int(nfd))
	if _, err := f.WriteString("x"); err != nil {
		fail("dupfile", "write through original: %v", err)
		return
	}
	ok("dupfile", fmt.Sprintf("fd %d", nfd))
}
