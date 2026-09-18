package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

// The three host-identity primitives a consumer needs from an APE, each
// checked on the host that has to answer it. All three were previously
// unavailable or unreliable on a darwin host, and every one of them fails
// CLOSED, which is why nothing noticed: a caller that cannot tell where it
// is running takes the wrong branch quietly.

// checkHostOS asserts the runtime reports the host it is actually running
// on. GOOS and GOARCH are variables on cosmo, read at startup from what
// the APE entry stub recorded, so a package that switches on GOOS takes
// the branch for the kernel underneath. cosmoHostOS reads the same
// record the runtime dispatches every syscall on, so the two cannot
// disagree.
func checkHostOS() {
	host := cosmoHostOS()
	switch host {
	case "linux", "darwin", "windows":
	default:
		fail("hostos", "reported %q, want the real host", host)
		return
	}
	if runtime.GOOS != host {
		fail("hostos", "runtime.GOOS is %q on a %s host", runtime.GOOS, host)
		return
	}
	ok("hostos", host+"/"+runtime.GOARCH)
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

// checkDupFile asserts dup(2) and dup2(2) on a FILE descriptor on every
// host. The duplicate is the same open file: a write through it lands
// in the file, and the original still works after the duplicate is
// closed. dup2 onto a slot that is open replaces what was there.
func checkDupFile() {
	dir, err := os.MkdirTemp("", "rp-dup")
	if err != nil {
		fail("dupfile", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "target")
	f, err := os.Create(path)
	if err != nil {
		fail("dupfile", "create: %v", err)
		return
	}
	defer f.Close()

	nfd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		fail("dupfile", "dup: %v", err)
		return
	}
	if _, err := syscall.Write(nfd, []byte("x")); err != nil {
		syscall.Close(nfd)
		fail("dupfile", "write through the duplicate: %v", err)
		return
	}
	if err := syscall.Close(nfd); err != nil {
		fail("dupfile", "close the duplicate: %v", err)
		return
	}
	if _, err := f.WriteString("y"); err != nil {
		fail("dupfile", "write through the original after the duplicate closed: %v", err)
		return
	}

	// dup2 over an open slot: the slot's old file is closed and the
	// slot now names the target.
	other, err := os.Create(filepath.Join(dir, "other"))
	if err != nil {
		fail("dupfile", "create other: %v", err)
		return
	}
	defer other.Close()
	if err := syscall.Dup2(int(f.Fd()), int(other.Fd())); err != nil {
		fail("dupfile", "dup2: %v", err)
		return
	}
	if _, err := syscall.Write(int(other.Fd()), []byte("z")); err != nil {
		fail("dupfile", "write through the dup2 slot: %v", err)
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		fail("dupfile", "read back: %v", err)
		return
	}
	if string(got) != "xyz" {
		fail("dupfile", "read back %q, want %q", got, "xyz")
		return
	}
	ok("dupfile", fmt.Sprintf("fd %d", nfd))
}
