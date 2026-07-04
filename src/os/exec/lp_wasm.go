// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm

package exec

import (
	"errors"
	"runtime"
)

// ErrNotFound is the error resulting if a path search failed to find an executable file.
var ErrNotFound = errors.New("executable file not found in $PATH")

// noExecError is the sentinel error (wrapped in *Error) reported by LookPath.
// Wasm cannot start processes, so no file - not even one that exists at an
// absolute path - can ever be executed; say so truthfully instead of blaming
// $PATH. It matches errors.ErrUnsupported, and also ErrNotFound for
// compatibility with callers that check for it.
type noExecError struct{}

func (noExecError) Error() string {
	return "cannot execute binaries on " + runtime.GOOS + "/" + runtime.GOARCH
}

func (noExecError) Is(target error) bool {
	return target == errors.ErrUnsupported || target == ErrNotFound
}

// LookPath searches for an executable named file in the
// directories named by the PATH environment variable.
// If file contains a slash, it is tried directly and the PATH is not consulted.
// The result may be an absolute path or a path relative to the current directory.
func LookPath(file string) (string, error) {
	// Wasm can not execute processes, so no executable can be found, ever.
	return "", &Error{file, noExecError{}}
}

// lookExtensions is a no-op on non-Windows platforms, since
// they do not restrict executables to specific extensions.
func lookExtensions(path, dir string) (string, error) {
	return path, nil
}
