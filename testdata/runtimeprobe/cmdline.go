// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// checkCmdline asserts that the host shows this process the way it shows a
// native program. The kernel's argv is what ps and htop read. Its process
// name is what top, Activity Monitor and btop read. A loader that boots an
// APE gets its own path in both. The loader and the boot script put the
// program in their place.
func checkCmdline() {
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "APE_REEXEC=") {
			fail("cmdline", "the loader's marker reached the program: %q", entry)
			return
		}
	}
	exe, err := os.Executable()
	if err != nil {
		fail("cmdline", "os.Executable(): %v", err)
		return
	}
	name := filepath.Base(exe)
	var got []string
	switch host := cosmoHostOS(); host {
	case "linux":
		raw, err := os.ReadFile("/proc/self/cmdline")
		if err != nil {
			fail("cmdline", "read /proc/self/cmdline: %v", err)
			return
		}
		got = strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
		// The kernel keeps a name of up to TASK_COMM_LEN-1 bytes.
		comm, err := os.ReadFile("/proc/self/comm")
		if err != nil {
			fail("cmdline", "read /proc/self/comm: %v", err)
			return
		}
		if bad := nameMismatch("comm", strings.TrimSuffix(string(comm), "\n"), name, 15); bad != "" {
			fail("cmdline", "%s", bad)
			return
		}
	case "darwin":
		if got, err = darwinNames(name); err != nil {
			fail("cmdline", "%v", err)
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
	if filepath.Base(got[0]) != name {
		fail("cmdline", "the host shows argv[0] %q, the program is %q", got[0], exe)
		return
	}
	ok("cmdline", fmt.Sprintf("%q", got[0]))
}

// nameMismatch describes a process name that does not name the program, or
// returns "". The kernel cuts the name to limit bytes.
func nameMismatch(field, got, name string, limit int) string {
	want := name
	if len(want) > limit {
		want = want[:limit]
	}
	if got != want {
		return fmt.Sprintf("the host's %s is %q, want %q for program %q", field, got, want, name)
	}
	return ""
}

// darwinNames checks each name macOS keeps for this process against the
// program's file name, and returns the argv ps reads. top and ps -c read
// p_comm. Activity Monitor reads p_name. btop reads the path the process
// was exec'd from.
func darwinNames(name string) ([]string, error) {
	const ctlKern, kernProc, kernProcPID, kernProcArgs2, kernProcName = 1, 14, 1, 49, 62
	pid := uint32(os.Getpid())
	raw, err := darwinSysctl(ctlKern, kernProcArgs2, pid)
	if err != nil {
		return nil, fmt.Errorf("KERN_PROCARGS2: %v", err)
	}
	execPath, got, err := parseProcArgs(raw)
	if err != nil {
		return nil, fmt.Errorf("KERN_PROCARGS2: %v", err)
	}
	// The loader's argc stays with the kernel. Its arguments read back as
	// empty strings after the program's own argv.
	for len(got) > len(os.Args) && got[len(got)-1] == "" {
		got = got[:len(got)-1]
	}
	if filepath.Base(execPath) != name {
		return nil, fmt.Errorf("the host's exec path is %q, want a file named %q", execPath, name)
	}

	// struct kinfo_proc opens with struct extern_proc, whose p_comm sits at a later byte and holds MAXCOMLEN bytes and a null.
	const commAt, maxComLen = 243, 16
	info, err := darwinSysctl(ctlKern, kernProc, kernProcPID, pid)
	if err != nil {
		return nil, fmt.Errorf("KERN_PROC_PID: %v", err)
	}
	if len(info) < commAt+maxComLen+1 {
		return nil, fmt.Errorf("KERN_PROC_PID: %d bytes, too short for p_comm", len(info))
	}
	if bad := nameMismatch("p_comm", cstring(info[commAt:commAt+maxComLen+1]), name, maxComLen); bad != "" {
		return nil, errors.New(bad)
	}
	pname, err := darwinSysctl(ctlKern, kernProcName)
	if err != nil {
		return nil, fmt.Errorf("KERN_PROCNAME: %v", err)
	}
	if bad := nameMismatch("p_name", cstring(pname), name, 2*maxComLen); bad != "" {
		return nil, errors.New(bad)
	}
	return got, nil
}

// cstring is the text of buf up to its first null.
func cstring(buf []byte) string {
	if end := slices.Index(buf, 0); end >= 0 {
		buf = buf[:end]
	}
	return string(buf)
}

// parseProcArgs reads the exec path and the argv out of a darwin
// KERN_PROCARGS2 buffer: a 32-bit argc, the exec path, nulls, then argc
// null-terminated strings.
func parseProcArgs(raw []byte) (string, []string, error) {
	if len(raw) < 4 {
		return "", nil, fmt.Errorf("%d bytes, too short for argc", len(raw))
	}
	argc := int(binary.LittleEndian.Uint32(raw))
	rest := raw[4:]
	end := slices.Index(rest, 0)
	if end < 0 {
		return "", nil, fmt.Errorf("the exec path has no terminator")
	}
	execPath := string(rest[:end])
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	args := make([]string, 0, argc)
	for len(args) < argc {
		end = slices.Index(rest, 0)
		if end < 0 {
			return "", nil, fmt.Errorf("argv[%d] of %d has no terminator", len(args), argc)
		}
		args = append(args, string(rest[:end]))
		rest = rest[end+1:]
	}
	return execPath, args, nil
}
