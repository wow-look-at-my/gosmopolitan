// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package exec_test

import (
	"context"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"internal/testenv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestStrippedPathStartsAChild starts this test binary again with PATH cut
// down to a dot and to nothing. TestCommand needs that to work: it copies
// this binary and runs the copy under exactly those two values.
//
// The host answers this for a program the fork did not build. A copy of a
// system binary, and a copy of an upstream gofmt, both start under every one
// of those values (dats/test/nt-strippedpath.ps1). So a failure here belongs
// to this binary.
//
// The log names every DLL the image imports and the flags the loader reads,
// because the loader reports only a number when it cannot resolve one.
func TestStrippedPathStartsAChild(t *testing.T) {
	maySkipHelperCommand("printpath")
	testenv.MustHaveExec(t)

	self := testenv.Executable(t)
	root := t.TempDir()
	copied := filepath.Join(root, "a.exe")
	writeCopy(t, self, copied)

	describeImage(t, "self", self)
	describeImage(t, "copy", copied)

	for _, image := range []struct {
		what string
		path string
	}{
		{"self", self},
		{"copy", copied},
	} {
		// os/exec keeps the last of a repeated name, so a PATH appended to
		// the inherited block wins.
		system := filepath.Join(os.Getenv("SystemRoot"), "System32")
		for _, value := range []struct {
			what string
			env  []string
		}{
			{"inherited", os.Environ()},
			// The system directory alone. If this starts the image and a dot
			// does not, the missing DLL is a system one the loader declines
			// to find in System32 by itself.
			{"system32", append(os.Environ(), "PATH="+system)},
			{"dot", append(os.Environ(), "PATH=.")},
			{"empty", append(os.Environ(), "PATH=")},
			// An environment with nothing else in it belongs to two other
			// tests. internal/syscall/windows TestRunAtLowIntegrity and
			// net/http/cgi TestEnvOverride each hand a child one. A helper
			// started that way here never returns, because it finds none of
			// what the test harness around it reads.
		} {
			// A child that hangs must not eat the whole package deadline.
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			cmd := exec.CommandContext(ctx, image.path, "printpath")
			cmd.Dir = root
			cmd.Env = value.env
			out, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				t.Errorf("%s under env %s: %v\n%s", image.what, value.what, err, out)
				continue
			}
			t.Logf("%s under env %s: ok, %q", image.what, value.what, strings.TrimSpace(string(out)))
		}
	}
}

// writeCopy copies src to dst the way installExe does.
func writeCopy(t *testing.T, src, dst string) {
	t.Helper()
	from, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	into, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o777)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(into, from); err != nil {
		into.Close()
		t.Fatal(err)
	}
	if err := into.Close(); err != nil {
		t.Fatal(err)
	}
}

// describeImage logs what the NT loader reads before it starts an image: the
// DLLs the import table names, the subsystem, and DependentLoadFlags, which
// decides which directories a dependent DLL may come from.
func describeImage(t *testing.T, what, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := pe.Open(path)
	if err != nil {
		t.Errorf("%s: %v", what, err)
		return
	}
	defer file.Close()

	syms, err := file.ImportedSymbols()
	if err != nil {
		t.Errorf("%s: imported symbols: %v", what, err)
	}
	perDLL := map[string]int{}
	for _, sym := range syms {
		if _, dll, found := strings.Cut(sym, ":"); found {
			perDLL[strings.ToLower(dll)]++
		}
	}
	names := make([]string, 0, len(perDLL))
	for dll := range perDLL {
		names = append(names, dll)
	}
	sort.Strings(names)
	listed := make([]string, len(names))
	for idx, dll := range names {
		listed[idx] = fmt.Sprintf("%s(%d)", dll, perDLL[dll])
	}
	t.Logf("%s %s: %d bytes, imports %s", what, path, info.Size(), strings.Join(listed, " "))

	opt, ok := file.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		t.Logf("%s: not a 64-bit image", what)
		return
	}
	t.Logf("%s: subsystem %d, dllcharacteristics %#x", what, opt.Subsystem, opt.DllCharacteristics)
	dir := opt.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_LOAD_CONFIG]
	if dir.VirtualAddress == 0 || dir.Size < 72 {
		t.Logf("%s: no load config", what)
		return
	}
	// DependentLoadFlags sits at offset 0x46 of the 64-bit load config.
	const flagsOffset = 0x46
	raw := readAt(t, file, dir.VirtualAddress, dir.Size)
	if len(raw) < flagsOffset+2 {
		t.Logf("%s: load config is %d bytes, too short to carry the flags", what, len(raw))
		return
	}
	t.Logf("%s: load config %d bytes, DependentLoadFlags %#x",
		what, dir.Size, binary.LittleEndian.Uint16(raw[flagsOffset:]))
}

// readAt returns size bytes of the section holding the virtual address addr.
func readAt(t *testing.T, file *pe.File, addr, size uint32) []byte {
	t.Helper()
	for _, sec := range file.Sections {
		if addr < sec.VirtualAddress || addr >= sec.VirtualAddress+sec.VirtualSize {
			continue
		}
		data, err := sec.Data()
		if err != nil {
			t.Errorf("section %s: %v", sec.Name, err)
			return nil
		}
		off := addr - sec.VirtualAddress
		end := off + size
		if end > uint32(len(data)) {
			end = uint32(len(data))
		}
		return data[off:end]
	}
	return nil
}
