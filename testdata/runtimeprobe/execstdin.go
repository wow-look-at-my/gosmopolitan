package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// checkExecStdin runs a child that reads its stdin from a Go reader, the
// shape of cmd/go's `cc -x c -` probe. os/exec starts the goroutine that
// feeds stdin only after Start returns, and Start returns only when the
// child's exec closes the status pipe. A status pipe that survives the
// exec therefore waits on the child, which waits on stdin: a deadlock
// that forkExec's budget ends after two minutes. A healthy exec makes
// this take milliseconds, so anything past the deadline is that wedge.
func checkExecStdin() {
	const deadline = 20 * time.Second
	cmd := exec.Command("cat")
	cmd.Stdin = strings.NewReader("stdin reaches the child\n")
	start := time.Now()
	done := make(chan error, 1)
	var out []byte
	go func() {
		var err error
		out, err = cmd.Output()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			fail("execstdin", "cat: %v", err)
			return
		}
		if string(out) != "stdin reaches the child\n" {
			fail("execstdin", "cat echoed %q", out)
			return
		}
		ok("execstdin", fmt.Sprintf("cat echoed stdin in %v", time.Since(start).Round(time.Millisecond)))
	case <-time.After(deadline):
		fail("execstdin", "cat has not returned after %v: the status pipe outlived the exec", deadline)
	}
}
