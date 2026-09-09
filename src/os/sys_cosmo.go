// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"errors"
	"runtime"
	"syscall"
)

// One APE runs on three kernels, and each keeps the machine's name
// somewhere else. uname's nodename answers on Linux and on NT, and a
// name of 65 bytes or more arrives truncated there, so a long one is
// re-read from the place the host publishes it in full: /proc on Linux,
// kern.hostname on macOS, where Apple's uname leaves nodename empty.
func hostname() (name string, err error) {
	var un syscall.Utsname
	var buf [512]byte // Enough for a DNS name.
	if syscall.Uname(&un) == nil {
		for i, b := range un.Nodename[:] {
			buf[i] = uint8(b)
			if b == 0 {
				name = string(buf[:i])
				break
			}
		}
		if len(name) > 0 && len(name) < 64 {
			return name, nil
		}
	}

	if runtime.GOOS == "linux" {
		f, err := Open("/proc/sys/kernel/hostname")
		if err != nil {
			return "", err
		}
		defer f.Close()

		n, err := f.Read(buf[:])
		if err != nil {
			return "", err
		}
		if n > 0 && buf[n-1] == '\n' {
			n--
		}
		return string(buf[:n]), nil
	}

	if h := runtime.CosmoHostname(); h != "" {
		return h, nil
	}
	if name != "" {
		// Truncated at 64 bytes, and no fuller source answered. A
		// short name beats an error, and it says so.
		return name, nil
	}
	return "", errors.New("os: no hostname on this " + runtime.GOOS + " host")
}
