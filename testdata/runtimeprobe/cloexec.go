package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// checkCloexec asserts the one invariant the whole fork/exec machinery
// rests on: when a descriptor is created with close-on-exec requested,
// FD_CLOEXEC is actually set on it.
//
// Go's os/exec is built on that promise. forkExec creates its child
// status pipe close-on-exec and then blocks reading it until every copy
// of the write end is gone; the child's copy is supposed to disappear at
// exec. If the flag is silently not applied, the write end rides through
// exec into the child, and the parent waits for an EOF that can never
// come. Whether that is visible depends entirely on what the child does
// next: a child that exits promptly closes the descriptor on its way out
// and the bug hides, while a child that waits on the parent deadlocks it.
// That is why the wedge only ever showed up in checkFdpass, whose child
// waits, and never in the exec checks whose children exit.
//
// A descriptor census taken from a wedged macOS run showed the pipes the
// exec machinery had created carrying no FD_CLOEXEC at all, and the
// child holding both ends of the status pipe. This check turns that
// observation into a standing assertion over every creation path,
// reporting each one separately so a failure names the mechanism instead
// of leaving a deadlock to be re-run until it goes away.
func checkCloexec() {
	dir, err := os.MkdirTemp("", "rp-cloexec")
	if err != nil {
		fail("cloexec", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)

	var broken []string
	// note records one mechanism's result. Everything here asks for
	// close-on-exec, so anything without the flag afterwards is a bug.
	note := func(mech string, fd int, err error) {
		if err != nil {
			broken = append(broken, fmt.Sprintf("%s: create failed: %v", mech, err))
			return
		}
		defer syscall.Close(fd)
		flags, ferr := fcntlGetFD(fd)
		if ferr != nil {
			broken = append(broken, fmt.Sprintf("%s: F_GETFD: %v", mech, ferr))
			return
		}
		if flags&syscall.FD_CLOEXEC == 0 {
			broken = append(broken, mech+": FD_CLOEXEC NOT set")
		}
	}

	// pipe2(O_CLOEXEC) - what forkExec's status pipe and os/exec's stdio
	// pipes are built from, so this is the one the wedge rides on.
	var p [2]int
	if err := syscall.Pipe2(p[:], syscall.O_CLOEXEC); err != nil {
		broken = append(broken, fmt.Sprintf("pipe2(O_CLOEXEC): %v", err))
	} else {
		note("pipe2(O_CLOEXEC) read end", p[0], nil)
		note("pipe2(O_CLOEXEC) write end", p[1], nil)
	}

	// os.Pipe - the same path through the os package.
	if r, w, err := os.Pipe(); err != nil {
		broken = append(broken, fmt.Sprintf("os.Pipe: %v", err))
	} else {
		rf, wf := int(r.Fd()), int(w.Fd())
		if flags, ferr := fcntlGetFD(rf); ferr == nil && flags&syscall.FD_CLOEXEC == 0 {
			broken = append(broken, "os.Pipe read end: FD_CLOEXEC NOT set")
		}
		if flags, ferr := fcntlGetFD(wf); ferr == nil && flags&syscall.FD_CLOEXEC == 0 {
			broken = append(broken, "os.Pipe write end: FD_CLOEXEC NOT set")
		}
		r.Close()
		w.Close()
	}

	// open(O_CLOEXEC).
	fpath := filepath.Join(dir, "f")
	if err := os.WriteFile(fpath, []byte("x"), 0o644); err == nil {
		fd, err := syscall.Open(fpath, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
		note("open(O_CLOEXEC)", fd, err)
	}

	// socket(SOCK_CLOEXEC).
	s, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	note("socket(SOCK_CLOEXEC)", s, err)

	// fcntl(F_DUPFD_CLOEXEC) - internal/poll's DupCloseOnExec fast path,
	// used by net.FileConn and (*net.TCPConn).File. Its command number
	// differs between Linux and Apple, so it is a translation the
	// emulation has to get right rather than a coincidence it can rely on.
	base, err := syscall.Open(fpath, syscall.O_RDONLY, 0)
	if err == nil {
		r, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(base),
			uintptr(syscall.F_DUPFD_CLOEXEC), 0)
		if e != 0 {
			broken = append(broken, fmt.Sprintf("fcntl(F_DUPFD_CLOEXEC): %v", e))
		} else {
			note("fcntl(F_DUPFD_CLOEXEC)", int(r), nil)
		}
		syscall.Close(base)
	}

	if len(broken) > 0 {
		fail("cloexec", "%s", strings.Join(broken, "; "))
		return
	}
	ok("cloexec")
}
