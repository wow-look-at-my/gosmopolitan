// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build unix && !cosmo && !darwin && !linux

package syscall

// execAPEFallback is the retry of an execve that answered ENOEXEC on an APE,
// in exec_ape.go.
func execAPEFallback(argv0 *byte, argv, envv []*byte, err error) error {
	return err
}
