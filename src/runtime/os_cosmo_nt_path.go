// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Path translation for the Windows NT personality.
//
// Cosmo binaries and the whole unix-shaped standard library speak
// Linux-style paths. Every emulated file syscall funnels its paths
// through exactly one function pair defined here: ntPathW forward, and
// ntPathToLinux back. Symlinks are unsupported, so readlink is EINVAL
// - Linux's own errno for "not a symlink" - and lstat is stat.

package runtime

import "unsafe"

// ntUTF16Append appends the UTF-16 encoding of the UTF-8 string s to
// dst, flipping '/' to '\' when flipSlash is set. Invalid UTF-8 bytes
// become U+FFFD, matching unicode/utf16's encoder.
func ntUTF16Append(dst []uint16, s string, flipSlash bool) []uint16 {
	for i := uint(0); i < uint(len(s)); {
		c := s[i]
		if c < 0x80 {
			if c == '/' && flipSlash {
				c = '\\'
			}
			dst = append(dst, uint16(c))
			i++
			continue
		}
		r, next := decoderune(s, i)
		i = next
		if r <= 0xFFFF {
			dst = append(dst, uint16(r))
		} else {
			r -= 0x10000
			dst = append(dst, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		}
	}
	return dst
}

func ntIsAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// ntTempPathW caches the GetTempPathW result: UTF-16, WITH the
// trailing backslash the API guarantees, WITHOUT a NUL. nil when the
// call failed. Lazily initialized; a racing double-init is idempotent.
var ntTempPath []uint16
var ntTempPathSet bool

func ntTempPathW() []uint16 {
	if ntTempPathSet {
		return ntTempPath
	}
	var buf [_NT_MAX_PATH + 2]uint16
	n := ntcall(ntGetTempPathWFn, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), 0, 0, 0, 0)
	if n > 0 && n < uintptr(len(buf)) {
		w := make([]uint16, n)
		copy(w, buf[:n])
		ntTempPath = w
	}
	ntTempPathSet = true
	return ntTempPath
}

const _NT_MAX_PATH = 260

// ntPathW converts a Linux-style path to a NUL-terminated Win32 UTF-16
// path, following cosmo libc's own NT munging. It returns nil for the
// empty path, which callers report as ENOENT like Linux. Each rule is
// stated on the case that applies it.
func ntPathW(path string) []uint16 {
	if path == "" {
		return nil
	}
	if path == "/dev/null" {
		return []uint16{'N', 'U', 'L', 0}
	}
	w := make([]uint16, 0, len(path)+8)
	switch {
	case path == "/tmp" || ntHasPrefix(path, "/tmp/"):
		// cosmo's os.TempDir() answers "/tmp" with TMPDIR unset and NT
		// has no /tmp, so the name and its subtree are grafted onto
		// the per-user NT temp directory. os.MkdirTemp then works
		// unmodified.
		if tmp := ntTempPathW(); tmp != nil {
			w = append(w, tmp...) // "C:\...\Temp\"
			rest := path[len("/tmp"):]
			if rest != "" {
				rest = rest[1:] // drop the '/' after "/tmp"
			}
			w = ntUTF16Append(w, rest, true)
			break
		}
		// No temp directory resolvable: fall through to the generic
		// current-drive-rooted rule ("\tmp\...").
		fallthrough
	default:
		switch {
		case len(path) >= 2 && ntIsAlpha(path[0]) && path[1] == ':':
			// Already drive-shaped ("C:\x", "c:/x"): flip slashes only.
			w = ntUTF16Append(w, path, true)
		case path[0] == '/' && len(path) >= 2 && ntIsAlpha(path[1]) &&
			(len(path) == 2 || path[2] == '/'):
			// "/c" or "/c/x": one ASCII letter after the leading
			// slash is a drive letter, the cosmo convention.
			drive := path[1] &^ 0x20 // upper-case for Win32
			w = append(w, uint16(drive), ':')
			rest := path[2:]
			if rest == "" {
				rest = "/" // "/c" alone means the drive root "C:\"
			}
			w = ntUTF16Append(w, rest, true)
		default:
			// Other absolute paths become current-drive rooted
			// ("/foo" -> "\foo"); relative paths pass through. Both
			// resolve against the Win32 process working directory.
			w = ntUTF16Append(w, path, true)
		}
	}
	// A plain path is preferred, because the \\?\ prefix demands
	// backslash-only fully-qualified paths. Prefix it only when a
	// drive-absolute result would not fit the classic MAX_PATH.
	if len(w) >= _NT_MAX_PATH-1 && len(w) >= 2 && w[1] == ':' {
		w = append([]uint16{'\\', '\\', '?', '\\'}, w...)
	}
	return append(w, 0)
}

// ntHasPrefix is strings.HasPrefix; the runtime cannot import strings.
func ntHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// ntPathToLinux converts a Win32 path (no NUL) to the Linux spelling
// and is what getcwd and os.Executable answer with. "\\?\" is
// stripped, a leading drive letter becomes the LOWERCASE "/c/..."
// form, backslashes flip, and a trailing slash is trimmed except at a
// drive root, which becomes the bare "/c". Only the drive changes case.
//
// The lowercase /c/ form is what makes unix-shaped filepath code see
// IsAbs() and Chdir(Getwd()) round-trip through ntPathW exactly. That
// holds for the /c/ form only: a "/tmp/x" path deliberately comes BACK
// as its real "/c/users/.../temp/x" spelling, an aliasing os.SameFile
// settles the way it settles macOS's /var symlink.
func ntPathToLinux(w []uint16) string {
	s := ntUTF16ToString(w)
	if len(s) >= 4 && s[0] == '\\' && s[1] == '\\' && s[2] == '?' && s[3] == '\\' {
		s = s[4:]
	}
	buf := make([]byte, 0, len(s)+4)
	if len(s) >= 2 && ntIsAlpha(s[0]) && s[1] == ':' {
		buf = append(buf, '/', s[0]|0x20)
		s = s[2:]
		if s == "" || s == "\\" || s == "/" {
			return string(buf) // drive root: "/c"
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			c = '/'
		}
		buf = append(buf, c)
	}
	// Trim one trailing slash ("C:\a\" -> "/c/a"), but never the
	// root's own slash.
	if n := len(buf); n > 1 && buf[n-1] == '/' {
		buf = buf[:n-1]
	}
	return string(buf)
}

// ntCPath copies the NUL-terminated UTF-8 C string at p into a Go
// string. The source may live on the calling goroutine's stack, so
// the copy happens before anything here can grow the stack: the
// caller (the nosplit dispatcher) passes p as a real pointer type,
// which stack copying adjusts.
func ntCPath(p *byte) string {
	if p == nil {
		return ""
	}
	n := findnull(p)
	if n == 0 {
		return ""
	}
	buf := make([]byte, n)
	copy(buf, unsafe.Slice(p, n))
	return string(buf)
}
