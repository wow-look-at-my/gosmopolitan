// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "internal/strconv"

// forkExecStatusBudget bounds the wait for a child's exec, in nanoseconds. A
// child that has not exec'd by then is stuck between fork and exec, and the
// wait would never end: the pipe closes only when the child execs or exits.
const forkExecStatusBudget = 120 * 1e9

// readForkExecStatus reads the child's exec status off the pipe. It returns
// what readlen returns: 0 bytes at EOF for a child that exec'd, an errno for
// one that could not. Past the budget it kills the child and answers
// ETIMEDOUT, so the parent reports a spawn that never happened instead of
// waiting on it forever.
func readForkExecStatus(fd int, p *byte, np int, pid int) (n int, err error) {
	if err := SetNonblock(fd, true); err != nil {
		return readlen(fd, p, np)
	}
	var waited int64
	step := Timespec{Nsec: 1e6}
	for {
		n, err = readlen(fd, p, np)
		if err != EAGAIN && err != EINTR {
			return n, err
		}
		if waited >= forkExecStatusBudget {
			forkExecStatusReport(pid)
			Kill(pid, SIGKILL)
			return 0, ETIMEDOUT
		}
		Nanosleep(&step, nil)
		waited += step.Nsec
		// The sleep grows, so a slow exec costs little and a stuck one does
		// not spin.
		if step.Nsec < 50e6 {
			step.Nsec *= 2
		}
	}
}

// forkExecStatusReport describes a child that has not exec'd by the budget,
// before the kill. It runs only with GOCOSMOFORKDIAG set: ps says whether
// the child is still this program or already the exec'd one, and on macOS
// sample says where it sits. Temporary, until the stall on macOS is found.
func forkExecStatusReport(pid int) {
	if v, ok := Getenv("GOCOSMOFORKDIAG"); !ok || v == "" {
		return
	}
	script := "echo forkExec: child $0 has not exec'd after 120s >&2; " +
		"ps -o pid,ppid,stat,wchan,command -p $0 >&2; " +
		"if command -v sample >/dev/null 2>&1; then sample $0 1 -file /dev/stderr; fi"
	attr := &ProcAttr{Files: []uintptr{0, 1, 2}, Env: Environ()}
	child, err := forkExec("/bin/sh", []string{"sh", "-c", script, strconv.Itoa(pid)}, attr)
	if err != nil {
		return
	}
	var status WaitStatus
	for {
		if _, err := Wait4(child, &status, 0, nil); err != EINTR {
			return
		}
	}
}
