// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package exec

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// apeMagic opens every Actually Portable Executable: the DOS stub whose bytes
// are also the shell script that boots the file on a kernel that cannot exec
// it directly.
const apeMagic = "MZqFpD"

// startProcess starts name. On a posix host the kernel refuses a pristine
// APE with ENOEXEC, because nothing has registered its header, and the file
// then starts the way its own header says it does: as a script under
// /bin/sh, which reads the header and execs the staged native image. An NT
// host starts the APE through its PE header and never gets here, and an
// assimilated APE is a native image the kernel accepts.
func startProcess(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
	proc, err := os.StartProcess(name, argv, attr)
	if err == nil || ntHost() || !errors.Is(err, syscall.ENOEXEC) || !isAPE(name, attr) {
		return proc, err
	}
	shellArgv := append([]string{"/bin/sh", name}, argv[1:]...)
	return os.StartProcess("/bin/sh", shellArgv, attr)
}

// isAPE reports whether the file name, relative to attr.Dir when it is not
// absolute, carries the APE magic.
func isAPE(name string, attr *os.ProcAttr) bool {
	if !filepath.IsAbs(name) && attr != nil && attr.Dir != "" {
		name = filepath.Join(attr.Dir, name)
	}
	file, err := os.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()
	head := make([]byte, len(apeMagic))
	if _, err := file.Read(head); err != nil {
		return false
	}
	return string(head) == apeMagic
}
