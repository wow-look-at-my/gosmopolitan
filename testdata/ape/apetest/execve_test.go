//go:build !windows

package apetest

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every other execution in this suite reaches the program through /bin/sh, so
// none of them says whether the kernel can load the file at all: the suite
// would pass against an artifact execve(2) refuses outright. commandForAPE
// documents that it refuses one, and this is what holds it to that.
//
// Go's os/exec is a raw execve. A POSIX shell retries an ENOEXEC file as a
// script and glibc's execvp does the same, which is why neither can be used
// to ask the question; exec.Command reports the errno instead.
func TestExecveRefusesTheAPEAsShipped(t *testing.T) {
	bin := copyAPE(t)
	before, err := os.ReadFile(bin)
	require.NoError(t, err)
	require.Equal(t, "MZqFpD='", string(before[:8]), "fixture is not an APE")

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "10", "5")
	cmd.WaitDelay = 30 * time.Second
	_, err = cmd.Output()
	require.Error(t, err, "an MZ-headed file must not be loadable by execve")
	assert.ErrorIs(t, err, syscall.ENOEXEC)

	// A refused exec must also not have touched the file. What makes an APE
	// runnable is the prologue reached through a shell, never a side effect of
	// the kernel turning it down.
	after, err := os.ReadFile(bin)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a refused execve must leave the APE alone")
}
