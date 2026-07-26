package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// checkExecStress is the deterministic version of the wedge that macOS
// CI has been hitting in checkFdpass and clearing by re-running.
//
// The wedge is a deadlock, and it needs three things at once. Plain
// fork+exec in a loop has none of them and never reproduces it:
//
//  1. The parent blocks in forkExec's read of the child status pipe
//     until that pipe's write end is closed everywhere. Close-on-exec is
//     what normally closes it in the child.
//  2. Enough open descriptors that the status pipe and the child's
//     stdio land on high, shifting fd numbers - which is what selects
//     the fd-shuffling path taken in the child between fork and exec.
//  3. A child that does NOT exit promptly, but waits on the parent. A
//     child that exits immediately closes any leaked descriptor on its
//     way out, so the parent's read completes and the leak is invisible.
//     checkFdpass's child waits for the parent to accept its connection,
//     so a leak there is a true deadlock: parent waits for the child's
//     exec, child waits for the parent.
//
// So this check reproduces that shape directly: it opens a pile of
// descriptors, then spawns a child that waits for a go-ahead the parent
// can only give AFTER Start returns. Without a leak, Start returns at
// once and the go-ahead releases the child. With one, both sides are
// stuck - and the reporter prints the parent's and child's descriptor
// censuses, which name the leaked descriptor instead of leaving a bare
// traceback to argue over.
const (
	execStressRounds  = 12
	execStressFdPad   = 24
	execStressTimeout = 20 * time.Second
)

const (
	execStressCensusEnv = "RUNTIMEPROBE_STRESS_CENSUS"
	execStressGoEnv     = "RUNTIMEPROBE_STRESS_GO"
)

// execStressChild stamps its descriptor census (proof the exec
// completed, plus what crossed it) and then waits for the parent's
// go-ahead, so that any descriptor that leaked stays open long enough to
// deadlock the parent rather than being closed by a quick exit.
func execStressChild() {
	writeFdCensus(os.Getenv(execStressCensusEnv))

	goFile := os.Getenv(execStressGoEnv)
	if goFile == "" {
		return
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(goFile); err == nil {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// execStressPadFds opens spare descriptors so the interesting ones land
// on high numbers, matching the fd pressure checkFdpass builds up.
func execStressPadFds(dir string) []*os.File {
	var pad []*os.File
	for i := 0; i < execStressFdPad; i++ {
		f, err := os.Open(dir)
		if err != nil {
			break
		}
		pad = append(pad, f)
	}
	return pad
}

func checkExecStress() {
	dir, err := os.MkdirTemp("", "rp-execstress")
	if err != nil {
		fail("execstress", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)

	pad := execStressPadFds(dir)
	defer func() {
		for _, f := range pad {
			f.Close()
		}
	}()

	// A socket too: checkFdpass holds sockets, and socket descriptors
	// come from a different emulation path than files on non-Linux hosts.
	if s, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0); err == nil {
		defer syscall.Close(s)
	}

	for round := 0; round < execStressRounds; round++ {
		census := filepath.Join(dir, fmt.Sprintf("census-%d", round))
		goFile := filepath.Join(dir, fmt.Sprintf("go-%d", round))

		cmd, _, bad := selfCommand("execstress", "execstress")
		if bad {
			return
		}
		cmd.Env = append(cmd.Env,
			execStressCensusEnv+"="+census,
			execStressGoEnv+"="+goFile)

		// The reporter has to live in its own goroutine: the wedge parks
		// the caller inside Start with nothing able to interrupt it, and
		// the diagnosis must reach the log before the 90s watchdog panic.
		startDone := make(chan struct{})
		go func(round int) {
			select {
			case <-startDone:
			case <-time.After(execStressTimeout):
				fmt.Printf("FAIL execstress: round %d Start wedged; parent fds=[%s]\n",
					round, fdCensus())
				if data, err := os.ReadFile(census); err == nil {
					fmt.Printf("FAIL execstress: round %d child DID exec: %s\n", round, string(data))
				} else {
					fmt.Printf("FAIL execstress: round %d child did NOT reach exec (%v)\n", round, err)
				}
				// Release the child so the run can still finish and the
				// remaining checks report.
				os.WriteFile(goFile, []byte("go"), 0644)
			}
		}(round)

		startErr := cmd.Start()
		close(startDone)
		if startErr != nil {
			fail("execstress", "round %d: start: %v", round, startErr)
			return
		}

		// Only now may the child proceed - that ordering is what turns a
		// leaked status-pipe write end into a deadlock instead of a
		// harmless delay.
		if err := os.WriteFile(goFile, []byte("go"), 0644); err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			fail("execstress", "round %d: go-ahead: %v", round, err)
			return
		}
		if err := cmd.Wait(); err != nil {
			fail("execstress", "round %d: wait: %v", round, err)
			return
		}
		if _, err := os.Stat(census); err != nil {
			fail("execstress", "round %d: child exited but left no census: %v", round, err)
			return
		}
	}
	ok("execstress", fmt.Sprintf("%d rounds, %d spare fds", execStressRounds, len(pad)))
}
