// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package os

import (
	"internal/filepathlite"
	"internal/stringslite"
	"runtime"
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
	// Only a Linux host answers this link with a path. Cosmopolitan serves
	// parts of procfs on the other hosts, so a success here is no proof the
	// answer names a file, and trusting one hands back a name that opens
	// nothing.
	if runtime.CosmoHostOS() == "linux" {
		if path, err := Readlink("/proc/self/exe"); err == nil {
			// Readlink appends " (deleted)" for a file nothing links to.
			path = stringslite.TrimSuffix(path, " (deleted)")
			// An APE boots through a loader that execs a memfd, so on Linux
			// the link reads "/memfd:<name>". That is the anonymous file's
			// name, not a path: nothing opens it, and a program that re-execs
			// itself by it fails. The loader passes the APE's own path as
			// argv[0], which is the answer, so resolve that instead.
			if !stringslite.HasPrefix(path, "/memfd:") {
				return path, nil
			}
		}
	}

	// No usable procfs on this host, or a memfd behind it: resolve Args[0]
	// instead, once.
	exeOnce.Do(func() { exeOnce.path, exeOnce.err = resolveArgv0() })
	return exeOnce.path, exeOnce.err
}

func resolveArgv0() (string, error) {
	var exePath string
	if len(Args) == 0 || Args[0] == "" {
		return "", ErrNotExist
	}
	if filepathlite.IsAbs(Args[0]) {
		// An NT host hands this APE a drive letter, and the leading byte of
		// "D:\\a\\x.exe" is no separator. IsAbs follows the host, so it sees
		// the volume. Prepending the working directory to such a name builds
		// a path carrying a colon, which NT refuses as an invalid argument.
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
		for _, named := range spellings(exePath) {
			if err := isExecutable(named); err == nil {
				return named, nil
			}
		}
		return "", ErrNotExist
	}
	// Search for executable in $PATH.
	for _, dir := range splitPathList(Getenv("PATH")) {
		if len(dir) == 0 {
			dir = "."
		}
		if !filepathlite.IsAbs(dir) {
			if errWd != nil {
				return "", errWd
			}
			dir = initWd + string(PathSeparator) + dir
		}
		for _, named := range spellings(dir + string(PathSeparator) + Args[0]) {
			switch isExecutable(named) {
			case nil:
				return named, nil
			case ErrPermission:
				return "", ErrPermission
			}
		}
	}
	return "", ErrNotExist
}

// spellings answers the file names a command started as path can have. NT
// looks a command up by its .exe name, and argv carries the name without it.
func spellings(path string) []string {
	if runtime.CosmoHostOS() != "windows" || stringslite.HasSuffix(path, ".exe") {
		return []string{path}
	}
	return []string{path, path + ".exe"}
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
	// NT keeps no such bit, so what cosmo reports for one is a guess. Reading
	// it there refuses the running program its own path.
	if runtime.CosmoHostOS() == "windows" {
		return nil
	}
	if (mode & 0111) == 0 {
		return ErrPermission
	}
	return nil
}

// pathListSeparator answers the byte that parts this host's PATH. NT parts
// it with a semicolon, and a colon there cuts every entry off its own drive
// letter.
func pathListSeparator() rune {
	if runtime.CosmoHostOS() == "windows" {
		return ';'
	}
	return PathListSeparator
}

// splitPathList splits a path list.
// This is based on genSplit from strings/strings.go
func splitPathList(pathList string) []string {
	return splitPathListSep(pathList, pathListSeparator())
}

func splitPathListSep(pathList string, sep rune) []string {
	if pathList == "" {
		return nil
	}
	n := 1
	for i := 0; i < len(pathList); i++ {
		if rune(pathList[i]) == sep {
			n++
		}
	}
	start := 0
	a := make([]string, n)
	na := 0
	for i := 0; i+1 <= len(pathList) && na+1 < n; i++ {
		if rune(pathList[i]) == sep {
			a[na] = pathList[start:i]
			na++
			start = i + 1
		}
	}
	a[na] = pathList[start:]
	return a[:na+1]
}
