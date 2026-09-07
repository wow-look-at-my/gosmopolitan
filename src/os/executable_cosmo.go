// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package os

import (
	"internal/stringslite"
	"sync"
)

// Cosmopolitan binaries run on several host operating systems. On Linux
// hosts /proc/self/exe gives the answer directly. On hosts without
// procfs (such as macOS) fall back to resolving Args[0] against the
// working directory captured at init and $PATH, the same strategy
// executable_path.go uses on aix and openbsd.

// We query the working directory at init, to use it later to search for the
// executable file
// errWd will be checked later, if we need to use initWd
var initWd, errWd = Getwd()

// The resolved path is remembered. The search below reaches the file
// through the filesystem, so it stops answering once the program deletes
// its own binary - which a running program may do, and which the kernel
// answers on Linux and Apple answers from the arguments it passed. The
// path a process was started from does not change, so one resolution
// serves every later call.
var exeOnce struct {
	sync.Once
	path string
	err  error
}

func executable() (string, error) {
	if path, err := Readlink("/proc/self/exe"); err == nil {
		// When the executable has been deleted then Readlink returns a
		// path appended with " (deleted)".
		return stringslite.TrimSuffix(path, " (deleted)"), nil
	}

	// No usable procfs on this host: resolve Args[0] instead, once.
	exeOnce.Do(func() { exeOnce.path, exeOnce.err = resolveArgv0() })
	return exeOnce.path, exeOnce.err
}

func resolveArgv0() (string, error) {
	var exePath string
	if len(Args) == 0 || Args[0] == "" {
		return "", ErrNotExist
	}
	if IsPathSeparator(Args[0][0]) {
		// Args[0] is an absolute path, so it is the executable.
		// Note that we only need to worry about Unix paths here.
		exePath = Args[0]
	} else {
		for i := 1; i < len(Args[0]); i++ {
			if IsPathSeparator(Args[0][i]) {
				// Args[0] is a relative path: prepend the
				// initial working directory.
				if errWd != nil {
					return "", errWd
				}
				exePath = initWd + string(PathSeparator) + Args[0]
				break
			}
		}
	}
	if exePath != "" {
		if err := isExecutable(exePath); err != nil {
			return "", err
		}
		return exePath, nil
	}
	// Search for executable in $PATH.
	for _, dir := range splitPathList(Getenv("PATH")) {
		if len(dir) == 0 {
			dir = "."
		}
		if !IsPathSeparator(dir[0]) {
			if errWd != nil {
				return "", errWd
			}
			dir = initWd + string(PathSeparator) + dir
		}
		exePath = dir + string(PathSeparator) + Args[0]
		switch isExecutable(exePath) {
		case nil:
			return exePath, nil
		case ErrPermission:
			return "", ErrPermission
		}
	}
	return "", ErrNotExist
}

// isExecutable returns an error if a given file is not an executable.
func isExecutable(path string) error {
	stat, err := Stat(path)
	if err != nil {
		return err
	}
	mode := stat.Mode()
	if !mode.IsRegular() {
		return ErrPermission
	}
	if (mode & 0111) == 0 {
		return ErrPermission
	}
	return nil
}

// splitPathList splits a path list.
// This is based on genSplit from strings/strings.go
func splitPathList(pathList string) []string {
	if pathList == "" {
		return nil
	}
	n := 1
	for i := 0; i < len(pathList); i++ {
		if rune(pathList[i]) == PathListSeparator {
			n++
		}
	}
	start := 0
	a := make([]string, n)
	na := 0
	for i := 0; i+1 <= len(pathList) && na+1 < n; i++ {
		if rune(pathList[i]) == PathListSeparator {
			a[na] = pathList[start:i]
			na++
			start = i + 1
		}
	}
	a[na] = pathList[start:]
	return a[:na+1]
}
