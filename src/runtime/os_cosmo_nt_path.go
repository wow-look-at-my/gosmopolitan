// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Path translation for the Windows NT personality (wave 2).
//
// Cosmo binaries and the whole unix-shaped standard library speak
// Linux-style paths ("/tmp/x", "/dev/null", forward slashes). Every
// file syscall emulated in os_cosmo_nt_sys.go funnels its paths
// through exactly one function pair defined here:
//
//	ntPathW      Linux-style UTF-8  ->  Win32 UTF-16 (NUL-terminated)
//	ntPathToLinux  Win32 UTF-16     ->  Linux-style UTF-8
//
// POLICY (following cosmo libc's precedent for NT path munging):
//
//	"/dev/null"          -> "NUL"           (the NT null device)
//	"/tmp" or "/tmp/x"   -> GetTempPathW()+"x"
//	    MAGIC, documented here once: cosmo's os.TempDir() returns
//	    "/tmp" when TMPDIR is unset, and NT has no /tmp - so the
//	    bare name and the subtree are grafted onto the per-user NT
//	    temp directory (e.g. "C:\Users\u\AppData\Local\Temp\x").
//	    This makes os.MkdirTemp/CreateTemp work unmodified.
//	"/c" or "/c/x"       -> "C:\" / "C:\x"  (single ASCII letter
//	    after the leading slash = drive letter, cosmo convention)
//	"C:..." or "c:..."   -> passthrough      (already drive-shaped;
//	    slashes flipped)
//	other absolute "/x"  -> "\x"             (current-drive rooted)
//	relative "x/y"       -> "x\y"            (resolved by Win32
//	    against the process working directory, same as Linux)
//
// The reverse direction (getcwd, os.Executable) always produces the
// "/c/..." form with a LOWERCASE drive letter, so unix-shaped
// filepath code sees IsAbs() == true and Chdir(Getwd()) round-trips
// through ntPathW exactly. Note the round trip is exact for the
// /c/-form; a "/tmp/x" path deliberately comes BACK as its real
// "/c/users/.../temp/x" spelling (the probe's checkWd compares via
// os.SameFile for exactly this kind of aliasing, as with macOS's
// /var symlink).
//
// Long paths: plain paths are preferred (CI temp paths are short);
// the \\?\ prefix is prepended only when a converted drive-absolute
// path exceeds the classic MAX_PATH budget. \\?\ requires
// backslash-only, fully-qualified paths, which is what ntPathW
// produces.
//
// No symlink support this wave: readlink returns EINVAL (the Linux
// errno for "not a symlink"), and lstat == stat.

package runtime

import "unsafe"

// ntUTF16Append appends the UTF-16 encoding of the UTF-8 string s to
// dst, flipping '/' to '\' when flipSlash is set. Invalid UTF-8 bytes
// become U+FFFD, matching unicode/utf16's encoder.
func ntUTF16Append(dst []uint16, s string, flipSlash bool) []uint16 {
	for i := 0; i < len(s); {
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

// ntPathW converts a Linux-style path to a NUL-terminated Win32
// UTF-16 path per the policy above. Returns nil for the empty path
// (callers report ENOENT, matching Linux).
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
			// "/c" or "/c/x": drive-letter form.
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
	// Long-path safety: prefer plain paths; prefix \\?\ only when a
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

// ntPathToLinux converts a Win32 path (no NUL) to the Linux-style
// spelling: "\\?\" is stripped, a leading drive letter becomes the
// lower-case "/c/..." form, backslashes flip to forward slashes, and
// a trailing slash is trimmed (except at a drive root, which becomes
// the bare "/c"). Only the drive letter changes case.
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
