// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// checkLoadSpan asserts that the linux loader put only this payload's load
// span into the memfd it exec'd. A fat APE also carries the other payload
// and any data appended past the load span. The loader must leave those
// out, or every start copies the whole file.
func checkLoadSpan() {
	if host := cosmoHostOS(); host != "linux" {
		ok("loadspan", "skipped: a "+host+" host boots without a memfd")
		return
	}
	link, err := os.Readlink("/proc/self/exe")
	if err != nil {
		fail("loadspan", "readlink /proc/self/exe: %v", err)
		return
	}
	if !strings.HasPrefix(link, "/memfd:") {
		fail("loadspan", "/proc/self/exe is %q, not the memfd the loader execs", link)
		return
	}
	var mem syscall.Stat_t
	if err := syscall.Stat("/proc/self/exe", &mem); err != nil {
		fail("loadspan", "stat /proc/self/exe: %v", err)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		fail("loadspan", "os.Executable(): %v", err)
		return
	}
	fi, err := os.Stat(exe)
	if err != nil {
		fail("loadspan", "os.Stat(%q): %v", exe, err)
		return
	}
	held := mem.Blocks * 512
	if held >= fi.Size() {
		fail("loadspan", "the memfd holds %d bytes of a %d-byte APE: the loader copied the whole file", held, fi.Size())
		return
	}
	ok("loadspan", fmt.Sprintf("memfd holds %d of %d bytes", held, fi.Size()))
}
