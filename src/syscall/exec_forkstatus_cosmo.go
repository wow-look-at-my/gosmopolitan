// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import (
	"internal/strconv"
)

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
			killErr := Kill(pid, SIGKILL)
			forkExecStatusKilled(pid, killErr)
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

// forkExecStatusReport names a child that has not exec'd by the budget,
// before the kill. It must not fork: once one child is stuck between fork
// and exec, every later child of this process sticks the same way, so a
// helper forked here hangs inside the report.
//
// When GOCOSMOFORKDIAG names a directory, the report also writes the pid
// into <dir>/<pid> and waits for a watcher outside this process to sample
// the child and remove the file, for at most forkExecDiagWait.
func forkExecStatusReport(pid int) {
	dir, ok := Getenv("GOCOSMOFORKDIAG")
	if !ok || dir == "" {
		return
	}
	spid := strconv.Itoa(pid)
	Write(2, []byte("forkExec: child "+spid+" has not exec'd after 120s; killing it\n"))
	if dir[0] != '/' {
		return
	}
	path := dir + "/" + spid
	fd, err := Open(path, O_WRONLY|O_CREAT|O_TRUNC|O_CLOEXEC, 0644)
	if err != nil {
		Write(2, []byte("forkExec: diag file "+path+": "+err.Error()+"\n"))
		return
	}
	Write(fd, []byte(spid+"\n"))
	Close(fd)
	step := Timespec{Sec: 1}
	var stat Stat_t
	for waited := int64(0); waited < forkExecDiagWait; waited++ {
		if err := Stat(path, &stat); err != nil {
			Write(2, []byte("forkExec: child "+spid+" sampled after "+strconv.Itoa(int(waited))+"s\n"))
			return
		}
		Nanosleep(&step, nil)
	}
	Write(2, []byte("forkExec: no watcher removed "+path+" within "+strconv.Itoa(forkExecDiagWait)+"s\n"))
}

// forkExecDiagWait is how long forkExecStatusReport waits for the watcher,
// in seconds.
const forkExecDiagWait = 60

// forkExecStatusKilled reports what the kill found: a child that is gone
// (ESRCH) or already a zombie means the pipe's write end is held elsewhere,
// not by a stuck child.
func forkExecStatusKilled(pid int, killErr error) {
	if v, ok := Getenv("GOCOSMOFORKDIAG"); !ok || v == "" {
		return
	}
	spid := strconv.Itoa(pid)
	if killErr != nil {
		Write(2, []byte("forkExec: kill "+spid+": "+killErr.Error()+"\n"))
		return
	}
	var ws WaitStatus
	wpid, err := Wait4(pid, &ws, WNOHANG, nil)
	msg := "forkExec: kill " + spid + " ok; wait4(WNOHANG) pid=" + strconv.Itoa(wpid)
	if err != nil {
		msg += " err=" + err.Error()
	} else if wpid == pid {
		msg += " signaled=" + strconv.Itoa(int(ws.Signal())) + " exited=" + strconv.Itoa(ws.ExitStatus())
	}
	Write(2, []byte(msg+"\n"))
}
