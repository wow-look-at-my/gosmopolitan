// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package pprof

import (
	"fmt"
	"io"
	"runtime"
	"syscall"
)

// Adds MaxRSS to platforms that are supported.
func addMaxRSS(w io.Writer) {
	var rssToBytes uintptr
	switch runtime.GOOS {
	case "aix", "android", "dragonfly", "freebsd", "linux", "netbsd", "openbsd":
		rssToBytes = 1024
	case "darwin", "ios":
		rssToBytes = 1
	case "illumos", "solaris":
		rssToBytes = uintptr(syscall.Getpagesize())
	case "windows":
		// A cosmo binary on an NT host. runtime.GOOS names the host, and
		// this file's unix build tag admits cosmo, so the switch meets a
		// name it was never written for. NT serves no getrusage, so the
		// profile goes out without the MaxRSS line, which is what the
		// windows build does.
		return
	default:
		panic("unsupported OS")
	}

	var rusage syscall.Rusage
	err := syscall.Getrusage(syscall.RUSAGE_SELF, &rusage)
	if err == nil {
		fmt.Fprintf(w, "# MaxRSS = %d\n", uintptr(rusage.Maxrss)*rssToBytes)
	}
}
