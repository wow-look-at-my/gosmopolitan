// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package main

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
)

// checkFileMap asserts that the linux loader mapped this program from the
// APE itself. The page holding this function must be a mapping of the file
// os.Executable names. A copy of the program in a memfd or anonymous memory
// would cost every start the whole load span.
func checkFileMap() {
	if host := cosmoHostOS(); host != "linux" {
		ok("filemap", "skipped: a "+host+" host has no /proc/self/maps")
		return
	}
	exe, err := os.Executable()
	if err != nil {
		fail("filemap", "os.Executable(): %v", err)
		return
	}
	var st syscall.Stat_t
	if err := syscall.Stat(exe, &st); err != nil {
		fail("filemap", "stat %s: %v", exe, err)
		return
	}
	maps, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		fail("filemap", "read /proc/self/maps: %v", err)
		return
	}
	pc := uint64(reflect.ValueOf(checkFileMap).Pointer())
	for _, line := range strings.Split(string(maps), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		lo, hi, found := strings.Cut(fields[0], "-")
		if !found {
			continue
		}
		start, err1 := strconv.ParseUint(lo, 16, 64)
		end, err2 := strconv.ParseUint(hi, 16, 64)
		if err1 != nil || err2 != nil || pc < start || pc >= end {
			continue
		}
		inode, err := strconv.ParseUint(fields[4], 10, 64)
		if err != nil {
			fail("filemap", "inode in %q: %v", line, err)
			return
		}
		path := strings.Join(fields[5:], " ")
		if inode != st.Ino {
			fail("filemap", "text at %#x maps %s (inode %d), not %s (inode %d)", pc, path, inode, exe, st.Ino)
			return
		}
		ok("filemap", fmt.Sprintf("text maps from %s", path))
		return
	}
	fail("filemap", "no mapping in /proc/self/maps holds pc %#x", pc)
}
