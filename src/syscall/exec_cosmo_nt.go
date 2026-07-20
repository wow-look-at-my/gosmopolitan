// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

// NT leg of process creation (wave 2 chunk B). On a Windows host the
// linux-shaped forkAndExecInChild cannot run - there is no fork - so
// exec_cosmo.go branches here BEFORE any fork machinery and the child
// is launched posix_spawn-style through the runtime's CreateProcessW
// hook (cosmo.WindowsFns.Spawn, runtime.ntSpawn).
//
// This file owns the Windows-specific string algebra, ported from
// upstream src/syscall/exec_windows.go (which never builds for
// GOOS=cosmo): MSVCRT-convention argument quoting (backslash doubling
// before quotes, quote-if-space/tab/empty), the case-insensitively
// sorted double-NUL-terminated UTF-16 environment block, and the
// UTF-16 command line. The runtime hook owns everything that needs
// Win32 state: path translation, the fd table, handle duplication,
// CreateProcessW itself, and the pid->handle table that wait4 reaps.
//
// The status pipe protocol degenerates cleanly: CreatePipe handles
// are born non-inheritable, so the child never holds the pipe's write
// end; once forkExec closes the parent copy, the status read returns
// EOF immediately - the "exec succeeded" outcome. Spawn failures
// surface synchronously as this function's errno instead of arriving
// through the pipe.

package syscall

import (
	"internal/bytealg"
	"internal/runtime/syscall/cosmo"
	"slices"
	"sync"
	"unicode/utf16"
	"unsafe"
)

// ntSpawnMu serializes spawns. The spawn window contains temporarily
// INHERITABLE duplicates of the child's stdio handles, and
// CreateProcessW(bInheritHandles=TRUE) captures every inheritable
// handle in the process - so two overlapping spawns would leak each
// other's stdio into the wrong child (a leaked pipe write end defers
// the reader's EOF until that unrelated child exits). acquireForkLock
// does not mutually exclude concurrent forkers (it only counts them),
// hence this dedicated lock; upstream windows lives with the race,
// upstream unix serializes with ForkLock for the same reason.
var ntSpawnMu sync.Mutex

// ntCStr converts a NUL-terminated byte pointer (produced by
// BytePtrFromString, so interior NULs were already rejected) back to a
// Go string. nil yields "".
func ntCStr(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for ptr := unsafe.Pointer(p); *(*byte)(unsafe.Add(ptr, n)) != 0; n++ {
	}
	return string(unsafe.Slice(p, n))
}

// ntAppendEscapeArg escapes the string s per the MSVCRT command line
// parsing rules and appends the result to b. Verbatim port of
// appendEscapeArg (upstream exec_windows.go):
//   - every backslash is doubled, but only if immediately followed by
//     a double quote;
//   - every double quote is escaped by a backslash;
//   - s is wrapped in double quotes if it is empty or contains a
//     space or tab (with trailing backslashes doubled before the
//     closing quote).
func ntAppendEscapeArg(b []byte, s string) []byte {
	if len(s) == 0 {
		return append(b, `""`...)
	}

	needsBackslash := false
	hasSpace := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\\':
			needsBackslash = true
		case ' ', '\t':
			hasSpace = true
		}
	}

	if !needsBackslash && !hasSpace {
		// No special handling required; normal case.
		return append(b, s...)
	}
	if !needsBackslash {
		// hasSpace is true, so we need to quote the string.
		b = append(b, '"')
		b = append(b, s...)
		return append(b, '"')
	}

	if hasSpace {
		b = append(b, '"')
	}
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		default:
			slashes = 0
		case '\\':
			slashes++
		case '"':
			for ; slashes > 0; slashes-- {
				b = append(b, '\\')
			}
			b = append(b, '\\')
		}
		b = append(b, c)
	}
	if hasSpace {
		for ; slashes > 0; slashes-- {
			b = append(b, '\\')
		}
		b = append(b, '"')
	}

	return b
}

// ntMakeCmdLine builds a command line out of args by escaping
// "special" characters and joining the arguments with spaces (port of
// upstream makeCmdLine).
func ntMakeCmdLine(args []string) string {
	var b []byte
	for _, v := range args {
		if len(b) > 0 {
			b = append(b, ' ')
		}
		b = ntAppendEscapeArg(b, v)
	}
	return string(b)
}

// ntEnvSorted returns envv sorted case-insensitively by name (ASCII
// lowering), as CreateProcess requires of environment blocks. Port of
// upstream envSorted.
func ntEnvSorted(envv []string) []string {
	if len(envv) < 2 {
		return envv
	}

	lowerKeyCache := map[string][]byte{} // lowercased keys to avoid recomputing them in sort
	lowerKey := func(kv string) []byte {
		eq := bytealg.IndexByteString(kv, '=')
		if eq < 0 {
			return nil
		}
		k := kv[:eq]
		v, ok := lowerKeyCache[k]
		if !ok {
			v = []byte(k)
			for i, b := range v {
				// ASCII-only normalization, like upstream: in
				// practice environment names are ASCII.
				if 'a' <= b && b <= 'z' {
					v[i] -= 'a' - 'A'
				}
			}
			lowerKeyCache[k] = v
		}
		return v
	}

	cmpEnv := func(a, b string) int {
		return bytealg.Compare(lowerKey(a), lowerKey(b))
	}

	if !slices.IsSortedFunc(envv, cmpEnv) {
		envv = slices.Clone(envv)
		slices.SortFunc(envv, cmpEnv)
	}
	return envv
}

