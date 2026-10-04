package main

import (
	"errors"
	"io/fs"
	"os"
	"time"
)

// checkPipeClose asserts that closing a pipe end wakes a goroutine blocked
// on it with ErrClosed. os/exec's WaitDelay closes a child's pipes to end
// the copies still reading them, so a read a close cannot wake parks Wait
// forever. On an NT host a pipe is a synchronous handle nothing polls, and
// the close has to abort the transfer itself.
const pipeCloseWake = 10 * time.Second

func checkPipeClose() {
	r, w, err := os.Pipe()
	if err != nil {
		fail("pipeclose", "pipe: %v", err)
		return
	}
	defer w.Close()

	read := make(chan error, 1)
	go func() {
		_, err := r.Read(make([]byte, 1))
		read <- err
	}()
	// The read has to be blocked in the kernel before the close, or the
	// close wins a race this check is not about.
	time.Sleep(200 * time.Millisecond)
	if err := r.Close(); err != nil {
		fail("pipeclose", "close: %v", err)
		return
	}
	select {
	case err := <-read:
		if !errors.Is(err, fs.ErrClosed) {
			fail("pipeclose", "the read returned %v, want %v", err, fs.ErrClosed)
			return
		}
	case <-time.After(pipeCloseWake):
		fail("pipeclose", "the close did not wake the blocked read within %v", pipeCloseWake)
		return
	}
	ok("pipeclose", "a close ends a blocked pipe read with ErrClosed")
}
