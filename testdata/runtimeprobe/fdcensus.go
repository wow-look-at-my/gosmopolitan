package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// fdCensus lists the process's open descriptors and what kind of object
// each one refers to. It uses only fcntl/fstat - no exec, no fork, no
// directory reads - so it stays usable in exactly the situation it
// exists for: diagnosing a process whose process-creation path is the
// thing under suspicion, and a forked child that must not do anything
// elaborate.
//
// The macOS wedge this serves: the parent blocks forever in forkExec's
// read of the child status pipe, which can only happen while some
// process still holds that pipe's WRITE end. The child is the only new
// process, so if the write end shows up in the child's census, the
// close-on-exec that forkExec relies on did not survive - and the census
// says which descriptor number it landed on.
func fdCensus() string {
	var b strings.Builder
	for fd := 0; fd < 64; fd++ {
		// F_GETFD on a closed descriptor fails with EBADF, which is the
		// cheapest "is this open" test that needs no /proc.
		flags, err := fcntlGetFD(fd)
		if err != nil {
			continue
		}
		var st syscall.Stat_t
		kind := "?"
		if err := syscall.Fstat(fd, &st); err == nil {
			switch st.Mode & syscall.S_IFMT {
			case syscall.S_IFIFO:
				kind = "pipe"
			case syscall.S_IFSOCK:
				kind = "sock"
			case syscall.S_IFREG:
				kind = "file"
			case syscall.S_IFCHR:
				kind = "chr"
			case syscall.S_IFDIR:
				kind = "dir"
			}
		}
		cloexec := "-"
		if flags&syscall.FD_CLOEXEC != 0 {
			cloexec = "E"
		}
		fmt.Fprintf(&b, "%d:%s%s ", fd, kind, cloexec)
	}
	return strings.TrimSpace(b.String())
}

// fcntlGetFD returns the descriptor flags (FD_CLOEXEC) for fd.
func fcntlGetFD(fd int) (int, error) {
	r, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(syscall.F_GETFD), 0)
	if e != 0 {
		return 0, e
	}
	return int(r), nil
}

// writeFdCensus stamps the census (plus the pid) into path. The fdpass
// child calls this as its first act after exec, so the file doubles as
// proof that the exec completed and as the list of descriptors that
// crossed it.
func writeFdCensus(path string) {
	if path == "" {
		return
	}
	os.WriteFile(path, []byte(fmt.Sprintf("pid=%d fds=[%s]", os.Getpid(), fdCensus())), 0644)
}
