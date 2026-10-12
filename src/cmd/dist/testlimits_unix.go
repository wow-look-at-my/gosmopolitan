// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package main

import "syscall"

// setTestLimits turns off core files and raises the data limit to its hard
// limit. Some tests need about 300 MB of bss.
func setTestLimits() {
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{}); err != nil {
		fatalf("setrlimit RLIMIT_CORE: %v", err)
	}
	var data syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_DATA, &data); err != nil {
		fatalf("getrlimit RLIMIT_DATA: %v", err)
	}
	if data.Cur != data.Max {
		data.Cur = data.Max
		if err := syscall.Setrlimit(syscall.RLIMIT_DATA, &data); err != nil {
			fatalf("setrlimit RLIMIT_DATA: %v", err)
		}
	}
}
