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
// The NT helpers below are path_windows.go's, reduced to the two volume shapes
// a host hands out: a drive letter and a UNC root. A device prefix (\\?\,
// \\.\, \??\) is NOT recognized, and one reaching here reads as a UNC root.
// That is deliberate rather than forgotten - the full form drags in reserved
// names, fold comparison and validation, which is a copy of upstream this fork
// would have to re-merge every uprev.

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
	if len(path) < 2 || !ntIsPathSeparator(path[0], nt) || !ntIsPathSeparator(path[1], nt) {
		return 0
	}
	// A UNC root: two separators, a host, then a share. Anything shorter has no
	// complete volume, so it has none.
	rest := path[2:]
	host, rest, ok := ntCutPath(rest, nt)
	if !ok || host == "" {
		return 0
	}
	share, _, _ := ntCutPath(rest, nt)
	if share == "" {
		return 0
	}
	return len(path) - len(rest) + len(share)
}

func ntCutPath(path string, nt bool) (before, after string, found bool) {
	for i := range len(path) {
		if ntIsPathSeparator(path[i], nt) {
			return path[:i], path[i+1:], true
		}
	}
	return path, "", false
}
