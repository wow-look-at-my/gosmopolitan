// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// checkCmdline asserts that the host shows this process with the argv a
// native program has. The kernel's copy is what ps, top and htop read. A
// loader that boots an APE gets its own path as argv[0] from the kernel, so
// the loader execs itself again with the program's argv.
func checkCmdline() {
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "APE_REEXEC=") {
			fail("cmdline", "the loader's marker reached the program: %q", entry)
			return
		}
	}
	var got []string
	switch host := cosmoHostOS(); host {
	case "linux":
		raw, err := os.ReadFile("/proc/self/cmdline")
		if err != nil {
			fail("cmdline", "read /proc/self/cmdline: %v", err)
			return
		}
		got = strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	case "darwin":
		raw, err := darwinProcArgs()
		if err != nil {
			fail("cmdline", "KERN_PROCARGS2: %v", err)
			return
		}
		if got, err = parseProcArgs(raw); err != nil {
			fail("cmdline", "KERN_PROCARGS2: %v", err)
			return
		}
	case "windows":
		// No loader runs here. The runtime parses os.Args out of the PE command line, which is the copy the host shows.
		ok("cmdline", "the PE command line is the source of os.Args")
		return
	default:
		fail("cmdline", "no reader for host %q", host)
		return
	}
	if !slices.Equal(got, os.Args) {
		fail("cmdline", "the host shows argv %q, os.Args is %q", got, os.Args)
		return
	}
	// htop shows argv[0] as the program. It must name this file, not a loader.
	exe, err := os.Executable()
	if err != nil {
		fail("cmdline", "os.Executable(): %v", err)
		return
	}
	if filepath.Base(got[0]) != filepath.Base(exe) {
		fail("cmdline", "the host shows argv[0] %q, the program is %q", got[0], exe)
		return
	}
	ok("cmdline", fmt.Sprintf("%q", got[0]))
}

// parseProcArgs reads the argv out of a darwin KERN_PROCARGS2 buffer: a
// 32-bit argc, the exec path, nulls, then argc null-terminated strings.
func parseProcArgs(raw []byte) ([]string, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("%d bytes, too short for argc", len(raw))
	}
	argc := int(binary.LittleEndian.Uint32(raw))
	rest := raw[4:]
	end := slices.Index(rest, 0)
	if end < 0 {
		return nil, fmt.Errorf("the exec path has no terminator")
	}
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	args := make([]string, 0, argc)
	for len(args) < argc {
		end = slices.Index(rest, 0)
		if end < 0 {
			return nil, fmt.Errorf("argv[%d] of %d has no terminator", len(args), argc)
		}
		args = append(args, string(rest[:end]))
		rest = rest[end+1:]
	}
	return args, nil
}
