// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix && !cosmo

package syscall

// execAPEFallback is the cosmo port's retry of an execve that answered
// ENOEXEC on an APE. No other port emits one, so this returns the error
// execve gave.
func execAPEFallback(argv0 *byte, argv, envv []*byte, err error) error {
	return err
}
