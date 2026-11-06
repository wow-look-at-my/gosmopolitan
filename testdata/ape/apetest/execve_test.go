//go:build !windows

package apetest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every other execution in this suite reaches the program through /bin/sh, so
// none of them says the kernel can load the file at all: the suite would pass
// against an artifact no execve can start. commandForAPE documents that the
// shell is what carries it, and this pins what the kernel answers on its own.
//
// Go's os/exec is a raw execve. A POSIX shell retries an ENOEXEC file as a
// script and glibc's execvp does the same, which is why neither can be used to
// ask the question; exec.Command reports the errno instead.
//
// Two answers are correct here, and which one a host gives is that host's
// business, not the artifact's:
//
//   - ENOEXEC from a kernel never told about the magic. The bootstrap script is
//     what makes the file runnable, which is the claim under test.
//   - A program that ran, from a kernel handed the magic. The CI legs register
//     the binfmt_misc entry for it a few steps before this suite runs, so on
//     those hosts the zero-write start docs/APE-BOOT.md describes is what
//     execve has to deliver.
//
// Anything else is a defect: a load that half-works, an errno that says the
// file was not even tried, or bytes the kernel changed. Asking for exactly one
// of the two refusals would fail against a working binary on the legs that
// register the magic, so this asserts the answer is one of the two and pins
// what each answer owes.
func TestExecveHandlesTheAPEAsShipped(t *testing.T) {
	bin := copyAPE(t)
	before, err := os.ReadFile(bin)
	require.NoError(t, err)
	require.Greater(t, len(before), 8, "the fixture is shorter than the APE magic")
	require.Equal(t, "MZqFpD='", string(before[:8]), "fixture is not an APE")

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "10", "5")
	cmd.WaitDelay = 30 * time.Second
	out, err := cmd.Output()

	switch {
	case err == nil:
		// The kernel loaded it, so it had better have reached the program.
		// Otherwise a header that loads into a broken image reads as success.
		t.Logf("this kernel loaded the APE directly")
		assert.Contains(t, string(out), "fizzbuzz", "the kernel loaded the APE and the program did not run")
	case errors.Is(err, syscall.ENOEXEC):
		t.Logf("this kernel refused the APE with ENOEXEC, as one with no handler for the magic must")
	default:
		t.Fatalf("execve of the APE answered %v, want either ENOEXEC or a successful run; output: %s", err, out)
	}

	// What makes an APE runnable is its header, never a side effect of the
	// kernel reading it. That holds whichever answer the kernel gave.
	after, err := os.ReadFile(bin)
	require.NoError(t, err)
	assert.Equal(t, before, after, "execve must leave the APE byte-identical")
}
