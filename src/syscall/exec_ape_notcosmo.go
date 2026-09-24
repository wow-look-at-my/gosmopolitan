// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

<<<<<<< HEAD
//go:build unix && !cosmo

package syscall

// execAPEFallback is the cosmo port's retry of an execve that answered
// ENOEXEC on an APE. No other port emits one, so this returns the error
// execve gave.
=======
//go:build unix && !cosmo && !darwin && !linux

package syscall

// execAPEFallback is the retry of an execve that answered ENOEXEC on an
// APE, in exec_ape.go. This toolchain builds no binary for this port, so
// this returns the error execve gave.
>>>>>>> origin/master
func execAPEFallback(argv0 *byte, argv, envv []*byte, err error) error {
	return err
}
