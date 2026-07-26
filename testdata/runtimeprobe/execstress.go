package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// checkExecStress hammers fork+exec while the watchdog goroutine spins
// hot, which is the condition under which macOS CI has been losing a
// forked child: the parent blocks forever in forkExec's read of the
// child status pipe (exec_unix.go's readlen), the 90s watchdog fires,
// and the traceback shows goroutine 1 parked in that read with no other
// goroutine in sight.
//
// One exec is enough to hit it perhaps a tenth of the time, which is why
// a probe that execs three times wedges on roughly a quarter to a half
// of macOS runs. Looping raises that to a near-certainty, so this check
// turns a run-to-run coin flip into a deterministic gate.
//
// The forensic value is the marker file. The child writes its pid there
// as the very first thing it does after exec, so when a round wedges the
// marker answers the question that separates the two candidate
// explanations, and nothing else does:
//
//   - marker ABSENT: the child never reached exec. It is stuck between
//     fork and exec, where only async-signal-safe work is legal and a
//     signal arriving on the forked thread can wedge it.
//   - marker PRESENT: the child exec'd and ran fine, so the write end of
//     the status pipe stayed open in some other process - a close-on-exec
//     leak, which on a darwin host is possible because cosmo emulates
//     pipe2 as pipe+fcntl (darwinPipe2, documented "not atomic").
//
// Reading the marker needs no exec of its own, so the diagnosis still
// works when exec is precisely what is broken.
const (
	execStressRounds  = 60
	execStressTimeout = 20 * time.Second
)

// execStressMarkerEnv names the file the execstress child stamps.
const execStressMarkerEnv = "RUNTIMEPROBE_STRESS_MARKER"

// execStressChild is the child half: stamp the marker and leave. It runs
// after a successful exec by definition, so the stamp existing is proof
// the exec completed.
func execStressChild() {
	marker := os.Getenv(execStressMarkerEnv)
	if marker == "" {
		return
	}
	os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())), 0644)
}

// execStressReport prints what a wedged round can still tell us. It only
// touches the filesystem - no exec, no fork - because it runs precisely
// when process creation is suspect.
func execStressReport(round int, phase, marker string) {
	data, err := os.ReadFile(marker)
	switch {
	case err == nil:
		fmt.Printf("FAIL execstress: round %d wedged in %s; child DID exec (marker pid %s) -"+
			" status-pipe write end leaked past exec\n", round, phase, string(data))
	case os.IsNotExist(err):
		fmt.Printf("FAIL execstress: round %d wedged in %s; child did NOT reach exec"+
			" (no marker) - child stuck between fork and exec\n", round, phase)
	default:
		fmt.Printf("FAIL execstress: round %d wedged in %s; marker unreadable: %v\n",
			round, phase, err)
	}
}

func checkExecStress() {
	dir, err := os.MkdirTemp("", "rp-execstress")
	if err != nil {
		fail("execstress", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)
	marker := filepath.Join(dir, "marker")

	for round := 0; round < execStressRounds; round++ {
		if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
			fail("execstress", "round %d: clear marker: %v", round, err)
			return
		}

		cmd, _, bad := selfCommand("execstress", "execstress")
		if bad {
			return
		}
		cmd.Env = append(cmd.Env, execStressMarkerEnv+"="+marker)

		// The wedge parks the caller inside Start (or Wait) with no way
		// to interrupt it, so a side goroutine does the reporting. It
		// prints before the 90s watchdog panics, which is the only
		// reason the diagnosis reaches the log at all.
		phase := make(chan string, 2)
		done := make(chan struct{})
		go func(round int) {
			cur := "start"
			for {
				select {
				case <-done:
					return
				case p := <-phase:
					cur = p
				case <-time.After(execStressTimeout):
					execStressReport(round, cur, marker)
					return
				}
			}
		}(round)

		startErr := cmd.Start()
		phase <- "wait"
		if startErr != nil {
			close(done)
			fail("execstress", "round %d: start: %v", round, startErr)
			return
		}
		waitErr := cmd.Wait()
		close(done)
		if waitErr != nil {
			fail("execstress", "round %d: wait: %v", round, waitErr)
			return
		}
		if _, err := os.Stat(marker); err != nil {
			fail("execstress", "round %d: child exited 0 but left no marker: %v", round, err)
			return
		}
	}
	ok("execstress", fmt.Sprintf("%d fork+exec rounds", execStressRounds))
}
