// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import (
	"internal/strconv"
)

// forkExecStatusBudget bounds the wait for a child's exec, in nanoseconds.
const forkExecStatusBudget = 120 * 1e9

// readForkExecStatus reads the child's exec status off the pipe. It returns
// what readlen returns: a couple of bytes at EOF for a child that exec'd, an
// errno for one that could not. Past the budget it kills the child and
// answers ETIMEDOUT, so the parent reports a spawn that never happened
// instead of waiting on it forever.
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
			forkExecStatusKill(pid)
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

// forkExecStatusKill kills a child that is past the budget and names it on
// stderr. The kill is not silent: the parent is about to report ETIMEDOUT
// for a spawn that got as far as fork. Only this line says which pid never
// reached exec.
func forkExecStatusKill(pid int) {
	spid := strconv.Itoa(pid)
	msg := "forkExec: child " + spid + " has not exec'd after 120s; killing it"
	if err := Kill(pid, SIGKILL); err != nil {
		// ESRCH, or a zombie, means the pipe's write end is held somewhere else.
		msg += ": kill: " + err.Error()
	}
	Write(2, []byte(msg+"\n"))
}
