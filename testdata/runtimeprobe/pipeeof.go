package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// checkPipeEOF asserts that a child's stdout pipe reaches EOF when the
// child exits. os/exec waits for that EOF inside Wait, so a pipe whose
// write end escaped to another process parks the parent forever. That is
// the macOS shape go-toolchain hits: a `go` subprocess exits and the
// parent stays in io.Copy on its stdout until the step budget runs out.
//
// A write end can only escape through a fork that happens while the
// parent still holds it, and that window lives inside Start. So each
// round forks a holder child CONCURRENTLY with the piped child. The
// holder then outlives the piped child, which is what turns an escaped
// descriptor into a wedge instead of a delay.
//
// Close-on-exec is the guard that must hold here. The arm64-apple
// variadic ABI once left FD_CLOEXEC unset on a share of all descriptors,
// so a regression in that area lands exactly here.
const (
	pipeEOFRounds  = 8
	pipeEOFTimeout = 15 * time.Second
	pipeEOFRelease = 5 * time.Second
)

const pipeEOFMarker = "pipe-eof-child"

// pipeEOFChild writes the marker to stdout and exits at once. The exit
// is what must close the pipe.
func pipeEOFChild() {
	fmt.Println(pipeEOFMarker)
}

func checkPipeEOF() {
	dir, err := os.MkdirTemp("", "rp-pipeeof")
	if err != nil {
		fail("pipeeof", "mkdtemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)

	// Spare descriptors push the pipe onto a high number, which selects
	// the fd-shuffling path in the child between fork and exec.
	pad := execStressPadFds(dir)
	defer func() {
		for _, f := range pad {
			f.Close()
		}
	}()

	for round := 0; round < pipeEOFRounds; round++ {
		goFile := filepath.Join(dir, fmt.Sprintf("go-%d", round))
		census := filepath.Join(dir, fmt.Sprintf("holder-%d", round))
		release := func() { os.WriteFile(goFile, []byte("go"), 0644) }

		holder, _, bad := selfCommand("pipeeof", "execstress")
		if bad {
			return
		}
		holder.Env = append(holder.Env,
			execStressCensusEnv+"="+census,
			execStressGoEnv+"="+goFile)
		holderStart := make(chan error, 1)
		go func() { holderStart <- holder.Start() }()

		cmd, _, bad := selfCommand("pipeeof", "pipeecho")
		if bad {
			release()
			<-holderStart
			return
		}
		var out strings.Builder
		cmd.Stdout = &out
		if err := cmd.Start(); err != nil {
			fail("pipeeof", "round %d: start piped child: %v", round, err)
			release()
			<-holderStart
			return
		}

		waitErr := make(chan error, 1)
		go func() { waitErr <- cmd.Wait() }()
		select {
		case err := <-waitErr:
			switch {
			case err != nil:
				fail("pipeeof", "round %d: piped child: %v", round, err)
			case !strings.Contains(out.String(), pipeEOFMarker):
				fail("pipeeof", "round %d: child stdout %q, want %q", round, out.String(), pipeEOFMarker)
			default:
				release()
				if startErr := <-holderStart; startErr == nil {
					holder.Wait()
				}
				continue
			}
			release()
			<-holderStart
			return
		case <-time.After(pipeEOFTimeout):
			reportPipeEOFWedge(round, census, release, waitErr)
			<-holderStart
			return
		}
	}
	ok("pipeeof", fmt.Sprintf("%d rounds, %d spare fds", pipeEOFRounds, len(pad)))
}

// reportPipeEOFWedge names the descriptor that did not close, then
// releases the holder and reports whether that alone unwedged the copy.
// A copy that finishes on release proves the holder was the thief, which
// is the difference between a lost close-on-exec and a pipe the kernel
// never drained.
func reportPipeEOFWedge(round int, census string, release func(), waitErr <-chan error) {
	fail("pipeeof", "round %d: the piped child's stdout never reached EOF; parent fds=[%s]", round, fdCensus())
	if data, err := os.ReadFile(census); err == nil {
		fmt.Printf("FAIL pipeeof: round %d holder census: %s\n", round, string(data))
	} else {
		fmt.Printf("FAIL pipeeof: round %d holder left no census (%v)\n", round, err)
	}
	release()
	select {
	case err := <-waitErr:
		fmt.Printf("FAIL pipeeof: round %d releasing the holder unwedged the copy (wait: %v)\n", round, err)
	case <-time.After(pipeEOFRelease):
		fmt.Printf("FAIL pipeeof: round %d still wedged after the holder exited\n", round)
	}
}
