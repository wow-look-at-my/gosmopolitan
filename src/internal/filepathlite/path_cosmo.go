// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

// One APE is handed paths by whichever host it booted on, and on NT those
// carry a drive letter and backslashes. Compiled as unix, every one of them
// reads as a relative path with no volume, which is how a consumer sees
// "go list returned non-absolute Package.Dir: C:\Users\...".
//
// So the four predicates that decide what a path IS follow the host. Separator
// stays the slash: NT accepts it beside the backslash, and it is what this
// package emits.
//
// The NT helpers below are path_windows.go's: a drive letter, a UNC root and
// the device prefixes (\\.\, \\?\, \??\), each taking the host as a parameter
// so a machine that is not NT can still test them.

package filepathlite

import (
	"internal/bytealg"
	"internal/stringslite"
	"runtime"
)

const (
	Separator     = '/' // OS-specific path separator
	ListSeparator = ':' // OS-specific path list separator
)

// onNT reports whether this APE booted on a Windows host. The entry stub
// records that before any Go code runs.
func onNT() bool {
	return runtime.CosmoHostOS() == "windows"
}

func IsPathSeparator(c uint8) bool {
	return ntIsPathSeparator(c, onNT())
}

func ntIsPathSeparator(c uint8, nt bool) bool {
	return c == '/' || (nt && c == '\\')
}

func isLocal(path string) bool {
	if onNT() {
		// A volume or a rooted path belongs to somewhere else.
		if path == "" || ntVolumeNameLen(path, true) != 0 || IsPathSeparator(path[0]) {
			return false
		}
		if stringslite.IndexByte(path, ':') >= 0 {
			return false
		}
	}
	return unixIsLocal(path)
}

func localize(path string) (string, error) {
	if bytealg.IndexByteString(path, 0) >= 0 {
		return "", errInvalidPath
	}
	if onNT() && (stringslite.IndexByte(path, ':') >= 0 || stringslite.IndexByte(path, '\\') >= 0) {
		return "", errInvalidPath
	}
	return path, nil
}

// IsAbs reports whether the path is absolute.
func IsAbs(path string) bool {
	return ntIsAbs(path, onNT())
}

func ntIsAbs(path string, nt bool) bool {
	if !nt {
		return stringslite.HasPrefix(path, "/")
	}
	l := ntVolumeNameLen(path, nt)
	if l == 0 {
		return false
	}
	// A volume that starts with two separators is a UNC root, already absolute.
	if ntIsPathSeparator(path[0], nt) && ntIsPathSeparator(path[1], nt) {
		return true
	}
	path = path[l:]
	if path == "" {
		return false
	}
	return ntIsPathSeparator(path[0], nt)
}

// volumeNameLen returns the length of the leading volume name.
func volumeNameLen(path string) int {
	return ntVolumeNameLen(path, onNT())
}

func ntVolumeNameLen(path string, nt bool) int {
	if !nt {
		return 0
	}
	if len(path) >= 2 && path[1] == ':' {
		// A drive letter. Windows does not insist the letter be in A-Z, so
		// neither does this.
		return 2
	}
	if len(path) == 0 || !ntIsPathSeparator(path[0], nt) {
		return 0
	}
	// A device prefix: \\.\ for a Local Device path, \\?\ or \??\ for a Root
	// Local Device path. The component after the prefix is part of the volume,
	// so Clean does not eat the trailing separator of \\?\c:\ .
	if ntHasPrefixFold(path, `\\.`, nt) || ntHasPrefixFold(path, `\\?`, nt) || ntHasPrefixFold(path, `\??`, nt) {
		if len(path) == 3 {
			return 3 // exactly \\., \\? or \??
		}
		if ntHasPrefixFold(path[4:], `UNC`, nt) {
			// The UNC host and share ride along in the volume prefix, which is
			// what upstream does and what callers expect.
			return ntValidVolumeNameLen(path, ntUNCLen(path, len(`\\.\UNC\`), nt), nt)
		}
		_, rest, ok := ntCutPath(path[4:], nt)
		if !ok {
			return ntValidVolumeNameLen(path, len(path), nt)
		}
		return ntValidVolumeNameLen(path, len(path)-len(rest)-1, nt)
	}
	// A UNC root: two separators, a host, then a share.
	if len(path) >= 2 && ntIsPathSeparator(path[1], nt) {
		return ntValidVolumeNameLen(path, ntUNCLen(path, 2, nt), nt)
	}
	return 0
}

// ntUNCLen returns the volume length of a UNC path. prefixLen is what sits in
// front of the host, so for //host/share it is the length of //.
func ntUNCLen(path string, prefixLen int, nt bool) int {
	count := 0
	for i := prefixLen; i < len(path); i++ {
		if ntIsPathSeparator(path[i], nt) {
			count++
			if count == 2 {
				return i
			}
		}
	}
	return len(path)
}

// ntValidVolumeNameLen rejects a prefix carrying a parent reference, which
// would let Clean walk out of the volume it named.
func ntValidVolumeNameLen(path string, n int, nt bool) int {
	for p := path[:n]; p != ""; {
		var part string
		part, p, _ = ntCutPath(p, nt)
		if part == ".." {
			return 0
		}
	}
	return n
}

// ntHasPrefixFold reports whether path begins with prefix, ignoring case and
// treating every separator as equivalent. A longer path must break on a
// separator, so \\?x does not match \\?.
func ntHasPrefixFold(s, prefix string, nt bool) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if ntIsPathSeparator(prefix[i], nt) {
			if !ntIsPathSeparator(s[i], nt) {
				return false
			}
		} else if ntToUpper(prefix[i]) != ntToUpper(s[i]) {
			return false
		}
	}
	return len(s) == len(prefix) || ntIsPathSeparator(s[len(prefix)], nt)
}

func ntToUpper(c byte) byte {
	if 'a' <= c && c <= 'z' {
		return c - ('a' - 'A')
	}
	return c
}

func ntCutPath(path string, nt bool) (before, after string, found bool) {
	for i := range len(path) {
		if ntIsPathSeparator(path[i], nt) {
			return path[:i], path[i+1:], true
		}
	}
	return path, "", false
}
