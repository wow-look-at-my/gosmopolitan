// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package os

import "runtime"

// PathSeparator stays the slash. Cosmopolitan's own file calls take it, and NT
// accepts it beside the backslash, so one value serves every host.
const PathSeparator = '/' // OS-specific path separator

// PathListSeparator cannot be compiled in. One APE runs where PATH is split on
// a colon and where it is split on a semicolon, so the value belongs to the
// host rather than to the build. The entry stub records the host before any Go
// code runs, so this is answered by the time package initialization reads it.
//
// It is a rune rather than an untyped constant, so a comparison against a byte
// needs a conversion. That is why the callers here spell rune(b).
var PathListSeparator = hostPathListSeparator() // OS-specific path list separator

func hostPathListSeparator() rune {
	if runtime.CosmoHostOS() == "windows" {
		return ';'
	}
	return ':'
}

// IsPathSeparator reports whether c is a directory separator character.
func IsPathSeparator(c uint8) bool {
	return PathSeparator == c
}

// splitPath returns the base name and parent directory.
func splitPath(path string) (string, string) {
	// if no better parent is found, the path is relative from "here"
	dirname := "."

	// Remove all but one leading slash.
	for len(path) > 1 && path[0] == '/' && path[1] == '/' {
		path = path[1:]
	}

	i := len(path) - 1

	// Remove trailing slashes.
	for ; i > 0 && path[i] == '/'; i-- {
		path = path[:i]
	}

	// if no slashes in path, base is path
	basename := path

	// Remove leading directory path
	for i--; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				dirname = path[:1]
			} else {
				dirname = path[:i]
			}
			basename = path[i+1:]
			break
		}
	}

	return dirname, basename
}