// ntCreateEnvBlock converts an array of environment strings into the
// UTF-16 block CreateProcessW(CREATE_UNICODE_ENVIRONMENT) requires: a
// case-insensitively sorted sequence of NUL-terminated strings,
// terminated by an extra NUL ("two UCS-2 NULs, or four NUL bytes").
// Port of upstream createEnvBlock; strings containing a NUL yield
// EINVAL.
func ntCreateEnvBlock(envv []string) ([]uint16, Errno) {
	if len(envv) == 0 {
		return []uint16{0, 0}, 0
	}

	envv = ntEnvSorted(envv)

	var length int
	for _, s := range envv {
		if bytealg.IndexByteString(s, 0) != -1 {
			return nil, EINVAL
		}
		length += len(s) + 1
	}
	length += 1

	b := make([]uint16, 0, length)
	for _, s := range envv {
		for _, c := range s {
			b = utf16.AppendRune(b, c)
		}
		b = utf16.AppendRune(b, 0)
	}
	b = utf16.AppendRune(b, 0)
	return b, 0
}

// ntUTF16FromString encodes s as a NUL-terminated UTF-16 sequence in a
// fresh mutable slice (CreateProcessW is documented to scribble on its
// lpCommandLine). Interior NULs yield EINVAL.
func ntUTF16FromString(s string) ([]uint16, Errno) {
	if bytealg.IndexByteString(s, 0) != -1 {
		return nil, EINVAL
	}
	buf := make([]uint16, 0, len(s)+1)
	for _, r := range s {
		buf = utf16.AppendRune(buf, r)
	}
	return append(buf, 0), 0
}

// ntIsAbs reports whether path is absolute in either spelling the
// cosmo path layer accepts: rooted ("/c/...", "/tmp/...") or
// drive-lettered ("C:...").
func ntIsAbs(path string) bool {
	if len(path) > 0 && path[0] == '/' {
		return true
	}
	return len(path) >= 2 && path[1] == ':' &&
		('a' <= path[0] && path[0] <= 'z' || 'A' <= path[0] && path[0] <= 'Z')
}

// ntForkExec is the NT replacement for the fork+exec sequence,
// called from forkAndExecInChild's iswindows branch with the same
// (already C-converted) arguments. The status pipe fd is deliberately
// not among them: the child cannot inherit it (see the file comment).
func ntForkExec(argv0 *byte, argv, envv []*byte, chroot, dir *byte, attr *ProcAttr, sys *SysProcAttr) (pid int, err Errno) {
	// Every fork-flavored SysProcAttr knob is meaningless on NT;
	// refuse loudly rather than silently dropping semantics.
	if chroot != nil || sys.Credential != nil || sys.Ptrace || sys.Setsid ||
		sys.Setpgid || sys.Setctty || sys.Noctty || sys.Foreground || sys.Pgid != 0 ||
		sys.Pdeathsig != 0 || sys.Cloneflags != 0 || sys.Unshareflags != 0 ||
		len(sys.UidMappings) != 0 || len(sys.GidMappings) != 0 ||
		sys.GidMappingsEnableSetgroups || sys.UseCgroupFD || sys.PidFD != nil {
		return 0, ENOSYS
	}
	if len(attr.Files) > 3 {
		// Only the three std handles can be conveyed to an NT child:
		// extra inherited handles would have no fd numbers on the
		// other side (upstream windows rejects >3 the same way).
		return 0, ENOSYS
	}

	args := make([]string, 0, len(argv))
	for _, p := range argv {
		if p == nil {
			break
		}
		args = append(args, ntCStr(p))
	}
	env := make([]string, 0, len(envv))
	for _, p := range envv {
		if p == nil {
			break
		}
		env = append(env, ntCStr(p))
	}
	argv0s := ntCStr(argv0)
	dirs := ntCStr(dir)
	// CreateProcessW resolves the image against the PARENT's cwd but
	// starts the child in lpCurrentDirectory, so absolutize a
	// relative argv0 against attr.Dir (upstream joinExeDirAndFName
	// fixes the same mismatch).
	if dirs != "" && !ntIsAbs(argv0s) {
		argv0s = dirs + "/" + argv0s
	}

	cmdline, errno := ntUTF16FromString(ntMakeCmdLine(args))
	if errno != 0 {
		return 0, errno
	}
	envBlock, errno := ntCreateEnvBlock(env)
	if errno != 0 {
		return 0, errno
	}

	stdio := [3]int32{-1, -1, -1}
	for i, f := range attr.Files {
		if f == ^uintptr(0) {
			continue // nil ProcAttr.Files entry: leave that slot empty
		}
		if f > 1<<30 {
			return 0, EBADF
		}
		stdio[i] = int32(f)
	}

	ntSpawnMu.Lock()
	cpid, spErrno := cosmo.Windows().Spawn(argv0s, dirs, cmdline, envBlock, stdio)
	ntSpawnMu.Unlock()
	if spErrno != 0 {
		return 0, Errno(spErrno)
	}
	return int(cpid), 0
}
