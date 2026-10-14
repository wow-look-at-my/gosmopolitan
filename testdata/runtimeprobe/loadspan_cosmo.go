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

// checkLoadSpan asserts that a start on linux never copies the whole APE.
// The loader maps the payload from the APE file, and then this program's
// text maps from that file. On a mount that refuses the mapping, the loader
// execs a memfd that holds only this payload's load span.
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
		checkMappedFromAPE()
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

// checkMappedFromAPE asserts that this program's text maps from the APE that
// os.Executable names, so no start copied it.
func checkMappedFromAPE() {
	exe, err := os.Executable()
	if err != nil {
		fail("loadspan", "os.Executable(): %v", err)
		return
	}
	maps, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		fail("loadspan", "read /proc/self/maps: %v", err)
		return
	}
	for _, line := range strings.Split(string(maps), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 6 && strings.Contains(fields[1], "x") && fields[5] == exe {
			ok("loadspan", "text maps from "+exe)
			return
		}
	}
	fail("loadspan", "no executable mapping of %q in /proc/self/maps:\n%s", exe, maps)
}
